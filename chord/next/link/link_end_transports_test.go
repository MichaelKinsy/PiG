package link_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// The transports' ends while a link's queue is handed over (link_end_test.go has the link end's own): what the transport
// delivered before it ended reaches the listener before the close, and this side's Close is at once. Not upstream's: Pi's
// one thread finishes the hand-over before the transport's next event.

// transport is one link end and its transport, played by hand.
type transport struct {
	name string
	end  link.Link
	// arrive is the transport delivering a message; ends, the transport ending.
	arrive func(message string)
	ends   func()
}

func eachTransport(t *testing.T, body func(t *testing.T, played transport)) {
	a, b := link.MemoryLinkPair()
	port := newFakePort()
	closing, text, failing := newFakeSocket(socketOpen), newFakeSocket(socketOpen), newFakeSocket(socketOpen)
	frame := func(socket *fakeSocket) func(string) {
		return func(message string) { socket.emit(link.WebSocketMessage, []byte(message)) }
	}
	for _, played := range []transport{
		{"memory, the peer closes", b, func(message string) {
			a.Send([]byte(message))
			a.Flushed()
		}, func() {
			a.Close()
			a.Flushed()
		}},
		{"ipc, the port disconnects", link.IpcLink(port), func(message string) { port.receive([]byte(message)) }, port.Disconnect},
		{"websocket, the socket closes", link.WebSocketLink(closing), frame(closing), func() { closing.emit(link.WebSocketClose, nil) }},
		{"websocket, a text frame", link.WebSocketLink(text), frame(text), func() { text.emit(link.WebSocketMessage, "text") }},
		{"websocket, the socket fails", link.WebSocketLink(failing), frame(failing), func() { failing.emit(link.WebSocketError, nil) }},
	} {
		t.Run(played.name, func(t *testing.T) { body(t, played) })
	}
}

// heldIn sets end's listener on a goroutine of its own, as a host sets it while the transport's reader delivers, and
// returns once the listener holds the message hold; release lets the listener go on and waits for the hand-over.
func heldIn(end link.Link, seen *recorder[string], hold string) (release func()) {
	held, let, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		end.OnMessage(func(message []byte) {
			seen.add(string(message))
			if string(message) == hold {
				close(held)
				<-let
			}
		})
	}()
	<-held
	return func() {
		close(let)
		<-done
	}
}

func TestALinkHandsOverWhatArrivedBeforeItsTransportEndedThenCloses(t *testing.T) {
	eachTransport(t, func(t *testing.T, played transport) {
		played.arrive("m1")
		var seen recorder[string]
		played.end.OnClose(func() { seen.add("end") })
		release := heldIn(played.end, &seen, "m1")
		played.arrive("m2")
		played.ends()
		played.arrive("after the end")
		if got := seen.all(); !slices.Equal(got, []string{"m1"}) {
			t.Errorf("with the first message at the listener: told = %q, want only m1: the close follows what arrived before it", got)
		}
		release()
		if got := seen.all(); !slices.Equal(got, []string{"m1", "m2", "end"}) {
			t.Errorf("told = %q, want m1, m2, end: each once, in the transport's order", got)
		}
	})
}

func TestALinkClosedByThisSideWhileItsQueueIsHandedOverClosesAtOnce(t *testing.T) {
	t.Run("during the hand-over", func(t *testing.T) {
		eachTransport(t, func(t *testing.T, played transport) {
			played.arrive("m1")
			played.arrive("m2")
			var seen recorder[string]
			played.end.OnClose(func() { seen.add("end") })
			release := heldIn(played.end, &seen, "m1")
			played.end.Close()
			played.arrive("after the close")
			if got := seen.all(); !slices.Equal(got, []string{"m1", "end"}) {
				t.Errorf("with the first message at the listener: told = %q, want m1, end: this side's close does not wait", got)
			}
			release()
			played.arrive("after the hand-over")
			if got := seen.all(); !slices.Equal(got, []string{"m1", "end"}) {
				t.Errorf("told = %q, want m1, end: what was still queued is dropped, and nothing that arrives later is delivered", got)
			}
		})
	})

	t.Run("after the transport ended during the hand-over", func(t *testing.T) {
		eachTransport(t, func(t *testing.T, played transport) {
			played.arrive("m1")
			played.arrive("m2")
			var seen recorder[string]
			played.end.OnClose(func() { seen.add("end") })
			release := heldIn(played.end, &seen, "m1")
			played.ends()
			if got := seen.all(); !slices.Equal(got, []string{"m1"}) {
				t.Errorf("with the first message at the listener: told = %q, want only m1", got)
			}
			played.end.Close()
			if got := seen.all(); !slices.Equal(got, []string{"m1", "end"}) {
				t.Errorf("after this side's close: told = %q, want m1, end: it does not wait for the hand-over", got)
			}
			release()
			if got := seen.all(); !slices.Equal(got, []string{"m1", "end"}) {
				t.Errorf("told = %q, want m1, end: what was still queued is dropped, the close is told once", got)
			}
		})
	})
}

// The close listener finds the link detached from its transport, as Pi's `#end` forgets the port before `ended` tells the
// close: a message it sends goes nowhere.
func TestALinkToldOfItsCloseHasLeftItsTransport(t *testing.T) {
	t.Run("ipc", func(t *testing.T) {
		port := newFakePort()
		end := link.IpcLink(port)
		listeners := -1
		end.OnClose(func() {
			end.Send([]byte("late"))
			listeners = port.listeners()
		})
		end.Close()
		if len(port.sent) != 0 || listeners != 0 {
			t.Errorf("sent = %d, listeners at the close = %d; want none of either", len(port.sent), listeners)
		}
	})

	t.Run("websocket", func(t *testing.T) {
		socket := newFakeSocket(socketOpen)
		end := link.WebSocketLink(socket)
		end.OnClose(func() { end.Send([]byte("late")) })
		end.Close()
		if len(socket.sent) != 0 || socket.closes != 1 {
			t.Errorf("sent = %d, socket closes = %d; want 0 and 1", len(socket.sent), socket.closes)
		}
	})
}
