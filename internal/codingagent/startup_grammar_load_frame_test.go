package codingagent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Pi's init() starts loadAllHighlightLanguages() after its first frame, and the import runs on the one event loop: a frame the startup requests meanwhile
// (the notice that follows init(), interactive-mode.ts run()) is a timer, which Node runs only after the import and the continuation that invalidates and
// requests a render, so the two requests are one frame. PiG loads the grammars on a task, so the owner loop can reach the notice's frame before the load
// ends; when the first frame was slow that frame is due at once, runs, and the continuation's request then paints a third time, a bare cursor placement.
// The owner loop therefore leaves a due frame until the load's continuation has run.
//
// startupWritesAroundGrammarLoad returns what the terminal got after the first frame: before the load ended, and after it. A first frame that took longer
// than the frame interval makes the notice's frame due at once.
func startupWritesAroundGrammarLoad(t *testing.T, slowFirstFrame bool) (before, after string) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		ctx, cancel := context.WithCancel(t.Context())
		m.backgroundCtx = ctx
		endLoad := heldLoad(t, nil)

		// Init's order: the first frame, the load, then the frame run() asks for.
		m.tuiInst.RenderNow()
		term.take()
		m.loadRemainingHighlightLanguages()
		if slowFirstFrame {
			time.Sleep(20 * time.Millisecond)
		}
		m.showWarning("No models available. Use /login to log into a provider via OAuth or API key.")
		synctest.Wait()
		m.drainMainLoopOnce()
		before = term.take()

		endLoad()
		synctest.Wait()
		m.drainMainLoopOnce()
		time.Sleep(time.Second)
		synctest.Wait()
		m.drainMainLoopOnce()
		after = term.take()
		cancel()
		m.backgroundTasks.Wait()
		if m.grammarLoadPending.Load() {
			t.Fatal("the frame hold outlived the load")
		}
	})
	return before, after
}

// heldLoad replaces the grammar load for the test with one that waits for the returned function, then runs after if it is not nil. A test that fails
// first still lets the load end.
func heldLoad(t *testing.T, after func()) (end func()) {
	t.Helper()
	release := make(chan struct{})
	end = sync.OnceFunc(func() { close(release) })
	previous := loadHighlightLanguages
	loadHighlightLanguages = func() {
		<-release
		if after != nil {
			after()
		}
	}
	t.Cleanup(func() {
		end()
		loadHighlightLanguages = previous
	})
	return end
}

func TestAFrameDueWhileTheGrammarLoadRunsWaitsForTheLoadsContinuation(t *testing.T) {
	// A first frame fast enough that the notice's frame is not yet due when the load ends: the order Pi's blocked loop gives in every start.
	piBefore, piAfter := startupWritesAroundGrammarLoad(t, false)
	if piBefore != "" || !strings.Contains(piAfter, "No models available") {
		t.Fatalf("the reference start wrote %q before the load ended and %q after it, want nothing and the notice's frame", piBefore, piAfter)
	}
	before, after := startupWritesAroundGrammarLoad(t, true)
	if before != "" {
		t.Fatalf("a frame due while the grammar load ran was painted before the load's continuation: %q", before)
	}
	if after != piAfter {
		t.Fatalf("a slow first frame changed the writes of the start.\nafter the load: %q\nwant (a first frame that was not slow): %q", after, piAfter)
	}
}

// A load that panics ends in the uncaught handler; whatever the handler does, the owner loop must not stay holding the frame it was given.
func TestAFrameHeldForTheGrammarLoadIsPaintedWhenTheLoadPanics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var handled atomic.Value
		t.Cleanup(SetUncaughtGoroutineHandler(func(value any, _ []byte) { handled.Store(value) }))
		m, term := newTickRenderProbe(t, "regular")
		ctx, cancel := context.WithCancel(t.Context())
		m.backgroundCtx = ctx
		endLoad := heldLoad(t, func() { panic("grammar load failed") })
		// Run first of the cleanups: the task is over before the handler is removed, whatever the test found.
		t.Cleanup(func() {
			endLoad()
			cancel()
			m.backgroundTasks.Wait()
		})
		m.tuiInst.RenderNow()
		term.take()
		m.loadRemainingHighlightLanguages()
		time.Sleep(20 * time.Millisecond)
		m.showWarning("No models available.")
		synctest.Wait()
		m.drainMainLoopOnce()
		if got := term.take(); got != "" {
			t.Fatalf("the frame was painted while the load ran: %q", got)
		}

		endLoad()
		synctest.Wait()
		m.drainMainLoopOnce()
		if handled.Load() != "grammar load failed" {
			t.Fatalf("the uncaught handler saw %v, want the loader's panic", handled.Load())
		}
		if got := term.take(); !strings.Contains(got, "No models available") {
			t.Fatalf("a load that panicked left the frame held: %q", got)
		}
		if m.grammarLoadPending.Load() {
			t.Fatal("the frame hold outlived a load that panicked")
		}
	})
}

// A mode that stops while the load runs must not keep a hold that nothing will lift: the continuation cannot be posted, so the task lifts it.
func TestTheFrameHoldEndsWhenTheModeStopsWhileTheGrammarLoadRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newTickRenderProbe(t, "regular")
		ctx, cancel := context.WithCancel(t.Context())
		m.backgroundCtx = ctx
		endLoad := heldLoad(t, nil)
		m.loadRemainingHighlightLanguages()
		synctest.Wait()
		if !m.grammarLoadPending.Load() {
			t.Fatal("a load in progress holds no frame")
		}
		for range cap(m.uiTaskCh) {
			m.postUITask(func() {})
		}
		cancel()
		endLoad()
		m.backgroundTasks.Wait()
		if m.grammarLoadPending.Load() {
			t.Fatal("a mode that stopped during the load left the frame hold set")
		}
	})
}
