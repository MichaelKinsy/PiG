package link_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports the fake-socket half of packages/chord/test/link/websocket.test.ts. The loopback `ws` server cases
// ("carry a live reference both ways", "close the connection on a frame above maxPayload (1009)") need createHost and
// the notes types: they run in chord/next/host/remote_websocket_test.go (TestWebSocketLinksOverALoopbackServer).
// "survive closing a socket that still connects" is ported on the fake socket (Go has no `ws` client socket whose late
// `error` could end the process): the link keeps its `error` and `close` listeners until the socket's own close.

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
	sendErr    error
	closes     int
	onClose    func()
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
	if socket.sendErr != nil {
		return socket.sendErr
	}
	socket.sent = append(socket.sent, data)
	return nil
}

func (socket *fakeSocket) Close() {
	if socket.onClose != nil {
		socket.onClose()
	}
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

// websocket.ts's `send`: `try { socket.send(message) } catch { this.close() }`, and `close` ends the link before closing the socket.
func TestWebSocketLinkSendsOnAnOpenSocketAndClosesOnAThrownSend(t *testing.T) {
	t.Run("send each message to an open socket at once, in order", func(t *testing.T) {
		socket := newFakeSocket(socketOpen)
		end := link.WebSocketLink(socket)
		end.Send([]byte{1})
		end.Send([]byte{2})
		if len(socket.sent) != 2 || socket.sent[0][0] != 1 || socket.sent[1][0] != 2 || socket.closes != 0 {
			t.Fatalf("sent = %v, closes = %d", socket.sent, socket.closes)
		}
		if socket.binaryType != "arraybuffer" {
			t.Fatalf("binaryType = %q", socket.binaryType)
		}
		// an open socket is not waited for: no `open` listener was registered, only message, close and error
		if socket.listenerCount() != 3 {
			t.Fatalf("listeners = %d, want message, close and error", socket.listenerCount())
		}
	})

	t.Run("close the link, then the socket, when a send throws; later sends are dropped", func(t *testing.T) {
		socket := newFakeSocket(socketOpen)
		end := link.WebSocketLink(socket)
		var order []string
		end.OnClose(func() { order = append(order, "link closed") })
		socket.onClose = func() { order = append(order, "socket closed") }
		socket.sendErr = errors.New("send failed")
		end.Send([]byte{1})
		if !slices.Equal(order, []string{"link closed", "socket closed"}) || socket.closes != 1 {
			t.Fatalf("order = %v, socket closes = %d, want the link closed, then the socket, once each", order, socket.closes)
		}
		socket.sendErr = nil
		end.Send([]byte{2})
		end.Close()
		if len(socket.sent) != 0 || socket.closes != 1 || len(order) != 2 {
			t.Fatalf("after the close: sent = %v, closes = %d, order = %v", socket.sent, socket.closes, order)
		}
	})

	t.Run("a socket that is open already is not queued for: a send reaches it, a close closes it once", func(t *testing.T) {
		socket := newFakeSocket(socketOpen)
		end := link.WebSocketLink(socket)
		closes := 0
		end.OnClose(func() { closes++ })
		end.Close()
		end.Close()
		if closes != 1 || socket.closes != 1 || socket.readyState != socketClosed {
			t.Fatalf("link closes = %d, socket closes = %d, state = %d", closes, socket.closes, socket.readyState)
		}
	})
}
