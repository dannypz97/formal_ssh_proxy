// Package proxy implements the SSH proxy server.
package proxy

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"os"

	"golang.org/x/crypto/ssh"

	"github.com/dparv/ssh-proxy/internal/config"
	"github.com/dparv/ssh-proxy/internal/summarizer"
)

// Server is the SSH proxy server.
type Server struct {
	cfg            *config.Config
	sshConfig      *ssh.ServerConfig
	summaryWorker  *summarizer.Worker
}

// NewServer creates a proxy server from the given configuration.
func NewServer(cfg *config.Config) (*Server, error) {
	s := &Server{cfg: cfg}

	sshCfg := &ssh.ServerConfig{
		PasswordCallback:  s.passwordCallback,
		PublicKeyCallback: s.publicKeyCallback,
	}

	hostKeyBytes, err := os.ReadFile(cfg.HostKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading host key: %w", err)
	}
	hostKey, err := ssh.ParsePrivateKey(hostKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing host key: %w", err)
	}
	sshCfg.AddHostKey(hostKey)

	s.sshConfig = sshCfg

	// Set up optional LLM summarizer.
	if sum := summarizer.NewSummarizer(&cfg.LLM); sum != nil {
		s.summaryWorker = summarizer.NewWorker(sum, 4)
		log.Printf("LLM session summarizer enabled (provider: %s, model: %s)", cfg.LLM.Provider, cfg.LLM.Model)
	}

	return s, nil
}

// ListenAndServe starts accepting SSH connections on the configured address.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	log.Printf("proxy listening on %s", s.cfg.Listen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		go s.handleConnection(conn)
	}
}

// handleConnection performs the SSH handshake and starts proxying.
func (s *Server) handleConnection(netConn net.Conn) {
	defer netConn.Close()

	serverConn, chans, reqs, err := ssh.NewServerConn(netConn, s.sshConfig)
	if err != nil {
		log.Printf("handshake failed from %s: %v", netConn.RemoteAddr(), err)
		return
	}
	defer serverConn.Close()

	username := serverConn.User()
	log.Printf("user %q connected from %s", username, netConn.RemoteAddr())

	// Connect to upstream.
	upstreamClient, err := s.dialUpstream()
	if err != nil {
		log.Printf("upstream dial failed for user %q: %v", username, err)
		return
	}
	defer upstreamClient.Close()

	log.Printf("upstream connection established for user %q", username)

	// Discard global requests (keepalives, etc.) — reply OK to keepalives.
	go ssh.DiscardRequests(reqs)

	// Proxy channels, passing username for session logging.
	s.proxyChannels(username, upstreamClient, chans)

	log.Printf("user %q disconnected", username)
}

// dialUpstream connects and authenticates to the upstream SSH server.
func (s *Server) dialUpstream() (*ssh.Client, error) {
	up := &s.cfg.Upstream

	var authMethods []ssh.AuthMethod

	// Try private key first if configured.
	if up.PrivateKeyPath != "" {
		keyBytes, err := os.ReadFile(up.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("reading upstream private key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parsing upstream private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	// Password fallback.
	if up.Password != "" {
		authMethods = append(authMethods, ssh.Password(up.Password))
	}

	clientCfg := &ssh.ClientConfig{
		User:            up.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", up.Addr(), clientCfg)
	if err != nil {
		return nil, fmt.Errorf("dial upstream %s: %w", up.Addr(), err)
	}
	return client, nil
}

// passwordCallback authenticates a client by password.
func (s *Server) passwordCallback(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	for _, u := range s.cfg.Users {
		if u.Username == conn.User() && u.Password == string(password) {
			return &ssh.Permissions{
				Extensions: map[string]string{"username": u.Username},
			}, nil
		}
	}
	return nil, fmt.Errorf("password auth failed for %q", conn.User())
}

// publicKeyCallback authenticates a client by public key.
func (s *Server) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	for _, u := range s.cfg.Users {
		if u.Username != conn.User() || u.PublicKeyPath == "" {
			continue
		}

		pubKeyBytes, err := os.ReadFile(u.PublicKeyPath)
		if err != nil {
			log.Printf("warning: cannot read public key for user %q: %v", u.Username, err)
			continue
		}

		allowedKey, _, _, _, err := ssh.ParseAuthorizedKey(pubKeyBytes)
		if err != nil {
			log.Printf("warning: cannot parse public key for user %q: %v", u.Username, err)
			continue
		}

		if key.Type() == allowedKey.Type() && bytes.Equal(key.Marshal(), allowedKey.Marshal()) {
			return &ssh.Permissions{
				Extensions: map[string]string{"username": u.Username},
			}, nil
		}
	}
	return nil, fmt.Errorf("public key auth failed for %q", conn.User())
}
