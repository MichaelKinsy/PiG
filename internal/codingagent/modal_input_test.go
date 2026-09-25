package codingagent

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// TestModalRoute_SendAbortsOnTeardown locks the fix for the /tree freeze:
// the stdin reader must not block forever sending to a modal input channel
// that the modal loop stopped draining (e.g. the user spammed keys as the
// selector closed). setModalInputChannel(nil) closes the paired done channel,
// which the reader's select observes to abandon the stale send.
func TestModalRoute_SendAbortsOnTeardown(t *testing.T) {
	m := &InteractiveMode{}
	ch := make(chan []byte) // unbuffered: a send blocks until something drains it
	m.setModalInputChannel(ch)

	routed := make(chan string, 1)
	go func() {
		modalCh, modalDone := m.modalRoute()
		// Mirror inputLoop's send-or-abort select.
		select {
		case modalCh <- []byte("x"):
			routed <- "delivered"
		case <-modalDone:
			routed <- "aborted"
		}
	}()

	// Let the goroutine reach the blocking send (nothing drains ch).
	time.Sleep(20 * time.Millisecond)
	// Tear the modal down while the reader is blocked. The blocked send
	// must abort instead of deadlocking.
	m.setModalInputChannel(nil)

	select {
	case got := <-routed:
		if got != "aborted" {
			t.Fatalf("expected send to abort on teardown, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader deadlocked: send did not abort on modal teardown")
	}
}

// TestModalRoute_DeliversWhenDrained confirms the abort path does not steal
// input from a live modal: while the channel has a draining reader, the chunk
// is delivered, not dropped.
func TestModalRoute_DeliversWhenDrained(t *testing.T) {
	m := &InteractiveMode{}
	ch := make(chan []byte, 1)
	m.setModalInputChannel(ch)

	modalCh, modalDone := m.modalRoute()
	select {
	case modalCh <- []byte("x"):
	case <-modalDone:
		t.Fatal("aborted unexpectedly while the channel had capacity")
	}
	if got := string(<-ch); got != "x" {
		t.Fatalf("delivered chunk = %q, want \"x\"", got)
	}
}

// TestRouteInputChunk_ChainedModalHandoffNoDeadlock locks the /tree ->
// "Summarize branch?" freeze fix. Between two chained modal selectors the
// route is briefly nil while the main loop is synchronously inside the
// selector chain and therefore NOT draining readCh. A keystroke read in that
// window must not deadlock the input pump on the unbuffered readCh; when the
// next selector arms, the chunk re-routes to it.
func TestRouteInputChunk_ChainedModalHandoffNoDeadlock(t *testing.T) {
	m := &InteractiveMode{}
	// Put the route through one install+teardown so modalChangedCh is armed,
	// matching the real state when the first (tree) selector has just closed.
	m.setModalInputChannel(make(chan []byte, 1))
	m.setModalInputChannel(nil)

	readCh := make(chan inputChunk) // unbuffered, never drained: a busy main loop
	done := make(chan struct{})
	go func() {
		m.routeInputChunk(context.Background(), []byte("x"), readCh)
		close(done)
	}()

	// Main loop stays busy (no readCh drain); the next selector arms shortly.
	time.Sleep(20 * time.Millisecond)
	modalCh := make(chan []byte, 1)
	m.setModalInputChannel(modalCh)

	select {
	case got := <-modalCh:
		if string(got) != "x" {
			t.Fatalf("re-routed chunk = %q, want \"x\"", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input pump deadlocked: stray chunk not re-routed to the armed modal")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("routeInputChunk did not return after delivery")
	}
}

// TestRouteInputChunk_NoModalDeliversToMainLoop confirms the normal path:
// with no modal active, a chunk reaches the main loop via readCh.
func TestRouteInputChunk_NoModalDeliversToMainLoop(t *testing.T) {
	m := &InteractiveMode{}
	readCh := make(chan inputChunk, 1)
	ticket := m.routeInputChunk(context.Background(), []byte("y"), readCh)
	got := <-readCh
	if string(got.data) != "y" {
		t.Fatalf("readCh chunk = %q, want \"y\"", got.data)
	}
	if ticket == nil || got.ticket != ticket {
		t.Fatal("a chunk the main loop takes must carry the ticket the pump waits on")
	}
}

// TestRouteInputChunk_ActiveModalReceives confirms an active modal still gets
// input directly and the main-loop readCh is left untouched.
func TestRouteInputChunk_ActiveModalReceives(t *testing.T) {
	m := &InteractiveMode{}
	modalCh := make(chan []byte, 1)
	m.setModalInputChannel(modalCh)
	readCh := make(chan inputChunk) // must not be used
	if ticket := m.routeInputChunk(context.Background(), []byte("z"), readCh); ticket != nil {
		t.Fatal("a modal chunk has no listener pass for the pump to wait on")
	}
	select {
	case got := <-modalCh:
		if string(got) != "z" {
			t.Fatalf("modal chunk = %q, want \"z\"", got)
		}
	default:
		t.Fatal("active modal did not receive chunk")
	}
}

// Upstream routes each parsed sequence to whichever component is focused when
// dispatch runs. If a modal closes while a sequence is waiting for it, the
// sequence must continue to the restored editor rather than disappear.
func TestRouteInputChunk_ModalTeardownReroutesToMainLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &InteractiveMode{}
		m.setModalInputChannel(make(chan []byte)) // no receiver: hold the handoff
		readCh := make(chan inputChunk, 1)
		done := make(chan struct{})
		go func() {
			m.routeInputChunk(context.Background(), []byte("x"), readCh)
			close(done)
		}()

		synctest.Wait()
		m.setModalInputChannel(nil)

		select {
		case got := <-readCh:
			if string(got.data) != "x" {
				t.Fatalf("re-routed chunk = %q, want \"x\"", got.data)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("modal teardown dropped input instead of restoring it to the editor")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("routeInputChunk did not return after re-routing")
		}
	})
}
