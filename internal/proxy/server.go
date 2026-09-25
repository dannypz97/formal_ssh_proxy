// Package proxy implements the SSH proxy server.
package proxy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/dparv/ssh-proxy/internal/config"
	"github.com/dparv/ssh-proxy/internal/summarizer"
)

const (
	// handshakeTimeout bounds how long an unauthenticated client may take to
	// complete the SSH handshake, so idle connections can't pile up.
	handshakeTimeout = 30 * time.Second
	// upstreamDialTimeout bounds the TCP connect plus SSH handshake to upstream.
	upstreamDialTimeout = 10 * time.Second
	// summaryWorkers is the maximum number of concurrent LLM requests.
	summaryWorkers = 4
)

// Server is the SSH proxy server.
type Server struct {
	cfg           *config.Config
	sshConfig     *ssh.ServerConfig
	upstreamCfg   *ssh.ClientConfig
	userKeys      map[string][]ssh.PublicKey // username → authorized keys
	summaryWorker *summarizer.Worker
}

// NewServer creates a proxy server from the given configuration. All key
// material is loaded and parsed up front so that misconfiguration is reported
// at startup rather than on the first connection.
func NewServer(cfg *config.Config) (*Server, error) {
	s := &Server{cfg: cfg}

	hostKey, err := loadSigner(cfg.HostKeyPath)
	if err != nil {
		return nil, fmt.Errorf("host key: %w", err)
	}
	s.sshConfig = &ssh.ServerConfig{
		PasswordCallback:  s.passwordCallback,
		PublicKeyCallback: s.publicKeyCallback,
	}
	s.sshConfig.AddHostKey(hostKey)

	s.userKeys = make(map[string][]ssh.PublicKey)
	for _, u := range cfg.Users {
		if u.PublicKeyPath == "" {
			continue
		}
		keys, err := loadAuthorizedKeys(u.PublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("user %q public key: %w", u.Username, err)
		}
		s.userKeys[u.Username] = keys
	}

	if s.upstreamCfg, err = newUpstreamClientConfig(&cfg.Upstream); err != nil {
		return nil, err
	}

	// Set up optional LLM summarizer.
	if summ := summarizer.NewSummarizer(&cfg.LLM); summ != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := summ.Validate(ctx)
		cancel()
		switch {
		case errors.Is(err, summarizer.ErrInvalidConfig):
			log.Printf("LLM session summarizer disabled: %v", err)
		default:
			if err != nil {
				log.Printf("warning: could not verify LLM API key, keeping summarizer enabled: %v", err)
			}
			s.summaryWorker = summarizer.NewWorker(summ, summaryWorkers)
			if cfg.LLM.MaxTranscriptChars > 0 {
				s.summaryWorker.SetMaxTranscriptLen(cfg.LLM.MaxTranscriptChars)
			}
			log.Printf("LLM session summarizer enabled (provider: %s, model: %s)", cfg.LLM.Provider, cfg.LLM.Model)
		}
	}

	return s, nil
}

