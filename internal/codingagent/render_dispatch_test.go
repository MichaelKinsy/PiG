package codingagent

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// drainMainLoopOnce runs, like inputLoop, every posted UI task and scheduled
// render that is ready, then returns.
func (m *InteractiveMode) drainMainLoopOnce() {
	for {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		default:
			return
		}
	}
}

// A throttled render requested while the UI task queue is saturated (the main
// loop stalled under load while spinner ticks and worker posts piled up) must
// still paint once the loop catches up. Upstream's requestRender timer callback
// always runs; pig used to post the render into the bounded task queue with a
// non-blocking send and silently drop it, leaving the screen stale until the
// next keystroke.
func TestScheduledRenderSurvivesSaturatedUITaskQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
		m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
		var out bytes.Buffer
		m.chatContainer = tui.NewContainer()
		m.tuiInst = tui.NewWithOutput(&out, 80, 24)
		m.tuiInst.Add(m.chatContainer)
		m.installRenderDispatcher()
		m.tuiInst.Render()

		for range cap(m.uiTaskCh) {
			m.postUITask(func() {})
		}
		m.chatContainer.Add(tui.NewText("SESSION FINISHED"))
		m.tuiInst.RequestRender()
		time.Sleep(time.Second) // past the 16ms frame throttle
		synctest.Wait()         // the throttle timer fired and handed the render over

		m.drainMainLoopOnce()
		if !strings.Contains(out.String(), "SESSION FINISHED") {
			t.Fatal("the scheduled render was dropped; the screen never showed the new content")
		}
	})
}

// Several scheduled renders dispatched before the loop wakes coalesce into
// one paint of the latest state.
func TestScheduledRendersCoalesceWhileLoopIsBusy(t *testing.T) {
	m := &InteractiveMode{renderWakeCh: make(chan struct{}, 1)}
	var ran []int
	for i := range 3 {
		m.dispatchScheduledRender(func() { ran = append(ran, i) })
	}
	for {
		select {
		case <-m.renderWakeCh:
			m.runScheduledRender()
			continue
		default:
		}
		break
	}
	if len(ran) != 1 || ran[0] != 2 {
		t.Fatalf("ran renders %v, want only the latest", ran)
	}
}

// A stalled main loop sees at most one queued spinner tick, like a Node
// setInterval whose callbacks do not pile up behind a blocked event loop, and
// ticking resumes once that tick has run.
func TestSpinnerTicksCoalesceWhileLoopIsStalled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := statusBorderMode(t, true)
		m.startWorkingLoader()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); m.tickSpinner(ctx) }()

		time.Sleep(time.Second) // about 12 spinner intervals with no loop draining
		synctest.Wait()
		if got := len(m.uiTaskCh); got != 1 {
			t.Fatalf("stalled loop has %d queued spinner ticks, want 1", got)
		}

		(<-m.uiTaskCh)()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if got := len(m.uiTaskCh); got != 1 {
			t.Fatalf("after the queued tick ran, %d ticks are queued, want 1", got)
		}
		cancel()
		<-done
	})
}
