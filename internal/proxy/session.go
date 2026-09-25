package proxy

import (
	"errors"
	"io"
	"log"
	"sync"

	"golang.org/x/crypto/ssh"
)

// proxyChannels accepts channels from the client and proxies each one to the
// upstream server. It blocks until the client connection is done.
func (s *Server) proxyChannels(username string, upstreamClient *ssh.Client, clientChans <-chan ssh.NewChannel) {
	for newChan := range clientChans {
		// Only interactive/exec sessions are proxied. Port-forwarding channels
		// (direct-tcpip etc.) would let a user tunnel arbitrary traffic through
		// the upstream host without it ever appearing in the session log.
		if t := newChan.ChannelType(); t != "session" {
			log.Printf("rejecting %q channel from user %q", t, username)
			newChan.Reject(ssh.Prohibited, "only session channels are permitted by this proxy")
			continue
		}
		go s.handleSession(username, upstreamClient, newChan)
	}
}

// handleSession proxies a single "session" channel between the client and
// upstream, logging client stdin to a per-session file.
func (s *Server) handleSession(username string, upstreamClient *ssh.Client, newChan ssh.NewChannel) {
	upstreamChan, upstreamReqs, err := upstreamClient.OpenChannel(newChan.ChannelType(), newChan.ExtraData())
	if err != nil {
		var openErr *ssh.OpenChannelError
		if errors.As(err, &openErr) {
			newChan.Reject(openErr.Reason, openErr.Message)
		} else {
			newChan.Reject(ssh.ConnectionFailed, err.Error())
		}
		return
	}
	defer upstreamChan.Close()

	clientChan, clientReqs, err := newChan.Accept()
	if err != nil {
		log.Printf("failed to accept client channel: %v", err)
		return
	}
	defer clientChan.Close()

	sessionLog, err := NewSessionLogger(s.cfg.LogDir, username)
	if err != nil {
		// Refuse to run an unaudited session.
		log.Printf("error: could not create session log for user %q: %v", username, err)
		return
	}
	log.Printf("session log started: %s", sessionLog.LogPath)

	var wg sync.WaitGroup

	// client stdin → upstream, teed to the session log.
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(io.MultiWriter(upstreamChan, sessionLog), clientChan)
		upstreamChan.CloseWrite()
	}()

	// upstream stdout/stderr → client.
	var outputDone sync.WaitGroup
	outputDone.Add(2)
	go func() {
		defer outputDone.Done()
		io.Copy(clientChan, upstreamChan)
	}()
	go func() {
		defer outputDone.Done()
		io.Copy(clientChan.Stderr(), upstreamChan.Stderr())
	}()
	go func() {
		outputDone.Wait()
		clientChan.CloseWrite()
	}()

	// Channel requests (pty-req, shell, exec, window-change, signal,
	// exit-status, ...) in both directions. Each request stream ends when its
	// channel is closed by the peer, which is our cue to close the other side.
	wg.Add(2)
	go func() {
		defer wg.Done()
		proxyRequests(clientReqs, upstreamChan)
		// Client closed the session: nothing left to deliver to it.
		upstreamChan.Close()
	}()
	go func() {
		defer wg.Done()
		proxyRequests(upstreamReqs, clientChan)
		// Upstream closed (after sending exit-status). Flush any output still
		// buffered, then close the client side so the client's ssh exits.
		outputDone.Wait()
		clientChan.Close()
	}()

	wg.Wait()

	if err := sessionLog.Close(); err != nil {
		log.Printf("warning: closing session log %s: %v", sessionLog.LogPath, err)
	}
	log.Printf("session log complete: %s", sessionLog.LogPath)

	if s.summaryWorker != nil {
		s.summaryWorker.Submit(sessionLog.LogPath, username)
	}
}

// proxyRequests forwards SSH channel requests from one side to the other
// until reqs is closed.
func proxyRequests(reqs <-chan *ssh.Request, dst ssh.Channel) {
	for req := range reqs {
		ok, err := dst.SendRequest(req.Type, req.WantReply, req.Payload)
		if err != nil && !errors.Is(err, io.EOF) {
			log.Printf("error forwarding request %q: %v", req.Type, err)
		}
		if req.WantReply {
			req.Reply(ok && err == nil, nil)
		}
	}
}
