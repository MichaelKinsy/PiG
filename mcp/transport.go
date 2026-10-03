package mcp

import (
	"sync"
)

// Ports packages/mcp/src/transports/transport.ts.

// DefaultMaxMessageBytes bounds one message on a transport.
const DefaultMaxMessageBytes = 16 * 1024 * 1024

// MessageListener receives each JSON-RPC message a transport reads.
type MessageListener func(message JSONRPCMessage)

// ErrorListener receives transport errors that do not close the connection.
type ErrorListener func(err error)

// CloseListener is called once when the transport closes.
type CloseListener func()

// Transport owns framing and I/O for one connection. It delivers individual
// JSON-RPC messages to listeners, in order, from goroutines it owns.
//
// Send may block until the peer answers at the HTTP level, so a caller that
// must not wait (the client) calls it from its own goroutine; a Transport
// therefore accepts concurrent Send calls. Close ends every goroutine the
// transport started before it returns.
type Transport interface {
	Start() error
	Send(message JSONRPCMessage) error
	Close() error
	OnMessage(listener MessageListener) (dispose func())
	OnError(listener ErrorListener) (dispose func())
	OnClose(listener CloseListener) (dispose func())
}

// OrderedSender is implemented by a transport whose Send writes messages in the order the writes took their place, such as a
// stdio pipe or an in-process queue. SendOrdered calls placed once every later Send is written after message, and
// otherwise behaves as Send. The client uses it to write requests in id order; a transport without it (HTTP, where
// Send waits for the reply) gets concurrent Sends in no fixed order.
type OrderedSender interface {
	SendOrdered(message JSONRPCMessage, placed func()) error
}

// ProtocolVersionSetter is implemented by transports that send the negotiated
// protocol version with later requests.
type ProtocolVersionSetter interface {
	SetProtocolVersion(version string)
}

// TransportEvents is listener bookkeeping shared by transports. EmitClose
// fires at most once per transport. Listeners run on the emitting goroutine,
// in registration order, outside the internal lock.
type TransportEvents struct {
	mu          sync.Mutex
	nextID      int
	messages    []messageEntry
	errors      []errorEntry
	closes      []closeEntry
	closeCalled bool
}

type messageEntry struct {
	id int
	fn MessageListener
}

type errorEntry struct {
	id int
	fn ErrorListener
}

type closeEntry struct {
	id int
	fn CloseListener
}

// OnMessage registers a message listener.
func (t *TransportEvents) OnMessage(listener MessageListener) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	id := t.nextID
	t.messages = append(t.messages, messageEntry{id, listener})
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.messages = removeEntry(t.messages, id, func(e messageEntry) int { return e.id })
	}
}

// OnError registers an error listener.
func (t *TransportEvents) OnError(listener ErrorListener) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	id := t.nextID
	t.errors = append(t.errors, errorEntry{id, listener})
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.errors = removeEntry(t.errors, id, func(e errorEntry) int { return e.id })
	}
}

// OnClose registers a close listener.
func (t *TransportEvents) OnClose(listener CloseListener) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	id := t.nextID
	t.closes = append(t.closes, closeEntry{id, listener})
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.closes = removeEntry(t.closes, id, func(e closeEntry) int { return e.id })
	}
}

func removeEntry[T any](entries []T, id int, idOf func(T) int) []T {
	out := make([]T, 0, len(entries))
	for _, e := range entries {
		if idOf(e) != id {
			out = append(out, e)
		}
	}
	return out
}

// EmitMessage delivers a message to the listeners.
func (t *TransportEvents) EmitMessage(message JSONRPCMessage) {
	t.mu.Lock()
	listeners := append([]messageEntry(nil), t.messages...)
	t.mu.Unlock()
	for _, l := range listeners {
		l.fn(message)
	}
}

// EmitError delivers an error to the listeners.
func (t *TransportEvents) EmitError(err error) {
	t.mu.Lock()
	listeners := append([]errorEntry(nil), t.errors...)
	t.mu.Unlock()
	for _, l := range listeners {
		l.fn(err)
	}
}

// EmitClose notifies the close listeners, once.
func (t *TransportEvents) EmitClose() {
	t.mu.Lock()
	if t.closeCalled {
		t.mu.Unlock()
		return
	}
	t.closeCalled = true
	listeners := append([]closeEntry(nil), t.closes...)
	t.mu.Unlock()
	for _, l := range listeners {
		l.fn()
	}
}
