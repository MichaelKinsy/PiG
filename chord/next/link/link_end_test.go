package link

import (
	"slices"
	"sync"
	"testing"
)

// A transport that ends while its end's queue is handed over. Pi has no test of it: one thread hands the queue over inside
// onMessage, so a message and a close that the transport brings meanwhile come after the hand-over, in the transport's
// order. In Go the transport's goroutine does not wait for the hand-over, which the goroutine that set the listener makes:
// its message queues behind, and its end has to come behind that. The tests hold the listener in the first message (the
// seam) while the transport delivers, ends, and delivers again.

// told collects what an end's listeners were told, in order.
type told struct {
	mu  sync.Mutex
	got []string
}

func (t *told) note(what string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.got = append(t.got, what)
}

func (t *told) all() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.got)
}

// heldHandOver sets end's listener on a goroutine of its own, as a host sets it while the transport's reader delivers, and
// returns once the listener holds the message hold; release lets the listener go on and waits for the hand-over.
func heldHandOver(end *LinkEnd, seen *told, hold string) (release func()) {
	held, let, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		end.OnMessage(func(message []byte) {
			seen.note(string(message))
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

func TestALinkEndHandsOverWhatArrivedBeforeItsTransportEndedThenEnds(t *testing.T) {
	for _, form := range []struct {
		name string
		// queued arrived before the listener was set; arrived, while the first message is with the listener.
		queued, arrived []string
	}{
		{"arrived during the hand-over", []string{"m1"}, []string{"m2"}},
		{"queued before the listener", []string{"m1", "m2"}, nil},
	} {
		t.Run(form.name, func(t *testing.T) {
			end := &LinkEnd{}
			for _, message := range form.queued {
				end.deliver([]byte(message))
			}
			seen := &told{}
			end.OnClose(func() { seen.note("end") })
			release := heldHandOver(end, seen, "m1")
			for _, message := range form.arrived {
				end.deliver([]byte(message))
			}
			end.ended()
			end.deliver([]byte("after the end"))
			if got := seen.all(); !slices.Equal(got, []string{"m1"}) || end.Closed() {
				t.Errorf("with the first message at the listener: told = %q, closed = %v; want only m1 and an open end: the end follows what arrived before it", got, end.Closed())
			}
			release()
			end.deliver([]byte("after the close"))
			if got := seen.all(); !slices.Equal(got, []string{"m1", "m2", "end"}) || !end.Closed() {
				t.Errorf("told = %q, closed = %v; want m1, m2, end and a closed end: each once, in the transport's order, nothing that arrived after its end", got, end.Closed())
			}
		})
	}
}

// A listener that panics leaves the hand-over: what it had not handed over goes with it (Pi's loop over the spliced queue
// ends at the throw), the listener stays for what arrives later, and a transport's end that waited for the hand-over is
// told.
func TestALinkEndWhoseListenerPanicsDuringTheHandOver(t *testing.T) {
	// panicking sets a listener that panics in m1 once let go, and returns the wait for what its setter recovered.
	panicking := func(end *LinkEnd, seen *told) (recovered func() any) {
		held, let, done := make(chan struct{}), make(chan struct{}), make(chan any)
		go func() {
			defer func() { done <- recover() }()
			end.OnMessage(func(message []byte) {
				seen.note(string(message))
				if string(message) == "m1" {
					close(held)
					<-let
					panic("the listener failed")
				}
			})
		}()
		<-held
		return func() any {
			close(let)
			return <-done
		}
	}

	t.Run("a transport's end that waited is told", func(t *testing.T) {
		end := &LinkEnd{}
		end.deliver([]byte("m1"))
		end.deliver([]byte("m2"))
		seen := &told{}
		end.OnClose(func() { seen.note("end") })
		recovered := panicking(end, seen)
		end.ended()
		if got := recovered(); got != "the listener failed" {
			t.Errorf("recovered = %v, want the listener's panic", got)
		}
		if got := seen.all(); !slices.Equal(got, []string{"m1", "end"}) || !end.Closed() {
			t.Errorf("told = %q, closed = %v; want m1, end and a closed end", got, end.Closed())
		}
	})

	t.Run("a later message goes to the listener and the end is told", func(t *testing.T) {
		end := &LinkEnd{}
		end.deliver([]byte("m1"))
		end.deliver([]byte("m2"))
		seen := &told{}
		end.OnClose(func() { seen.note("end") })
		if got := panicking(end, seen)(); got != "the listener failed" {
			t.Errorf("recovered = %v, want the listener's panic", got)
		}
		end.deliver([]byte("m3"))
		end.ended()
		if got := seen.all(); !slices.Equal(got, []string{"m1", "m3", "end"}) || !end.Closed() {
			t.Errorf("told = %q, closed = %v; want m1, m3, end and a closed end", got, end.Closed())
		}
	})
}
