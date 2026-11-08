package tui

import (
	"bytes"
	"slices"
	"sync"
	"testing"
	"time"
)

type countingRenderComponent struct {
	mu      sync.Mutex
	renders int
}

func (c *countingRenderComponent) Render(width int) []string {
	c.mu.Lock()
	c.renders++
	c.mu.Unlock()
	return []string{"x"}
}

func (c *countingRenderComponent) Invalidate() {}

func (c *countingRenderComponent) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renders
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

// Pi tui.ts:932-934 cancels the render timer when stopping, including a callback
// already runnable on the event loop. The Go timer must share its cancellation lock.
func TestTUIStopCancelsRacingRenderTimer(t *testing.T) {
	ui := NewWithOutput(&bytes.Buffer{}, 20, 5)
	var timer *manualRenderTimer
	ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		timer = &manualRenderTimer{fn: fn}
		return timer
	}
	ui.SetRenderDispatcher(func(func()) { t.Fatal("stopped timer dispatched a render") })
	ui.RequestRender()
	ui.Stop()
	timer.fn()
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

// manualRenderClock is the throttle's time source and timer for the scheduler tests. It owns both halves of the throttle (the clock
// scheduleRenderLocked reads and the timer it arms), so no test depends on how long a real goroutine happens to be descheduled.
type manualRenderClock struct {
	t      *testing.T
	at     time.Time
	delays []time.Duration
	timer  *manualRenderTimer
}

func newManualRenderClock(t *testing.T, ui *TuiMainScreen) *manualRenderClock {
	c := &manualRenderClock{t: t, at: time.Unix(1_000_000, 0)}
	ui.now = func() time.Time { return c.at }
	ui.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
		if c.timer != nil && !c.timer.stopped {
			t.Fatal("a second throttle timer was armed while one was pending")
		}
		c.delays = append(c.delays, d)
		c.timer = &manualRenderTimer{fn: fn}
		return c.timer
	}
	return c
}

// fire runs the pending throttle timer's callback, as the runtime does when it comes due.
func (c *manualRenderClock) fire() {
	c.t.Helper()
	if c.timer == nil || c.timer.stopped {
		c.t.Fatal("no throttle timer is pending")
	}
	timer := c.timer
	c.timer = nil
	timer.fn()
}

// Pi tui.ts scheduleRender: delay = max(0, MIN_RENDER_INTERVAL_MS - (now - lastRenderAt)). A request made soon after a frame waits out the
// rest of the interval, and a request made after the interval has already elapsed renders at once, however the time went by.
func TestTUIRequestRender_RespectsMinInterval(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	comp := &countingRenderComponent{}
	ui.Add(comp)
	clock := newManualRenderClock(t, ui)

	ui.requestRender(false)
	if !slices.Equal(clock.delays, []time.Duration{0}) {
		t.Fatalf("first request delays = %v, want [0]: nothing has rendered yet", clock.delays)
	}
	clock.fire()
	if got := comp.Count(); got != 1 {
		t.Fatalf("first render count = %d, want 1", got)
	}

	clock.at = clock.at.Add(5 * time.Millisecond)
	ui.requestRender(false)
	if want := []time.Duration{0, minRenderInterval - 5*time.Millisecond}; !slices.Equal(clock.delays, want) {
		t.Fatalf("second request delays = %v, want %v: the rest of the interval after the first frame", clock.delays, want)
	}
	if got := comp.Count(); got != 1 {
		t.Fatalf("second render fired before min interval elapsed; count=%d want 1", got)
	}
	clock.fire()
	if got := comp.Count(); got != 2 {
		t.Fatalf("second render count = %d, want 2", got)
	}
	if clock.timer != nil {
		t.Fatal("a render with nothing further requested armed another timer")
	}
}

// The interval is measured from the last frame, not from the request: a request that arrives after the interval has gone by (the caller
// was descheduled, the machine was loaded) renders at once. This is the interleaving that made the real-clock version of the test flaky.
func TestTUIRequestRender_RendersAtOnceAfterMinInterval(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	comp := &countingRenderComponent{}
	ui.Add(comp)
	clock := newManualRenderClock(t, ui)

	ui.requestRender(false)
	clock.fire()
	clock.at = clock.at.Add(minRenderInterval)
	ui.requestRender(false)
	if want := []time.Duration{0, 0}; !slices.Equal(clock.delays, want) {
		t.Fatalf("request after the interval delays = %v, want %v", clock.delays, want)
	}
	clock.fire()
	if got := comp.Count(); got != 2 {
		t.Fatalf("render count = %d, want 2", got)
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
	comp := &countingRenderComponent{}
	ui.Add(comp)
	clock := newManualRenderClock(t, ui)

	var render func()
	ui.SetRenderDispatcher(func(f func()) {
		if render != nil {
			t.Fatal("dispatcher was invoked twice for one scheduled render")
		}
		render = f
	})

	ui.requestRender(false)
	clock.fire()
	if render == nil {
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

// tui.ts requestRender(force = false): without force a request waits for the frame throttle; with force it resets the render state so the next
// frame is a full repaint and renders at once (delay 0), whatever the throttle says. Both renderers and the TUI interface take the argument.
func TestRequestRenderForceRendersAtOnceAsFullRepaint(t *testing.T) {
	for name, build := range map[string]func() (TUI, *tuiBase){
		"main screen": func() (TUI, *tuiBase) { ui := NewWithOutput(&bytes.Buffer{}, 20, 5); return ui, &ui.tuiBase },
		"alt screen": func() (TUI, *tuiBase) {
			ui := newAltScreenForTest(&bytes.Buffer{}, 20, 5, TuiAltScreenOptions{})
			return ui, &ui.tuiBase
		},
	} {
		t.Run(name, func(t *testing.T) {
			ui, base := build()
			ui.Add(NewText("x"))
			base.lastRenderAt = time.Now() // inside the frame throttle
			var delays []time.Duration
			base.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
				delays = append(delays, d)
				return noopTimer{}
			}
			ui.RequestRender()
			if len(delays) != 1 || delays[0] <= 0 {
				t.Fatalf("plain request delays = %v, want one throttled delay", delays)
			}
			base.mu.Lock()
			base.renderRequested, base.renderTimer, base.forceRedraw = false, nil, false
			base.mu.Unlock()
			delays = nil
			ui.RequestRender(true)
			base.mu.Lock()
			forced := base.forceRedraw
			base.mu.Unlock()
			if len(delays) != 1 || delays[0] != 0 || (name == "main screen" && !forced) {
				t.Fatalf("forced request delays = %v forceRedraw = %v, want one immediate full-repaint frame", delays, forced)
			}
		})
	}
}
