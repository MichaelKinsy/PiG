// Package mcptest holds test doubles for package mcp.
package mcptest

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/src/transports/in-memory.ts.

// InMemoryTransport is one end of an in-process transport pair. A message sent
// on one end is delivered to the other end in order, as a copy, on a goroutine
// of the receiving end; Send returns once the receiving listeners have run.
type InMemoryTransport struct {
	mcp.TransportEvents
	mu      sync.Mutex
	peer    *InMemoryTransport
	started bool
	closed  bool
	queue   chan inMemoryDelivery
	done    chan struct{}
	once    sync.Once
}

// inMemoryDelivery is one queued message. delivered closes once the peer's
// listeners have run.
type inMemoryDelivery struct {
	message   mcp.JSONRPCMessage
	delivered chan struct{}
}

func newInMemoryTransport() *InMemoryTransport {
	return &InMemoryTransport{queue: make(chan inMemoryDelivery, 1024), done: make(chan struct{})}
}

// ConnectPeer pairs the transport with peer. It fails when a peer is set.
func (t *InMemoryTransport) ConnectPeer(peer *InMemoryTransport) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.peer != nil {
		return errors.New("In-memory MCP transport already has a peer")
	}
	t.peer = peer
	return nil
}

// Start marks the transport started and begins delivering queued messages.
func (t *InMemoryTransport) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return mcp.NewConnectionClosedError()
	}
	if !t.started {
		t.started = true
		go t.deliverLoop()
	}
	return nil
}

func (t *InMemoryTransport) deliverLoop() {
	for {
		select {
		case delivery := <-t.queue:
			t.mu.Lock()
			closed := t.closed
			t.mu.Unlock()
			if !closed {
				t.EmitMessage(delivery.message)
			}
			close(delivery.delivered)
		case <-t.done:
			return
		}
	}
}

// Send copies message and queues it on the peer.
func (t *InMemoryTransport) Send(message mcp.JSONRPCMessage) error {
	return t.SendOrdered(message, nil)
}

// SendOrdered is Send that calls placed, if not nil, once the message has its place in the peer's queue.
func (t *InMemoryTransport) SendOrdered(message mcp.JSONRPCMessage, placed func()) error {
	t.mu.Lock()
	started, closed, peer := t.started, t.closed, t.peer
	t.mu.Unlock()
	if !started || closed {
		return mcp.NewConnectionClosedError()
	}
	if peer == nil {
		return &mcp.McpConnectionClosedError{Message: "In-memory MCP peer is not connected"}
	}
	peer.mu.Lock()
	peerOK := peer.started && !peer.closed
	peer.mu.Unlock()
	if !peerOK {
		return &mcp.McpConnectionClosedError{Message: "In-memory MCP peer is not connected"}
	}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	copied, err := mcp.ParseJSONRPCMessage(data)
	if err != nil {
		return err
	}
	delivery := inMemoryDelivery{message: copied, delivered: make(chan struct{})}
	select {
	case peer.queue <- delivery:
		if placed != nil {
			placed()
		}
	case <-peer.done:
		return nil
	}
	// Upstream queues a microtask, which runs before the sender's next await
	// continues. Waiting for delivery keeps that order across goroutines. A
	// listener must therefore not Send to the transport that is delivering to it.
	select {
	case <-delivery.delivered:
	case <-peer.done:
	}
	return nil
}

// Close closes both ends and notifies their close listeners.
func (t *InMemoryTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	peer := t.peer
	t.mu.Unlock()
	t.once.Do(func() { close(t.done) })
	t.EmitClose()
	if peer != nil {
		return peer.Close()
	}
	return nil
}

// EmitError lets tests simulate transport-level failures.
func (t *InMemoryTransport) EmitError(err error) { t.TransportEvents.EmitError(err) }

// NewInMemoryTransportPair returns two connected transports.
func NewInMemoryTransportPair() (client, server *InMemoryTransport) {
	client, server = newInMemoryTransport(), newInMemoryTransport()
	_ = client.ConnectPeer(server)
	_ = server.ConnectPeer(client)
	return client, server
}
