package subprocess

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// serveOnOwner hands a call to the owner goroutine that waits for the callback its parent names; it runs there with the members the owner holds for that
// callback, and the caller returns after it ran.
func TestServeOnOwnerRunsTheCallOnTheWaitingGoroutine(t *testing.T) {
	s := &tuiAltScreenState{waits: map[string]*tuiAltWait{}}
	wait := &tuiAltWait{runs: make(chan func()), done: make(chan struct{})}
	s.waits["r7"] = wait
	served := make(chan struct{})
	go func() {
		defer close(served)
		(<-wait.runs)()
	}()
	ran := false
	if !s.serveOnOwner("r7", func(tuiAltMembers, bool) { ran = true }) {
		t.Fatal("serveOnOwner did not run the call on the waiting goroutine")
	}
	if !ran {
		t.Fatal("serveOnOwner returned before the call ran")
	}
	<-served
}

// A call whose parent the owner does not wait for, or whose wait ended before it took the call, is not served: the caller runs it as any other.
func TestServeOnOwnerLeavesOtherCallsToTheCaller(t *testing.T) {
	s := &tuiAltScreenState{waits: map[string]*tuiAltWait{}}
	if s.serveOnOwner("", func(tuiAltMembers, bool) { t.Error("ran a call with no parent") }) {
		t.Error("served a call with no parent")
	}
	if s.serveOnOwner("r1", func(tuiAltMembers, bool) { t.Error("ran a call of an unknown request") }) {
		t.Error("served a call of a request the owner does not wait for")
	}
	ended := &tuiAltWait{runs: make(chan func()), done: make(chan struct{})}
	close(ended.done)
	s.waits["r2"] = ended
	result := make(chan bool, 1)
	go func() {
		result <- s.serveOnOwner("r2", func(tuiAltMembers, bool) { t.Error("ran a call after the wait ended") })
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

// A program that never calls EnableTuiAltScreens does not link the renderer (TestHeadlessEmbedBinaryHoldsNoWordDictionary), so a tui.alt call fails
// with an error that names the call instead of constructing a screen; EnableTuiAltScreens turns the screens on.
func TestTuiAltCallFailsUntilTuiAltScreensAreEnabled(t *testing.T) {
	previous := tuiAltEnabled.Swap(nil)
	t.Cleanup(func() { tuiAltEnabled.Store(previous) })
	b := NewUIBridge(func() {})
	_, err := b.callTuiAlt(nil, "", json.RawMessage(`{"op":"create","id":"s"}`))
	if err == nil || !strings.Contains(err.Error(), "EnableTuiAltScreens") {
		t.Fatalf("tui.alt call before EnableTuiAltScreens: err = %v, want one that names EnableTuiAltScreens", err)
	}
	b.clearTuiAlt("ext", nil, false)
	EnableTuiAltScreens()
	if tuiAltEnabled.Load() == nil {
		t.Fatal("EnableTuiAltScreens left the TuiAltScreen server unset")
	}
	if _, err := b.callTuiAlt(nil, "", json.RawMessage(`{"op":"create","id":"s"}`)); err == nil || !strings.Contains(err.Error(), "needs an extension connection") {
		t.Fatalf("tui.alt call after EnableTuiAltScreens: err = %v, want the screen's own error", err)
	}
}
