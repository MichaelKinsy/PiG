package codingagent

import (
	"context"
	"io"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func newRunOnMainProbe(t *testing.T) *InteractiveMode {
	t.Helper()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.editor = tui.NewEditor()
	m.chatContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.tuiInst.Add(m.chatContainer)
	m.tuiInst.Add(m.editor)
	return m
}

// drainLoop mirrors inputLoop's uiTaskCh case: it runs posted tasks on THIS
// goroutine and exits on ctx cancel. All editor/tree mutation posted via
// runOnMain therefore happens on one goroutine.
func (m *InteractiveMode) drainLoop(ctx context.Context, done chan<- struct{}) {
	for {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-ctx.Done():
			close(done)
			return
		}
	}
}

// TestRunOnMain_DeterministicNoLeak uses testing/synctest to prove,
// deterministically (no probabilistic hammering), that runOnMain (1) delivers
// every posted task reliably (backpressure, no drops), (2) runs them on the
// loop goroutine so editor mutation is single-threaded, and (3) leaks no
// goroutine: synctest.Test fails if any bubble goroutine is still running when
// the root function returns. Run with -race to also assert no data race.
func TestRunOnMain_DeterministicNoLeak(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newRunOnMainProbe(t)
		ctx, cancel := context.WithCancel(context.Background())
		m.runCtx = ctx

		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)

		const turns = 100
		var applied atomic.Int64
		for range turns {
			// Each simulates a turn goroutine posting its end-state reliably.
			go func() {
				m.runOnMain(ctx, func() {
					applied.Add(1)
					m.editor.HandleInput("x") // mutation runs on the loop goroutine
				})
			}()
		}

		// Block until every turn goroutine has posted and the loop has drained
		// all of them (durably blocked on select). Deterministic.
		synctest.Wait()

		if got := applied.Load(); got != turns {
			t.Fatalf("runOnMain delivered %d/%d tasks; it dropped some", got, turns)
		}

		cancel()
		<-loopDone
	})
}

// TestRunOnMain_DropsOnShutdown proves the ctx escape: once the loop is gone
// (ctx cancelled, nothing draining), runOnMain returns instead of blocking
// forever, so a late-finishing worker cannot hang shutdown.
func TestRunOnMain_DropsOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newRunOnMainProbe(t)
		ctx, cancel := context.WithCancel(context.Background())
		m.runCtx = ctx

		// Saturate the queue while the loop is still LIVE, so the next send has
		// no room and the caller becomes durably blocked on the send: the real
		// hazard. Cancelling before dispatch would only prove post-cancel calls
		// return, not that an already-blocked sender is released.
		for range cap(m.uiTaskCh) {
			m.uiTaskCh <- func() {}
		}

		ran := atomic.Bool{}
		returned := make(chan struct{})
		go func() {
			m.runOnMain(ctx, func() { ran.Store(true) })
			close(returned)
		}()

		synctest.Wait() // the sender is now durably blocked on the full queue
		select {
		case <-returned:
			t.Fatal("runOnMain returned before shutdown; the queue was not actually blocking")
		default:
		}

		cancel() // loop gone -> the blocked sender must take the ctx.Done escape
		synctest.Wait()
		select {
		case <-returned:
		default:
			t.Fatal("runOnMain stayed blocked past shutdown instead of taking the ctx.Done escape")
		}
		if ran.Load() {
			t.Fatal("task ran even though it was dropped at shutdown")
		}
	})
}

// TestRunOnMain_NoGoroutineLeakProfile validates the same no-leak property with
// Go's goroutineleak profile. The profile is available by default in Go 1.27.
func TestRunOnMain_NoGoroutineLeakProfile(t *testing.T) {
	prof := pprof.Lookup("goroutineleak")
	if prof == nil {
		t.Fatal("runtime has no goroutineleak profile")
	}

	m := newRunOnMainProbe(t)
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)

	for range 200 {
		m.runOnMain(ctx, func() { m.editor.HandleInput("x") })
	}
	cancel()
	<-loopDone

	// The loop and every posting caller have returned; nothing should remain
	// blocked on uiTaskCh. GC drives the leak detector's reachability pass.
	runtime.GC()
	if n := prof.Count(); n != 0 {
		t.Fatalf("goroutineleak profile reports %d leaked goroutine(s) after the runOnMain workload", n)
	}
}

// TestCreateInteractiveTuiTickBindsOwnerLoopNotAbortCtx exercises the PRODUCTION
// wiring (createInteractiveTui, the seam Run() uses), not the
// autoScrollTickDispatcher helper in isolation, and pins the full binding
// contract deterministically with a saturated queue:
//
//   - The turn-scoped m.abortCtx is already canceled (as after a Ctrl+C abort).
//     With the queue full, a dispatcher wrongly bound to abortCtx would find
//     ctx.Done ready and DROP immediately (return). The correct owner-loop
//     binding finds its context live and stays blocked on the send. Asserting the
//     dispatch is still blocked catches the "bind m.abortCtx" mutation.
//   - The cleanup returned by the factory must cancel that owner-loop context,
//     releasing the blocked tick without running it. Asserting cleanup unblocks it
//     catches a cleanup that never cancels.
func TestCreateInteractiveTuiTickBindsOwnerLoopNotAbortCtx(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newRunOnMainProbe(t)
		m.opts.Settings.TuiMode = "fullscreen"

		abortCtx, abortCancel := context.WithCancel(context.Background())
		abortCancel() // canceled turn context
		m.abortCtx = abortCtx

		ownerCtx, ownerCancel := context.WithCancel(context.Background())
		defer ownerCancel()
		h := m.createInteractiveTui(ownerCtx)

		dispatch := h.tickDispatch
		if dispatch == nil {
			t.Fatal("fullscreen createInteractiveTui installed no tick dispatcher")
		}

		// Saturate the queue so only a canceled context can release the send.
		for range cap(m.uiTaskCh) {
			m.uiTaskCh <- func() {}
		}

		ran := atomic.Bool{}
		returned := make(chan struct{})
		go func() {
			dispatch(func() { ran.Store(true) })
			close(returned)
		}()

		synctest.Wait()
		select {
		case <-returned:
			t.Fatal("tick dispatcher dropped while the owner loop was live: production wiring bound the canceled turn-scoped abortCtx instead of the owner-loop context")
		default:
		}

		m.teardownCurrentTui() // must cancel the owner-loop context and release the blocked tick
		synctest.Wait()
		select {
		case <-returned:
		default:
			t.Fatal("cleanup did not cancel the tick's owner-loop context; the tick stayed blocked past teardown")
		}
		if ran.Load() {
			t.Fatal("tick closure ran even though it was dropped at owner-loop teardown")
		}
	})
}
