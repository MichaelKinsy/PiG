package tui

import (
	"testing"
	"time"
)

// armNext swaps the timer for one the test can read, once the frame in flight is parked: the parked frame does not read afterFunc until its own follow-up
// scheduling, which the release (a channel close) orders after this write.
func (f *frameInFlight) armNext() **manualRenderTimer {
	var next *manualRenderTimer
	f.ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		next = &manualRenderTimer{fn: fn}
		return next
	}
	return &next
}

// A request made AFTER the direct Render cancelled the frame has its own timer: the dropped frame in flight neither stops it nor consumes it, and the
// frame that timer runs draws.
func TestTUIFrameInFlightDroppedFrameKeepsLaterRequest(t *testing.T) {
	f := startFrameInFlight(t)
	f.ui.Render()
	next := f.armNext()
	f.ui.RequestRender()
	if *next == nil {
		t.Fatal("a request after the direct Render armed no frame")
	}
	afterRender := f.comp.Count()
	f.finish(t)
	if got := f.comp.Count(); got != afterRender {
		t.Fatalf("the dropped frame rendered: count %d -> %d", afterRender, got)
	}
	if (*next).stopped {
		t.Fatal("the dropped frame stopped the timer of the request made after the cancel")
	}
	(*next).fn()
	if got := f.comp.Count(); got != afterRender+1 {
		t.Fatalf("the frame of the later request did not render: count %d, want %d", got, afterRender+1)
	}
}

// Start after Stop: the frame in flight at Stop belongs to the stopped screen and draws nothing for the restarted one; Start's own request has its frame.
func TestTUIFrameInFlightAtStopDrawsNothingAfterStart(t *testing.T) {
	f := startFrameInFlight(t)
	f.ui.Stop()
	next := f.armNext()
	f.ui.Start()
	if *next == nil {
		t.Fatal("Start armed no frame")
	}
	afterStart := f.out.String()
	drawn := f.comp.Count()
	f.finish(t)
	if late := f.out.String()[len(afterStart):]; late != "" {
		t.Fatalf("the frame in flight at Stop wrote %q after Start", late)
	}
	if got := f.comp.Count(); got != drawn {
		t.Fatalf("the frame in flight at Stop rendered after Start: count %d -> %d", drawn, got)
	}
	(*next).fn()
	if got := f.comp.Count(); got != drawn+1 {
		t.Fatalf("Start's frame did not render: count %d, want %d", got, drawn+1)
	}
}
