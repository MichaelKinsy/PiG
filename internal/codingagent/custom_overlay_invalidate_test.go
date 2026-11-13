package codingagent

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's TUI.invalidate calls invalidate() on every overlay's component (tui.ts:951-954). A remote component is told by a notify
// (ui.custom.invalidate), and the TUI's owner loop, which calls Invalidate, must never write to the extension's connection: its send
// queue holds 64 frames and then waits for an extension that does not read. Invalidate only counts; the goroutine that delivers
// the component's input publishes the count (serveRemoteInput).
func TestRemoteOverlayInvalidateOnlyCountsOnTheOwnerLoop(t *testing.T) {
	overlay := newCustomOverlay(func() {})
	_, changed := overlay.inputRoute()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			overlay.Invalidate()
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Invalidate waited with no delivery goroutine running")
	}
	select {
	case <-changed:
	default:
		t.Fatal("Invalidate did not wake the delivery goroutine")
	}
	if n := overlay.takeInvalidations(); n != 1000 {
		t.Fatalf("pending invalidations = %d, want 1000", n)
	}
	if n := overlay.takeInvalidations(); n != 0 {
		t.Fatalf("pending invalidations after a take = %d, want 0", n)
	}
}

// blockingInvalidateHost is a remote producer whose connection has no room: each report waits until the test lets it through.
type blockingInvalidateHost struct {
	extension.RemoteOverlayHost
	calls   chan int
	release chan struct{}
}

func (h *blockingInvalidateHost) OnInvalidate(count int) {
	h.calls <- count
	<-h.release
}

// The goroutine that delivers a component's input publishes the invalidations the owner loop counted, as one report with their
// count (ui.custom.invalidate {key, count}). While a report waits for room on the extension's connection, the owner loop keeps
// counting without waiting, and the next report carries everything counted since: a burst is one frame, at most one report per
// component is waiting, and the last invalidation is never lost because the count is only forgotten when it is handed to a report.
func TestRemoteOverlayInvalidationsCoalesceWhileTheConnectionHasNoRoom(t *testing.T) {
	overlay := newCustomOverlay(func() {})
	host := &blockingInvalidateHost{calls: make(chan int), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan bool, 1)
	overlay.Invalidate()
	go func() {
		served <- (&ExtUIContext{}).serveRemoteInput(ctx, func(context.Context, func()) {}, overlay, host, false)
	}()
	report := func(what string) int {
		t.Helper()
		select {
		case n := <-host.calls:
			return n
		case <-time.After(10 * time.Second):
			t.Fatalf("no report of %s", what)
			return 0
		}
	}
	if n := report("the first invalidation"); n != 1 {
		t.Fatalf("first report = %d invalidations, want 1", n)
	}
	// The delivery goroutine now waits in the producer, as on a connection whose queue is full. The owner loop invalidates 200 times.
	burst := make(chan struct{})
	go func() {
		defer close(burst)
		for range 200 {
			overlay.Invalidate()
		}
	}()
	select {
	case <-burst:
	case <-time.After(10 * time.Second):
		t.Fatal("the owner loop waited for a report that has no room")
	}
	select {
	case n := <-host.calls:
		t.Fatalf("a second report (%d) started while the first was waiting", n)
	default:
	}
	host.release <- struct{}{}
	if n := report("the burst"); n != 200 {
		t.Fatalf("second report = %d invalidations, want the 200 counted while the first waited", n)
	}
	host.release <- struct{}{}
	cancel()
	select {
	case ok := <-served:
		if ok {
			t.Fatal("serveRemoteInput reported a close, want the context's end")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveRemoteInput did not end with its context")
	}
	select {
	case n := <-host.calls:
		t.Fatalf("a third report (%d) for no invalidation", n)
	default:
	}
}