// newUpstreamClientConfig builds the SSH client configuration used for every
// upstream connection.
func newUpstreamClientConfig(up *config.UpstreamConfig) (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod
	if up.PrivateKeyPath != "" {
		signer, err := loadSigner(up.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("upstream private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if up.Password != "" {
		authMethods = append(authMethods, ssh.Password(up.Password))
	}

	clientCfg := &ssh.ClientConfig{
		User:            up.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	if up.KnownHostsPath == "" {
		log.Printf("warning: upstream.known_hosts not set; upstream host key will NOT be verified")
		return clientCfg, nil
	}
	cb, err := knownhosts.New(up.KnownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("upstream known_hosts: %w", err)
	}
	algos, err := knownHostKeyAlgorithms(cb, up.Addr())
	if err != nil {
		return nil, fmt.Errorf("upstream known_hosts %s: %w", up.KnownHostsPath, err)
	}
	clientCfg.HostKeyCallback = cb
	clientCfg.HostKeyAlgorithms = algos
	return clientCfg, nil
}

// knownHostKeyAlgorithms returns the host key algorithms matching the keys
// known for addr. Without this, the server may present a key type that
// isn't in known_hosts (e.g. ECDSA when only Ed25519 is listed) and the
// handshake would fail with a spurious key mismatch.
func knownHostKeyAlgorithms(cb ssh.HostKeyCallback, addr string) ([]string, error) {
	// Probe the callback with a throwaway key: the resulting KeyError lists
	// every key known for addr.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	probe, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	var keyErr *knownhosts.KeyError
	if err := cb(addr, &net.TCPAddr{}, probe.PublicKey()); !errors.As(err, &keyErr) {
		return nil, fmt.Errorf("probing host keys: %v", err)
	}
	if len(keyErr.Want) == 0 {
		return nil, fmt.Errorf("no entry for %s", addr)
	}

	var algos []string
	seen := make(map[string]bool)
	for _, k := range keyErr.Want {
		typ := k.Key.Type()
		candidates := []string{typ}
		if typ == ssh.KeyAlgoRSA {
			candidates = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
		}
		for _, a := range candidates {
			if !seen[a] {
				seen[a] = true
				algos = append(algos, a)
			}
		}
	}
	return algos, nil
}

// Serve accepts SSH connections on the configured address until ctx is
// cancelled. Connections already in progress are not interrupted.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	log.Printf("proxy listening on %s", s.cfg.Listen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Likely transient (e.g. out of file descriptors); back off briefly
			// instead of spinning.
			log.Printf("accept error: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handleConnection(conn)
	}
}

// Shutdown waits for pending LLM summaries to finish, or for ctx to expire.
func (s *Server) Shutdown(ctx context.Context) {
	if s.summaryWorker == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		s.summaryWorker.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("shutdown: gave up waiting for pending session summaries")
	}
}

// handleConnection performs the SSH handshake and starts proxying.
func (s *Server) handleConnection(netConn net.Conn) {
	defer netConn.Close()

	netConn.SetDeadline(time.Now().Add(handshakeTimeout))
	serverConn, chans, reqs, err := ssh.NewServerConn(netConn, s.sshConfig)
	if err != nil {
		log.Printf("handshake failed from %s: %v", netConn.RemoteAddr(), err)
		return
	}
	defer serverConn.Close()
	netConn.SetDeadline(time.Time{})

	username := serverConn.User()
	log.Printf("user %q connected from %s", username, netConn.RemoteAddr())

	// Global requests (keepalives, tcpip-forward, ...) are answered with
	// failure. OpenSSH treats a failure reply to a keepalive as "alive".
	go ssh.DiscardRequests(reqs)

	upstreamClient, err := s.dialUpstream()
	if err != nil {
		log.Printf("upstream dial failed for user %q: %v", username, err)
		return
	}
	defer upstreamClient.Close()

	// If the upstream connection drops, tear down the client connection too,
	// otherwise the client would sit on a dead session.
	go func() {
		upstreamClient.Wait()
		serverConn.Close()
	}()

	log.Printf("upstream connection established for user %q", username)

	s.proxyChannels(username, upstreamClient, chans)

	log.Printf("user %q disconnected", username)
}

// dialUpstream connects and authenticates to the upstream server. The
// deadline covers the SSH handshake as well as the TCP connect (the
// ClientConfig timeout only covers the latter).
func (s *Server) dialUpstream() (*ssh.Client, error) {
	addr := s.cfg.Upstream.Addr()
	conn, err := net.DialTimeout("tcp", addr, upstreamDialTimeout)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(upstreamDialTimeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, s.upstreamCfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// passwordCallback authenticates a client by password. Users without a
// configured password never match, so an empty password can't be used to log
// in as a public-key-only user.
func (s *Server) passwordCallback(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	for _, u := range s.cfg.Users {
		if u.Username != conn.User() || u.Password == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(u.Password), password) == 1 {
			return &ssh.Permissions{}, nil
		}
	}
	return nil, fmt.Errorf("password auth failed for %q", conn.User())
}

// publicKeyCallback authenticates a client by public key.
func (s *Server) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	offered := key.Marshal()
	for _, allowed := range s.userKeys[conn.User()] {
		if bytes.Equal(offered, allowed.Marshal()) {
			return &ssh.Permissions{}, nil
		}
	}
	return nil, fmt.Errorf("public key auth failed for %q", conn.User())
}

// loadSigner reads and parses an unencrypted private key file.
func loadSigner(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return signer, nil
}

// loadAuthorizedKeys parses every key in an OpenSSH authorized_keys-format
// file (a single .pub file is the one-line case).
func loadAuthorizedKeys(path string) ([]ssh.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var keys []ssh.PublicKey
	for i, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil {
			return nil, fmt.Errorf("parsing %s line %d: %w", path, i+1, err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s contains no keys", path)
	}
	return keys, nil
}
