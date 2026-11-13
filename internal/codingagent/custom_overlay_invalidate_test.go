package codingagent

import (
	"testing"
	"time"
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
