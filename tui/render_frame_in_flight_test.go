package tui

import (
	"sync/atomic"
	"testing"
	"time"
)

// A frame of a timer goroutine is in flight once its callback has passed renderScheduled's generation check: from there to its doRender it holds neither
// mu nor outMu, so a caller's Stop or Render can run entirely in between. Pi runs its timer callback on the one event loop, so nothing runs between
// the callback's check and its render (tui.ts:1075-1080), and renderNow cancels the timer and leaves nothing to draw until the next request
// (tui.ts:1026-1031).
//
// The frame is parked at the first t.now() of renderAndReschedule, the call after the check and before the render. The timer is manual, so the test
// starts the callback itself and no other goroutine reads the clock while it is parked.
type frameInFlight struct {
	ui      *TuiMainScreen
	out     *lockedBuffer
	comp    *countingRenderComponent
	release chan struct{}
	done    chan struct{}
}

func startFrameInFlight(t *testing.T) *frameInFlight {
	t.Helper()
	f := &frameInFlight{out: &lockedBuffer{}, comp: &countingRenderComponent{}, release: make(chan struct{}), done: make(chan struct{})}
	f.ui = NewWithOutput(f.out, 20, 5)
	f.ui.Add(f.comp)
	// A drawn frame: Stop then parks the cursor below it, as it does after a picker's last frame.
	f.ui.Render()
	var timer *manualRenderTimer
	f.ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		timer = &manualRenderTimer{fn: fn}
		return timer
	}
	f.ui.RequestRender()
	if timer == nil {
		t.Fatal("RequestRender did not arm a frame")
	}
	parked := make(chan struct{})
	var gate atomic.Bool
	f.ui.now = func() time.Time {
		if gate.CompareAndSwap(true, false) {
			close(parked)
			<-f.release
		}
		return time.Now()
	}
	gate.Store(true)
	go func() {
		defer close(f.done)
		timer.fn()
	}()
	select {
	case <-parked:
	case <-time.After(10 * time.Second):
		t.Fatal("the frame never reached the render")
	}
	return f
}

// finish lets the parked frame go on and returns once its callback has returned.
func (f *frameInFlight) finish(t *testing.T) {
	t.Helper()
	close(f.release)
	select {
	case <-f.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the released frame did not return")
	}
}

// Stop parks the cursor below the content and shows it (tui.ts stop()); a frame that was in flight must not draw after it, as its hide of the cursor
// would leave the shell prompt without one.
func TestTUIFrameInFlightAtStopWritesNothingAfterStop(t *testing.T) {
	f := startFrameInFlight(t)
	f.ui.Stop()
	afterStop := f.out.String()
	drawn := f.comp.Count()
	f.finish(t)
	if late := f.out.String()[len(afterStop):]; late != "" {
		t.Fatalf("a frame in flight wrote %q after Stop returned", late)
	}
	if got := f.comp.Count(); got != drawn {
		t.Fatalf("a frame in flight rendered the components after Stop: count %d -> %d", drawn, got)
	}
}

// A direct Render cancels the frame the timer armed (CancelPendingRender); a frame already in flight must not draw once more after it.
func TestTUIFrameInFlightAtRenderDrawsNothingMore(t *testing.T) {
	f := startFrameInFlight(t)
	f.ui.Render()
	afterRender := f.out.String()
	drawn := f.comp.Count()
	f.finish(t)
	if late := f.out.String()[len(afterRender):]; late != "" {
		t.Fatalf("a frame in flight wrote %q after the direct Render returned", late)
	}
	if got := f.comp.Count(); got != drawn {
		t.Fatalf("a frame in flight rendered the components after the direct Render: count %d -> %d", drawn, got)
	}
}
