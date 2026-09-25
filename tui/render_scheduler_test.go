package tui

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

type countingRenderComponent struct {
	mu      sync.Mutex
	renders int
	times   []time.Time
	ch      chan time.Time
}

func (c *countingRenderComponent) Render(width int) []string {
	now := time.Now()
	c.mu.Lock()
	c.renders++
	c.times = append(c.times, now)
	c.mu.Unlock()
	if c.ch != nil {
		select {
		case c.ch <- now:
		default:
		}
	}
	return []string{"x"}
}

func (c *countingRenderComponent) Invalidate() {}

func (c *countingRenderComponent) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renders
}

func waitForRender(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case tm := <-ch:
		return tm
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for scheduled render")
		return time.Time{}
	}
}

type manualRenderTimer struct {
	fn      func()
	stopped bool
}

func (t *manualRenderTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func TestTUIRenderNowCancelsPendingRequestedFrame(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	comp := &countingRenderComponent{}
	ui.Add(comp)

	var timer *manualRenderTimer
	ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		timer = &manualRenderTimer{fn: fn}
		return timer
	}
	var dispatched func()
	ui.SetRenderDispatcher(func(render func()) { dispatched = render })
	ui.RequestRender()
	if timer == nil {
		t.Fatal("RequestRender did not schedule a frame")
	}

	// Let the timer hand its render to the owner loop before the final state
	// change performs a direct render. The queued closure is now too late to
	// cancel through the timer handle alone.
	timer.fn()
	if dispatched == nil {
		t.Fatal("scheduled frame was not handed to the render dispatcher")
	}
	ui.Render()
	if got := comp.Count(); got != 1 {
		t.Fatalf("Render count = %d, want 1", got)
	}

	dispatched()
	if got := comp.Count(); got != 1 {
		t.Fatalf("queued stale frame rendered after direct Render: count = %d, want 1", got)
	}
}

func TestTUICancelPendingRenderInvalidatesDispatchedFrame(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	comp := &countingRenderComponent{}
	ui.Add(comp)

	var timer *manualRenderTimer
	ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		timer = &manualRenderTimer{fn: fn}
		return timer
	}
	var dispatched func()
	ui.SetRenderDispatcher(func(render func()) { dispatched = render })
	ui.RequestRender()
	timer.fn()
	if dispatched == nil {
		t.Fatal("scheduled frame was not handed to the render dispatcher")
	}

	ui.CancelPendingRender()
	dispatched()
	if got := comp.Count(); got != 0 {
		t.Fatalf("cancelled dispatched frame rendered: count = %d, want 0", got)
	}
}

func TestTUIRequestRender_CoalescesPendingCalls(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	comp := &countingRenderComponent{}
	ui.Add(comp)

	// Hold the timer until every request is pending. A zero-delay real timer
	// can run between requests, in which case two frames are legitimate.
	var timer *manualRenderTimer
	ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		if timer != nil {
			t.Fatal("pending requests scheduled more than one timer")
		}
		timer = &manualRenderTimer{fn: fn}
		return timer
	}
	for range 5 {
		ui.requestRender(false)
	}
	if timer == nil {
		t.Fatal("pending requests did not schedule a timer")
	}
	timer.fn()

	if got := comp.Count(); got != 1 {
		t.Fatalf("coalesced requestRender count = %d, want 1", got)
	}
}

func TestTUIRequestRender_RespectsMinInterval(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	comp := &countingRenderComponent{ch: make(chan time.Time, 8)}
	ui.Add(comp)

	firstReq := time.Now()
	ui.requestRender(false)
	firstRender := waitForRender(t, comp.ch)
	if firstRender.Before(firstReq) {
		t.Fatalf("first render timestamp %v before request %v", firstRender, firstReq)
	}

	ui.requestRender(false)
	time.Sleep(5 * time.Millisecond)
	if got := comp.Count(); got != 1 {
		t.Fatalf("second render fired before min interval elapsed; count=%d want 1", got)
	}
	secondRender := waitForRender(t, comp.ch)
	if delta := secondRender.Sub(firstRender); delta < minRenderInterval-4*time.Millisecond {
		t.Fatalf("second render delta = %v, want at least %v", delta, minRenderInterval-4*time.Millisecond)
	}

	time.Sleep(30 * time.Millisecond)
	if got := comp.Count(); got != 2 {
		t.Fatalf("unexpected extra scheduled renders: got %d want 2", got)
	}
}

// TestTUIScheduledRender_RunsThroughDispatcher pins the scheduler race fix:
// when a render dispatcher is installed, a throttled scheduled render must be
// handed to the dispatcher (the owner's main loop) instead of running
// doRender on the throttle-timer goroutine, where it would read the lock-free
// component tree concurrently with the main loop's mutations.
func TestTUIScheduledRender_RunsThroughDispatcher(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	comp := &countingRenderComponent{ch: make(chan time.Time, 8)}
	ui.Add(comp)

	dispatched := make(chan func(), 4)
	ui.SetRenderDispatcher(func(render func()) { dispatched <- render })

	ui.requestRender(false)

	var render func()
	select {
	case render = <-dispatched:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("dispatcher was not invoked for the scheduled render")
	}
	if got := comp.Count(); got != 0 {
		t.Fatalf("scheduled render ran on the timer goroutine (count=%d); it must defer to the dispatcher", got)
	}

	// Running the closure (as the main loop would) performs the render.
	render()
	if got := comp.Count(); got != 1 {
		t.Fatalf("dispatched render did not render; count=%d want 1", got)
	}
}
