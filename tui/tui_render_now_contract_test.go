package tui

import (
	"bytes"
	"testing"
	"time"
)

// renderNowHarness is a renderer whose frame timer and clock the test owns.
type renderNowHarness struct {
	ui     *TuiMainScreen
	comp   *countingRenderComponent
	timers []*manualRenderTimer
	delays []time.Duration
	clock  time.Time
}

func newRenderNowHarness() *renderNowHarness {
	h := &renderNowHarness{comp: &countingRenderComponent{}, clock: time.Unix(1000, 0)}
	h.ui = NewWithOutput(&bytes.Buffer{}, 20, 5)
	h.ui.Add(h.comp)
	h.ui.now = func() time.Time { return h.clock }
	h.ui.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
		timer := &manualRenderTimer{fn: fn}
		h.timers = append(h.timers, timer)
		h.delays = append(h.delays, d)
		return timer
	}
	return h
}

// TestRenderNowCancelsTheScheduledFrameAndTheRequest covers tui.ts renderNow: the pending request is cleared and the scheduled frame cancelled, so the frame
// that was waiting neither renders again nor blocks the next request from scheduling one.
func TestRenderNowCancelsTheScheduledFrameAndTheRequest(t *testing.T) {
	h := newRenderNowHarness()
	h.ui.RequestRender()
	if len(h.timers) != 1 {
		t.Fatalf("RequestRender armed %d frames, want 1", len(h.timers))
	}
	h.ui.RenderNow()
	if got := h.comp.Count(); got != 1 {
		t.Fatalf("RenderNow rendered %d times, want 1", got)
	}
	if !h.timers[0].stopped {
		t.Fatal("RenderNow left the scheduled frame running")
	}
	h.timers[0].fn()
	if got := h.comp.Count(); got != 1 {
		t.Fatalf("the cancelled frame rendered after RenderNow: %d renders", got)
	}
	h.ui.RequestRender()
	if len(h.timers) != 2 {
		t.Fatalf("a request after RenderNow armed %d frames in total, want 2: the request flag survived RenderNow", len(h.timers))
	}
}

// TestRenderNowRecordsTheRenderTime covers tui.ts renderNow's lastRenderAt: a request made 5 ms after RenderNow waits for the rest of the 16 ms minimum render interval,
// and one made at the start of a fresh renderer does not wait.
func TestRenderNowRecordsTheRenderTime(t *testing.T) {
	h := newRenderNowHarness()
	h.ui.RenderNow()
	h.clock = h.clock.Add(5 * time.Millisecond)
	h.ui.RequestRender()
	if len(h.delays) != 1 || h.delays[0] != 11*time.Millisecond {
		t.Fatalf("frame delays after RenderNow = %v, want [11ms]", h.delays)
	}
	fresh := newRenderNowHarness()
	fresh.ui.RequestRender()
	if len(fresh.delays) != 1 || fresh.delays[0] != 0 {
		t.Fatalf("frame delays on a renderer that has not rendered = %v, want [0s]", fresh.delays)
	}
}

// TestRenderNowForceResetsTheRenderState covers tui.ts renderNow(force): force resets the differential state before drawing, so the frame is a full redraw, and an
// unforced call draws incrementally. A TUI that holds a differential-state reset calls it once per forced call and never for an unforced one.
func TestRenderNowForceResetsTheRenderState(t *testing.T) {
	h := newRenderNowHarness()
	resets := 0
	h.ui.resetRenderState = func() { resets++ }
	h.ui.RenderNow()
	if resets != 0 {
		t.Fatalf("an unforced RenderNow reset the render state %d times", resets)
	}
	h.ui.RenderNow(true)
	if resets != 1 {
		t.Fatalf("RenderNow(true) reset the render state %d times, want 1", resets)
	}
	before := h.ui.FullRedraws()
	h.ui.RenderNow(true)
	if h.ui.FullRedraws() != before+1 {
		t.Fatalf("RenderNow(true) FullRedraws %d -> %d, want one more", before, h.ui.FullRedraws())
	}
	before = h.ui.FullRedraws()
	h.ui.RenderNow()
	if h.ui.FullRedraws() != before {
		t.Fatalf("RenderNow() FullRedraws %d -> %d, want no change", before, h.ui.FullRedraws())
	}
}
