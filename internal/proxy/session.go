package proxy

import (
	"io"
	"log"
	"sync"

	"golang.org/x/crypto/ssh"
)

// proxyChannels accepts channels from the client and proxies each one to the
// upstream server. It blocks until the client connection is done.
func (s *Server) proxyChannels(serverConn *ssh.ServerConn, upstreamClient *ssh.Client, clientChans <-chan ssh.NewChannel) {
	for newChan := range clientChans {
		go s.handleChannel(serverConn, upstreamClient, newChan)
	}
}

// handleChannel proxies a single channel (typically "session") between the
// client and upstream.
func (s *Server) handleChannel(serverConn *ssh.ServerConn, upstreamClient *ssh.Client, newChan ssh.NewChannel) {
	chanType := newChan.ChannelType()

	// Open a matching channel on the upstream side.
	upstreamChan, upstreamReqs, err := upstreamClient.OpenChannel(chanType, newChan.ExtraData())
	if err != nil {
		// If upstream rejects, reject the client with the same reason.
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

	// Forward data: client → upstream (this is the stdin we'll log in Phase 2).
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(upstreamChan, clientChan)
		// Signal EOF to upstream when client stops sending.
		upstreamChan.CloseWrite()
	}()

	// Forward data: upstream → client (stdout/stderr back to client).
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(clientChan, upstreamChan)
		clientChan.CloseWrite()
	}()

	wg.Wait()
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
