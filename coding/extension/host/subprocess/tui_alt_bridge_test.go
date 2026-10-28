package subprocess

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// serveInFrame hands a call to the goroutine that waits for the callback its parent names; it runs there with that goroutine's frame, and the caller
// returns after it ran.
func TestServeInFrameRunsTheCallOnTheWaitingGoroutine(t *testing.T) {
	s := &tuiAltScreenState{waits: map[string]*tuiAltWait{}}
	wait := &tuiAltWait{runs: make(chan func()), done: make(chan struct{})}
	s.waits["r7"] = wait
	served := make(chan struct{})
	go func() {
		defer close(served)
		(<-wait.runs)()
	}()
	ran := false
	if !s.serveInFrame("r7", func(tui.AltScreenFrame) { ran = true }) {
		t.Fatal("serveInFrame did not run the call on the waiting goroutine")
	}
	if !ran {
		t.Fatal("serveInFrame returned before the call ran")
	}
	<-served
}

// A call whose parent no frame goroutine waits for, or whose wait ended before it took the call, is not served: the caller runs it as any other.
func TestServeInFrameLeavesOtherCallsToTheCaller(t *testing.T) {
	s := &tuiAltScreenState{waits: map[string]*tuiAltWait{}}
	if s.serveInFrame("", func(tui.AltScreenFrame) { t.Error("ran a call with no parent") }) {
		t.Error("served a call with no parent")
	}
	if s.serveInFrame("r1", func(tui.AltScreenFrame) { t.Error("ran a call of an unknown request") }) {
		t.Error("served a call of a request no frame goroutine waits for")
	}
	ended := &tuiAltWait{runs: make(chan func()), done: make(chan struct{})}
	close(ended.done)
	s.waits["r2"] = ended
	result := make(chan bool, 1)
	go func() {
		result <- s.serveInFrame("r2", func(tui.AltScreenFrame) { t.Error("ran a call after the wait ended") })
	}()
	select {
	case served := <-result:
		if served {
			t.Error("served a call after the wait ended")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a call whose wait ended blocks for a goroutine that no longer waits")
	}
}
