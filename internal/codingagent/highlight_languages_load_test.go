//go:build !pig_strip_syntax_highlight

package codingagent

import (
	"context"
	"runtime/metrics"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

// repaintRecorder records the repaint calls a deferred grammar load makes; any other Renderer call panics.
type repaintRecorder struct {
	tui.TUI
	calls []string
}

func (r *repaintRecorder) Invalidate()           { r.calls = append(r.calls, "invalidate") }
func (r *repaintRecorder) RequestRender(...bool) { r.calls = append(r.calls, "requestRender") }

// Pi's init loads the remaining grammars off the event loop after the startup frame; its continuation, back on the loop, invalidates and requests a render unless the mode has stopped (interactive-mode.ts:init).
func TestLoadRemainingHighlightLanguagesRepaintsOnTheOwnerLoop(t *testing.T) {
	ctx := t.Context()
	renderer := &repaintRecorder{}
	m := &InteractiveMode{tuiInst: renderer, uiTaskCh: make(chan func(), 1), backgroundCtx: ctx}
	m.loadRemainingHighlightLanguages()

	var task func()
	select {
	case task = <-m.uiTaskCh:
	case <-time.After(30 * time.Second):
		t.Fatal("the grammar load posted no repaint to the owner loop")
	}
	if len(renderer.calls) != 0 {
		t.Fatalf("the background task repainted off the owner loop: %v", renderer.calls)
	}
	if !tui.SupportsLanguage("ada") {
		t.Fatal("the repaint was posted before every grammar loaded")
	}
	task()
	if got := renderer.calls; len(got) != 2 || got[0] != "invalidate" || got[1] != "requestRender" {
		t.Fatalf("repaint calls %v, want invalidate then requestRender", got)
	}
	m.backgroundTasks.Wait()
}

func TestLoadRemainingHighlightLanguagesSkipsTheRepaintAfterStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	renderer := &repaintRecorder{}
	m := &InteractiveMode{tuiInst: renderer, uiTaskCh: make(chan func()), backgroundCtx: ctx}
	m.loadRemainingHighlightLanguages()
	m.backgroundTasks.Wait()
	select {
	case <-m.uiTaskCh:
		t.Fatal("a stopped mode received a repaint")
	default:
	}
	if len(renderer.calls) != 0 {
		t.Fatalf("a stopped mode repainted: %v", renderer.calls)
	}
}

func heapReleasedBytes() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/released:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

var startupGarbageSink [][]byte

// The first frame and the startup loads leave free heap the runtime's background scavenger returns slowly. The grammar-load task does not collect it
// right after the first frame (a forced collection there took 23 ms of the core the first prompt needs): it arms the idle collection, which returns
// the heap once the session has stayed idle (D172). The test runs in its own process so no other test's collection returns the garbage first.
func TestStartupHeapIsReturnedByTheFirstIdleCollectionNotByTheGrammarLoad(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	for range 64 {
		startupGarbageSink = append(startupGarbageSink, make([]byte, 1<<20))
	}
	startupGarbageSink = nil
	m, ctx, _ := newLifecycleMode(t)
	m.installRenderDispatcher()
	m.isIdle = true
	m.backgroundCtx = ctx
	spinCtx, cancel := context.WithCancel(ctx)
	spinnerDone := make(chan struct{})
	go func() { defer close(spinnerDone); m.tickSpinner(spinCtx) }()
	defer func() { cancel(); <-spinnerDone }()

	before := heapReleasedBytes()
	m.loadRemainingHighlightLanguages()
	m.backgroundTasks.Wait()
	// The released-bytes counter also falls when the runtime reuses released pages, so the difference is signed.
	if released := int64(heapReleasedBytes()) - int64(before); released >= 20<<20 {
		t.Fatalf("the grammar-load task returned %d bytes of the 64 MiB the start left free before the first idle point, want less than 20 MiB", released)
	}
	if got := m.idleCollector.collects.Load(); got != 0 {
		t.Fatalf("idle collections right after the grammar load = %d, want 0 before the idle wait", got)
	}

	// Drive the owner loop as the input loop does until the idle collection has run.
	start := time.Now()
	for m.idleCollector.collects.Load() == 0 {
		if time.Since(start) > 5*time.Second {
			t.Fatal("the idle collection never ran after the grammar load")
		}
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if after := time.Since(start); after < idleCollectEarliest {
		t.Fatalf("the idle collection ran %v after the grammar load, want at least %v of idle first", after, idleCollectEarliest)
	}
	if released := int64(heapReleasedBytes()) - int64(before); released < 40<<20 {
		t.Fatalf("the first idle collection returned %d bytes of the 64 MiB the start left free, want at least 40 MiB", released)
	}
}
