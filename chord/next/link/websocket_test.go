package link_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports the fake-socket half of packages/chord/test/next/link/websocket.test.ts. The loopback `ws` server cases
// ("carry a live reference both ways", "close the connection on a frame above maxPayload (1009)") need createHost and
// the notes types: pending on lane D5. "survive closing a socket that still connects" is ported on the fake socket:
// the link keeps its `error` and `close` listeners until the socket's own close.

const (
	socketConnecting = 0
	socketOpen       = 1
	socketClosing    = 2
	socketClosed     = 3
)

// fakeSocket plays a WebSocket by hand.
type fakeSocket struct {
	mu         sync.Mutex
	readyState int
	binaryType string
	sent       [][]byte
	closes     int
	next       int
	listeners  map[int]struct {
		event link.WebSocketEvent
		fn    func(data any)
	}
}

func newFakeSocket(readyState int) *fakeSocket {
	return &fakeSocket{readyState: readyState, binaryType: "blob", listeners: map[int]struct {
		event link.WebSocketEvent
		fn    func(data any)
	}{}}
}

func (socket *fakeSocket) ReadyState() int { return socket.readyState }

func (socket *fakeSocket) SetBinaryType(binaryType string) { socket.binaryType = binaryType }

func (socket *fakeSocket) Send(data []byte) error {
	socket.sent = append(socket.sent, data)
	return nil
}

func (socket *fakeSocket) Close() {
	socket.closes++
	socket.readyState = socketClosed
}

func (socket *fakeSocket) AddEventListener(event link.WebSocketEvent, listener func(data any)) (remove func()) {
	socket.mu.Lock()
	defer socket.mu.Unlock()
	socket.next++
	id := socket.next
	socket.listeners[id] = struct {
		event link.WebSocketEvent
		fn    func(data any)
	}{event, listener}
	return func() {
		socket.mu.Lock()
		defer socket.mu.Unlock()
		delete(socket.listeners, id)
	}
}

func (socket *fakeSocket) emit(event link.WebSocketEvent, data any) {
	if event == link.WebSocketOpen {
		socket.readyState = socketOpen
	}
	socket.mu.Lock()
	var calls []func(any)
	for _, entry := range socket.listeners {
		if entry.event == event {
			calls = append(calls, entry.fn)
		}
	}
	socket.mu.Unlock()
	for _, call := range calls {
		call(data)
	}
}

func (socket *fakeSocket) listenerCount() int {
	socket.mu.Lock()
	defer socket.mu.Unlock()
	return len(socket.listeners)
}

func TestWebSocketLinksOverAFakeSocket(t *testing.T) {
	t.Run("buffer messages sent while connecting and send them on open, as binary frames (race 29)", func(t *testing.T) {
		socket := newFakeSocket(socketConnecting)
		end := link.WebSocketLink(socket)
		if socket.binaryType != "arraybuffer" {
			t.Fatalf("binaryType = %q", socket.binaryType)
		}
		end.Send([]byte{1})
		end.Send([]byte{2})
		if len(socket.sent) != 0 {
			t.Fatalf("sent while connecting: %v", socket.sent)
		}
		socket.emit(link.WebSocketOpen, nil)
		if len(socket.sent) != 2 || socket.sent[0][0] != 1 || socket.sent[1][0] != 2 {
			t.Fatalf("sent = %v", socket.sent)
		}
		var received recorder[byte]
		end.OnMessage(func(message []byte) { received.add(message[0]) })
		socket.emit(link.WebSocketMessage, []byte{7})
		if !slices.Equal(received.all(), []byte{7}) {
			t.Fatalf("received = %v", received.all())
		}
	})

	t.Run("give a closed link for a socket closing or closed already", func(t *testing.T) {
		for _, state := range []int{socketClosing, socketClosed} {
			closed := false
			link.WebSocketLink(newFakeSocket(state)).OnClose(func() { closed = true })
			if !closed {
				t.Fatalf("state %d gave an open link", state)
			}
		}
	})

	for name, fail := range map[string]func(*fakeSocket){
		"close":        func(socket *fakeSocket) { socket.emit(link.WebSocketClose, nil) },
		"error":        func(socket *fakeSocket) { socket.emit(link.WebSocketError, nil) },
		"a text frame": func(socket *fakeSocket) { socket.emit(link.WebSocketMessage, "text") },
	} {
		t.Run("close once on "+name+", listeners removed", func(t *testing.T) {
			socket := newFakeSocket(socketOpen)
			end := link.WebSocketLink(socket)
			closes := 0
			end.OnClose(func() { closes++ })
			fail(socket)
			socket.emit(link.WebSocketClose, nil)
			end.Send([]byte{1})
			if closes != 1 || len(socket.sent) != 0 || socket.listenerCount() != 0 {
				t.Fatalf("closes = %d, sent = %v, listeners = %d", closes, socket.sent, socket.listenerCount())
			}
		})
	}

	t.Run("survive closing a socket that still connects: a late error is handled, listeners go at the socket's close", func(t *testing.T) {
		socket := newFakeSocket(socketConnecting)
		end := link.WebSocketLink(socket)
		closed := false
		end.OnClose(func() { closed = true })
		end.Close()
		if !closed || socket.closes != 1 {
			t.Fatalf("closed = %v, closes = %d", closed, socket.closes)
		}
		// `ws` emits an error for a socket closed while it connects: the link still handles it.
		if socket.listenerCount() != 2 {
			t.Fatalf("listeners before the socket's close = %d, want error and close", socket.listenerCount())
		}
		socket.emit(link.WebSocketError, nil)
		socket.emit(link.WebSocketClose, nil)
		if socket.listenerCount() != 0 {
			t.Fatalf("listeners after = %d", socket.listenerCount())
		}
	})
}
