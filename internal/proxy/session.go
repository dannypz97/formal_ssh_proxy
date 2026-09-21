package proxy

import (
	"io"
	"log"
	"sync"

	"golang.org/x/crypto/ssh"
)

// proxyChannels accepts channels from the client and proxies each one to the
// upstream server. It blocks until the client connection is done.
func (s *Server) proxyChannels(username string, upstreamClient *ssh.Client, clientChans <-chan ssh.NewChannel) {
	for newChan := range clientChans {
		go s.handleChannel(username, upstreamClient, newChan)
	}
}

// handleChannel proxies a single channel (typically "session") between the
// client and upstream, logging client stdin to a per-session file.
func (s *Server) handleChannel(username string, upstreamClient *ssh.Client, newChan ssh.NewChannel) {
	chanType := newChan.ChannelType()

	// Open a matching channel on the upstream side.
	upstreamChan, upstreamReqs, err := upstreamClient.OpenChannel(chanType, newChan.ExtraData())
	if err != nil {
		if openErr, ok := err.(*ssh.OpenChannelError); ok {
			newChan.Reject(openErr.Reason, openErr.Message)
		} else {
			newChan.Reject(ssh.ConnectionFailed, err.Error())
		}
		return
	}
	defer upstreamChan.Close()

	// Accept the client channel.
	clientChan, clientReqs, err := newChan.Accept()
	if err != nil {
		log.Printf("failed to accept client channel: %v", err)
		return
	}
	defer clientChan.Close()

	// Set up session logger for session channels.
	var sessionLog *SessionLogger
	if chanType == "session" {
		sl, err := NewSessionLogger(s.cfg.LogDir, username)
		if err != nil {
			log.Printf("warning: could not create session log: %v", err)
		} else {
			sessionLog = sl
			defer sl.Close()
			log.Printf("session log started: %s", sl.LogPath)
		}
	}

	// Proxy requests and data bidirectionally.
	var wg sync.WaitGroup

	// Forward channel requests: client → upstream.
	wg.Add(1)
	go func() {
		defer wg.Done()
		proxyRequests(clientReqs, upstreamChan)
	}()

	// Forward channel requests: upstream → client.
	wg.Add(1)
	go func() {
		defer wg.Done()
		proxyRequests(upstreamReqs, clientChan)
	}()

	// Forward data: client → upstream, teeing stdin to the session log.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var dst io.Writer = upstreamChan
		if sessionLog != nil {
			dst = io.MultiWriter(upstreamChan, sessionLog)
		}
		io.Copy(dst, clientChan)
		upstreamChan.CloseWrite()
	}()

	// Forward data: upstream → client.
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(clientChan, upstreamChan)
		clientChan.CloseWrite()
	}()

	wg.Wait()

	if sessionLog != nil {
		log.Printf("session log complete: %s", sessionLog.LogPath)
	}
}

// proxyRequests forwards SSH channel requests (pty-req, shell, exec,
// window-change, env, signal, etc.) from one side to the other.
func proxyRequests(reqs <-chan *ssh.Request, dst ssh.Channel) {
	for req := range reqs {
		ok, err := dst.SendRequest(req.Type, req.WantReply, req.Payload)
		if err != nil {
			log.Printf("error forwarding request %q: %v", req.Type, err)
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}
		if req.WantReply {
			req.Reply(ok, nil)
		}
	}
}
