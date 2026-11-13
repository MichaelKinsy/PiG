package subprocess

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// droppedView is the view of a renderer's component that the transcript drew and then dropped.
type droppedView struct {
	view rendererView
	// dropped is set once the transcript has dropped the component; repaints counts the repaints its loader asked for since.
	dropped  atomic.Bool
	repaints atomic.Int64
}

// loaderTimers is the count of the view's armed loader timers.
func (d *droppedView) loaderTimers() int {
	d.view.mu.Lock()
	surface := d.view.surface
	d.view.mu.Unlock()
	if surface == nil {
		return 0
	}
	surface.mu.Lock()
	defer surface.mu.Unlock()
	return surface.loaderTimers
}

// TestViewLoadersStopWhenNothingDrawsTheirSurface: a renderer's view is shown by a transcript component, and a transcript that is rebuilt
// (after a compaction, a /tree, a /reload) drops its components without a call. The host animates a view's loaders with a timer each, so
// the loaders of a dropped view must stop by themselves: fifty rebuilds while the connection lives leave no timer armed, where each left
// one that asked for a repaint for ever and kept the view. A view that is drawn again animates again.
func TestViewLoadersStopWhenNothingDrawsTheirSurface(t *testing.T) {
	const rebuilds = 50
	result := RenderResult{View: json.RawMessage(`{"root":{"kind":"loader","message":"wait","indicator":{"frames":["<a>","<b>"],"intervalMs":1}}}`)}
	views := make([]*droppedView, rebuilds)
	for i := range views {
		d := &droppedView{}
		views[i] = d
		lines, ok := d.view.apply(nil, func() {
			if d.dropped.Load() {
				d.repaints.Add(1)
			}
		}, "", nil, result, 40)
		if !ok || !strings.Contains(strings.Join(lines, "\n"), "wait") {
			t.Fatalf("rebuild %d: the view drew %q (accepted %t), want its loader", i, lines, ok)
		}
		// The transcript is rebuilt: nothing draws this component again.
		d.dropped.Store(true)
	}
	// armed waits until no loader timer of the views is armed. A timer that fires after the last draw sees that draw, ticks once and arms
	// the last timer, which finds nothing drawn: a second repaint is a loader that runs on with nothing to show it.
	armed := func(when string) {
		t.Helper()
		deadline := time.Now().Add(testbudget.Wait(t))
		for {
			timers, running := 0, 0
			for _, d := range views {
				timers += d.loaderTimers()
				if d.repaints.Load() > 1 {
					running++
				}
			}
			if running > 0 {
				t.Fatalf("%s: the loaders of %d of %d dropped views run on, and %d timers are armed, want none", when, running, rebuilds, timers)
			}
			if timers == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d loader timers are still armed (wait budget %s)", when, timers, testbudget.Env)
			}
			time.Sleep(time.Millisecond)
		}
	}
	armed("after the transcript was rebuilt")

	// The component is on screen again: its loader runs for as long as it is drawn.
	again := views[0]
	again.repaints.Store(0)
	deadline := time.Now().Add(testbudget.Wait(t))
	for again.repaints.Load() < 3 {
		if _, ok := again.view.live(40); !ok {
			t.Fatal("the view no longer draws")
		}
		if time.Now().After(deadline) {
			t.Fatalf("a view that is drawn again got %d repaints of its loader, want it to run (wait budget %s)", again.repaints.Load(), testbudget.Env)
		}
		time.Sleep(time.Millisecond)
	}
	again.repaints.Store(0)
	armed("after the view was drawn again and dropped")
}
