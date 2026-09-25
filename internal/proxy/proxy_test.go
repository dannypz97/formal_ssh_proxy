package proxy

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dparv/ssh-proxy/internal/config"
)

// fakeUpstream is a minimal in-process SSH server standing in for sshd.
// Supported exec commands:
//
//	"cat"   – echo stdin to stdout until EOF, then exit 0
//	"fail"  – write "out" to stdout and "err" to stderr, then exit 3
//	"winch" – report the first window-change as "COLSxROWS" on stdout, exit 0
type fakeUpstream struct {
	addr string
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func startFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == "up" && string(p) == "uppass" {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(newSigner(t))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					ch, creqs, err := nch.Accept()
					if err != nil {
						continue
					}
					go serveFakeSession(ch, creqs)
				}
			}()
		}
	}()
	return &fakeUpstream{addr: ln.Addr().String()}
}

func serveFakeSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	exit := func(code uint32) {
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
	}
	winch := make(chan string, 1)
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			req.Reply(true, nil)
		case "window-change":
			var wc struct{ Cols, Rows, W, H uint32 }
			ssh.Unmarshal(req.Payload, &wc)
			select {
			case winch <- fmt.Sprintf("%dx%d", wc.Cols, wc.Rows):
			default:
			}
		case "exec":
			var cmd struct{ Command string }
			ssh.Unmarshal(req.Payload, &cmd)
			req.Reply(true, nil)
			switch cmd.Command {
			case "cat":
				io.Copy(ch, ch)
				exit(0)
				return
			case "fail":
				io.WriteString(ch, "out")
				io.WriteString(ch.Stderr(), "err")
				exit(3)
				return
			case "winch":
				go func() {
					// Reply once the window-change arrives; the request loop
					// keeps running meanwhile.
					io.WriteString(ch, <-winch)
					exit(0)
					ch.Close()
				}()
			}
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// writeHostKey writes a fresh OpenSSH-format ed25519 private key.
func writeHostKey(t *testing.T, dir string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "host_key")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type testProxy struct {
	addr   string
	logDir string
	// clientKey is authorized for user "keyuser".
	clientKey ssh.Signer
}

func startProxy(t *testing.T) *testProxy {
	t.Helper()
	up := startFakeUpstream(t)
	dir := t.TempDir()

	clientKey := newSigner(t)
	pubPath := filepath.Join(dir, "keyuser.pub")
	authorized := "# comment line\n\n" + string(ssh.MarshalAuthorizedKey(clientKey.PublicKey()))
	if err := os.WriteFile(pubPath, []byte(authorized), 0o644); err != nil {
		t.Fatal(err)
	}

	host, port, _ := net.SplitHostPort(up.addr)
	portNum, _ := strconv.Atoi(port)

	cfg := &config.Config{
		HostKeyPath: writeHostKey(t, dir),
		Upstream: config.UpstreamConfig{
			Host: host, Port: portNum, Username: "up", Password: "uppass",
		},
		Users: []config.UserConfig{
			{Username: "pwuser", Password: "secret"},
			{Username: "keyuser", PublicKeyPath: pubPath},
		},
		LogDir: filepath.Join(dir, "logs"),
	}
	if err := cfg.EnsureLogDir(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConnection(c)
		}
	}()

	return &testProxy{addr: ln.Addr().String(), logDir: cfg.LogDir, clientKey: clientKey}
}

func (p *testProxy) dial(user string, auth ...ssh.AuthMethod) (*ssh.Client, error) {
	return ssh.Dial("tcp", p.addr, &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

// runWithTimeout fails the test if f doesn't return in time — the classic
// symptom of a proxy that never closes the client channel.
func runWithTimeout(t *testing.T, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("session did not finish: proxy hung")
		return nil
	}
}

func TestAuth(t *testing.T) {
	p := startProxy(t)

	tests := []struct {
		name string
		user string
		auth ssh.AuthMethod
		ok   bool
	}{
		{"password ok", "pwuser", ssh.Password("secret"), true},
		{"password wrong", "pwuser", ssh.Password("nope"), false},
		{"empty password for key-only user", "keyuser", ssh.Password(""), false},
		{"public key ok", "keyuser", ssh.PublicKeys(p.clientKey), true},
		{"public key for other user", "pwuser", ssh.PublicKeys(p.clientKey), false},
		{"unknown user", "mallory", ssh.Password("secret"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := p.dial(tt.user, tt.auth)
			if c != nil {
				c.Close()
			}
			if (err == nil) != tt.ok {
				t.Errorf("dial err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestExecExitStatusAndStderr(t *testing.T) {
	p := startProxy(t)
	c, err := p.dial("pwuser", ssh.Password("secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	sess.Stdout, sess.Stderr = &stdout, &stderr

	err = runWithTimeout(t, func() error { return sess.Run("fail") })
	var exitErr *ssh.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitStatus() != 3 {
		t.Errorf("Run err = %v, want exit status 3", err)
	}
	if stdout.String() != "out" || stderr.String() != "err" {
		t.Errorf("stdout=%q stderr=%q, want \"out\" / \"err\"", stdout.String(), stderr.String())
	}
}

func TestStdinIsForwardedAndLogged(t *testing.T) {
	p := startProxy(t)
	c, err := p.dial("keyuser", ssh.PublicKeys(p.clientKey))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	input := "whoami\nsudo rm -rf /tmp/x\n"
	sess.Stdin = strings.NewReader(input)
	out, err := runWithTimeoutOutput(t, sess, "cat")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != input {
		t.Errorf("echoed %q, want %q", out, input)
	}

	// The log is closed once the session finishes; poll briefly for it.
	var logs []string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		logs, _ = filepath.Glob(filepath.Join(p.logDir, "keyuser_*.log"))
		if len(logs) == 1 {
			if b, _ := os.ReadFile(logs[0]); string(b) == input {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected one log for keyuser containing the stdin, got %v", logs)
}

func runWithTimeoutOutput(t *testing.T, sess *ssh.Session, cmd string) (string, error) {
	var out []byte
	err := runWithTimeout(t, func() error {
		var err error
		out, err = sess.Output(cmd)
		return err
	})
	return string(out), err
}

func TestWindowChangeIsForwarded(t *testing.T) {
	p := startProxy(t)
	c, err := p.dial("pwuser", ssh.Password("secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	sess.Stdout = &stdout
	if err := sess.Start("winch"); err != nil {
		t.Fatal(err)
	}
	if err := sess.WindowChange(50, 132); err != nil {
		t.Fatal(err)
	}
	if err := runWithTimeout(t, sess.Wait); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "132x50" {
		t.Errorf("upstream saw window %q, want 132x50", stdout.String())
	}
}

func TestNonSessionChannelsRejected(t *testing.T) {
	p := startProxy(t)
	c, err := p.dial("pwuser", ssh.Password("secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_, err = c.Dial("tcp", "127.0.0.1:22")
	var openErr *ssh.OpenChannelError
	if !errors.As(err, &openErr) || openErr.Reason != ssh.Prohibited {
		t.Errorf("direct-tcpip err = %v, want administratively prohibited", err)
	}
}
