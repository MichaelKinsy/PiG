package hosts

import (
	"testing"
	"time"
)

// Go-only: handle writes the failure answer of a request whose host closed under it (host.Root returns at once, an uncooperative dispatch runs on). The answer
// may be written only when the dispatch is not writing the response: after it ended, or never because it had not started.
func TestResponseHandoffAbandonWaitsForARunningDispatchAndStopsOneThatHasNotStarted(t *testing.T) {
	var running responseHandoff
	if !running.enter() {
		t.Fatal("a dispatch that was not abandoned may start")
	}
	abandoned := make(chan struct{})
	go func() {
		running.abandon()
		close(abandoned)
	}()
	select {
	case <-abandoned:
		t.Fatal("abandon returned while the dispatch was running")
	case <-time.After(100 * time.Millisecond):
	}
	running.leave()
	select {
	case <-abandoned:
	case <-time.After(10 * time.Second):
		t.Fatal("abandon never returned after the dispatch ended")
	}

	var idle responseHandoff
	idle.abandon() // no dispatch started: nothing to wait for
	if idle.enter() {
		t.Fatal("a dispatch may not start once the request was abandoned")
	}
}
