package tui

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withinFixture is a renderer whose timers are held by hand: every timer the renderer arms is recorded with its delay.
type withinFixture struct {
	ui     *TuiMainScreen
	comp   *countingRenderComponent
	timers []*manualRenderTimer
	delays []time.Duration
}

func newWithinFixture(t *testing.T) *withinFixture {
	t.Helper()
	f := &withinFixture{comp: &countingRenderComponent{}}
	f.ui = NewWithOutput(&bytes.Buffer{}, 20, 5)
	f.ui.Add(f.comp)
	f.ui.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
		timer := &manualRenderTimer{fn: fn}
		f.timers = append(f.timers, timer)
		f.delays = append(f.delays, d)
		return timer
	}
	return f
}

// Pi-Bolt tui.ts requestRenderWithin: a change that need not be drawn at once is drawn by any frame drawn within the time, and a frame is asked for only when none was.
func TestRequestRenderWithinAsksForAFrameOnlyWhenNoneIsDrawn(t *testing.T) {
	f := newWithinFixture(t)
	f.ui.RequestRenderWithin(40 * time.Millisecond)
	if len(f.timers) != 1 || f.delays[0] != 40*time.Millisecond {
		t.Fatalf("timers %d delays %v, want one timer of 40ms", len(f.timers), f.delays)
	}
	if f.comp.Count() != 0 {
		t.Fatal("RequestRenderWithin drew a frame at once")
	}
	f.timers[0].fn()
	if len(f.timers) != 2 {
		t.Fatalf("an expired RequestRenderWithin armed %d timers, want the frame's", len(f.timers))
	}
	f.timers[1].fn()
	if f.comp.Count() != 1 {
		t.Fatalf("frames drawn %d, want 1", f.comp.Count())
	}
}

func TestFrameDrawnWithinTheDelayCarriesTheChange(t *testing.T) {
	f := newWithinFixture(t)
	f.ui.RequestRenderWithin(40 * time.Millisecond)
	f.ui.Render()
	if !f.timers[0].stopped {
		t.Fatal("a frame drawn within the delay left the timer of the change armed")
	}
	// A callback that was already running when the frame began finds its generation gone.
	f.timers[0].fn()
	if len(f.timers) != 1 {
		t.Fatalf("a stale RequestRenderWithin callback asked for a frame (timers %d)", len(f.timers))
	}
	if f.comp.Count() != 1 {
		t.Fatalf("frames drawn %d, want 1", f.comp.Count())
	}
}

func TestRequestRenderWithinIsCoveredByAFrameAlreadyAskedFor(t *testing.T) {
	f := newWithinFixture(t)
	f.ui.RequestRender()
	armed := len(f.timers)
	f.ui.RequestRenderWithin(40 * time.Millisecond)
	if len(f.timers) != armed {
		t.Fatalf("a frame is asked for already, RequestRenderWithin armed a timer (%d -> %d)", armed, len(f.timers))
	}
	f2 := newWithinFixture(t)
	f2.ui.RequestRenderWithin(40 * time.Millisecond)
	f2.ui.RequestRenderWithin(10 * time.Millisecond)
	if len(f2.timers) != 1 {
		t.Fatalf("a second RequestRenderWithin armed another timer (%d)", len(f2.timers))
	}
}

func TestRequestRenderWithinAfterStopAsksForNothing(t *testing.T) {
	f := newWithinFixture(t)
	f.ui.Stop()
	f.ui.RequestRenderWithin(40 * time.Millisecond)
	if len(f.timers) != 0 {
		t.Fatalf("a stopped renderer armed %d timers", len(f.timers))
	}
}

// A request made while a frame renders cannot wait: the frame may have read the component before it changed.
func TestRequestRenderWithinDuringAFrameAsksForTheNextFrame(t *testing.T) {
	f := newWithinFixture(t)
	f.ui.Add(&withinRequestingComponent{ui: f.ui})
	f.ui.Render()
	if len(f.timers) != 1 || f.delays[0] > minRenderInterval {
		t.Fatalf("timers %d delays %v, want the next frame's timer", len(f.timers), f.delays)
	}
	f.timers[0].fn()
	if f.comp.Count() != 2 {
		t.Fatalf("frames drawn %d, want the requested one after the first", f.comp.Count())
	}
}

type withinRequestingComponent struct {
	ui   *TuiMainScreen
	once sync.Once
}

func (c *withinRequestingComponent) Invalidate() {}
func (c *withinRequestingComponent) Render(int) []string {
	c.once.Do(func() { c.ui.RequestRenderWithin(40 * time.Millisecond) })
	return []string{"y"}
}

// withinCountingTUI is a TUI that draws changes within a time.
type withinCountingTUI struct {
	renderCountingTUI
	within  atomic.Int64
	delayNs atomic.Int64
}

func (w *withinCountingTUI) RequestRenderWithin(d time.Duration) {
	w.within.Add(1)
	w.delayNs.Store(int64(d))
}

// Pi-Bolt loader.ts: an animation tick asks for its frame within min(STEP_FRAME_DELAY_MS, interval/2); every other change asks for one at once.
func TestLoaderAnimationStepAsksForItsFrameWithin(t *testing.T) {
	ui := &withinCountingTUI{}
	loader := NewLoader(ui, nil, nil, "Working", &LoaderIndicatorOptions{Frames: []string{"a", "b"}, IntervalMs: 20})
	t.Cleanup(loader.Stop)
	loader.Start()
	if got := ui.requests.Load(); got != 2 {
		t.Fatalf("constructing and starting requested %d frames at once, want 2", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for ui.within.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the animation never asked for a step frame")
		}
		time.Sleep(time.Millisecond)
	}
	loader.Stop()
	if got := ui.requests.Load(); got != 2 {
		t.Fatalf("animation steps requested %d frames at once, want only the constructor's and Start's", got)
	}
	if got := time.Duration(ui.delayNs.Load()); got != 10*time.Millisecond {
		t.Fatalf("a 20 ms step asked within %v, want interval/2 = 10ms", got)
	}

	slow := &withinCountingTUI{}
	slowLoader := NewLoader(slow, nil, nil, "Working", &LoaderIndicatorOptions{Frames: []string{"a", "b"}, IntervalMs: 200})
	t.Cleanup(slowLoader.Stop)
	slowLoader.Start()
	for slow.within.Load() < 1 {
		if time.Now().After(deadline.Add(5 * time.Second)) {
			t.Fatal("the slow animation never asked for a step frame")
		}
		time.Sleep(time.Millisecond)
	}
	if got := time.Duration(slow.delayNs.Load()); got != loaderStepFrameDelay {
		t.Fatalf("a 200 ms step asked within %v, want the cap %v", got, loaderStepFrameDelay)
	}
}

// TickStep advances the spinner and drops its cached lines without asking the TUI for a frame; Tick asks for one at once.
func TestLoaderTickStepAsksForNoFrame(t *testing.T) {
	ui := &renderCountingTUI{}
	loader := NewLoader(ui, nil, nil, "Working", &LoaderIndicatorOptions{Frames: []string{"a", "b"}})
	before := ui.requests.Load()
	loader.TickStep()
	if loader.Frame != 1 || ui.requests.Load() != before {
		t.Fatalf("TickStep: frame %d, requests %d -> %d; want frame 1 and no request", loader.Frame, before, ui.requests.Load())
	}
	loader.Tick()
	if ui.requests.Load() != before+1 {
		t.Fatalf("Tick requested %d frames, want 1", ui.requests.Load()-before)
	}
}
