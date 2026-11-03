package link_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// socketCall is one call a link made on its socket: the member and the event it named.
type socketCall struct {
	member string
	event  link.WebSocketEvent
}

// spySocket records the listeners a link registers with the socket's AddEventListener.
type spySocket struct {
	*fakeSocket
	calls []socketCall
}

func (socket *spySocket) AddEventListener(event link.WebSocketEvent, listener func(data any)) (remove func()) {
	socket.calls = append(socket.calls, socketCall{member: "AddEventListener", event: event})
	return socket.fakeSocket.AddEventListener(event, listener)
}

// Pi packages/chord/src/next/link/websocket.ts: a link listens for `open` only while the socket connects, always for `message`, and handles
// `close` and `error` until the socket's own close; ending the link removes the `open` and `message` listeners, and the socket's close removes the
// other two. The removal each AddEventListener returns is what the link calls.
func TestWebSocketLinkRegistersItsListenersWithAddEventListenerAndRemovesThemWhenItEnds(t *testing.T) {
	t.Run("a connecting socket gets open, message, close and error in that order", func(t *testing.T) {
		socket := &spySocket{fakeSocket: newFakeSocket(socketConnecting)}
		end := link.WebSocketLink(socket)
		want := []socketCall{
			{"AddEventListener", link.WebSocketOpen}, {"AddEventListener", link.WebSocketMessage},
			{"AddEventListener", link.WebSocketClose}, {"AddEventListener", link.WebSocketError},
		}
		if !slices.Equal(socket.calls, want) {
			t.Fatalf("listeners registered = %v, want %v", socket.calls, want)
		}
		if got := socket.listenerCount(); got != 4 {
			t.Fatalf("listeners held = %d, want 4", got)
		}
		end.Close()
		if got := socket.listenerCount(); got != 2 {
			t.Fatalf("listeners held after the link closed = %d, want the close and error handlers to stay", got)
		}
		socket.emit(link.WebSocketClose, nil)
		if got := socket.listenerCount(); got != 0 {
			t.Fatalf("listeners held after the socket closed = %d, want none", got)
		}
	})

	t.Run("an open socket gets no open listener", func(t *testing.T) {
		socket := &spySocket{fakeSocket: newFakeSocket(socketOpen)}
		link.WebSocketLink(socket)
		want := []socketCall{
			{"AddEventListener", link.WebSocketMessage}, {"AddEventListener", link.WebSocketClose}, {"AddEventListener", link.WebSocketError},
		}
		if !slices.Equal(socket.calls, want) {
			t.Fatalf("listeners registered = %v, want %v", socket.calls, want)
		}
	})

	t.Run("an error ends the link, and the socket's own close then drops the close and error handlers", func(t *testing.T) {
		socket := &spySocket{fakeSocket: newFakeSocket(socketOpen)}
		end := link.WebSocketLink(socket)
		closed := 0
		end.OnClose(func() { closed++ })
		socket.emit(link.WebSocketError, nil)
		if closed != 1 || socket.listenerCount() != 2 {
			t.Fatalf("after error: closed %d, listeners %d, want 1 and 2", closed, socket.listenerCount())
		}
		socket.emit(link.WebSocketClose, nil)
		if closed != 1 || socket.listenerCount() != 0 {
			t.Fatalf("after close: closed %d, listeners %d, want 1 and 0", closed, socket.listenerCount())
		}
	})
}
