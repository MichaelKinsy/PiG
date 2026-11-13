package tui

import (
	"bytes"
	"testing"
	"time"
)

// Pi tui.ts cancelRenderTimer (:1061-1065) clears the throttled frame's timer, and a cleared timer's callback never runs. Go's timer callback may already be
// running when the owner cancels it, so a frame carries the generation that armed it and a newer generation (a forced render, RequestImmediateRender) or
// Stop() retires it. The retired callback must neither draw nor consume the request that belongs to the newer frame.
func newGenerationUI(t *testing.T) (*TuiMainScreen, *countingRenderComponent, *[]*manualRenderTimer) {
	t.Helper()
	ui := NewWithOutput(&bytes.Buffer{}, 20, 5)
	comp := &countingRenderComponent{}
	ui.Add(comp)
	timers := &[]*manualRenderTimer{}
	ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		timer := &manualRenderTimer{fn: fn}
		*timers = append(*timers, timer)
		return timer
	}
	return ui, comp, timers
}

// A throttled frame armed before a forced render is retired by it: running its callback late draws nothing and leaves the forced frame's request alone, so the
// forced frame still draws exactly once (tui.ts requestRender(force): resetRenderState + requestImmediateRender cancel the older timer).
func TestTUIThrottledFrameRetiredByAForcedRenderNeitherDrawsNorConsumesTheNewRequest(t *testing.T) {
	ui, comp, timers := newGenerationUI(t)
	ui.requestRender(false)
	if len(*timers) != 1 {
		t.Fatalf("requestRender armed %d timers, want 1", len(*timers))
	}
	stale := (*timers)[0]
	ui.requestRender(true)
	if len(*timers) != 2 {
		t.Fatalf("a forced render armed %d timers in all, want 2", len(*timers))
	}
	stale.fn()
	if got := comp.Count(); got != 0 {
		t.Fatalf("the retired frame drew: count = %d, want 0", got)
	}
	(*timers)[1].fn()
	if got := comp.Count(); got != 1 {
		t.Fatalf("the forced frame drew %d times after the retired one ran, want 1", got)
	}
}

// An owner-loop render handed over by a throttled timer is dropped when Stop() ran between the hand-over and the loop turn (tui.ts:1075 checks `stopped` when the timer fires).
func TestTUIStopRetiresARenderAlreadyHandedToTheOwnerLoop(t *testing.T) {
	ui, comp, timers := newGenerationUI(t)
	var dispatched func()
	ui.SetRenderDispatcher(func(render func()) { dispatched = render })
	ui.requestRender(false)
	(*timers)[0].fn()
	if dispatched == nil {
		t.Fatal("the throttled frame was not handed to the owner loop")
	}
	ui.Stop()
	dispatched()
	if got := comp.Count(); got != 0 {
		t.Fatalf("a stopped TUI drew a frame handed over before Stop: count = %d", got)
	}
}

// An immediate render handed to the owner loop is retired by a newer generation even when a later RequestImmediateRender has set the immediate flag again:
// the first callback draws nothing and the newer one draws once (tui.ts:1049-1058 requestImmediateRender cancels the timer and coalesces onto one tick).
func TestTUIImmediateRenderOfASupersededGenerationDrawsNothing(t *testing.T) {
	ui, comp, _ := newGenerationUI(t)
	var dispatched []func()
	ui.SetRenderDispatcher(func(render func()) { dispatched = append(dispatched, render) })
	ui.RequestImmediateRender()
	ui.requestRender(true)
	ui.RequestImmediateRender()
	if len(dispatched) != 2 {
		t.Fatalf("%d immediate renders were handed over, want 2", len(dispatched))
	}
	dispatched[0]()
	if got := comp.Count(); got != 0 {
		t.Fatalf("the superseded immediate render drew: count = %d, want 0", got)
	}
	dispatched[1]()
	if got := comp.Count(); got != 1 {
		t.Fatalf("the current immediate render drew %d times, want 1", got)
	}
}
