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

// Pi's init loads the remaining grammars off the event loop after the startup frame; its continuation, back on the loop, invalidates and requests a render unless the mode has stopped (interactive-mode.ts:init). pig divergence (D618): PiG does so only when a highlight lookup made before the load answers differently now. The grammar load runs once per process, so each test runs in a process of its own.
func TestLoadRemainingHighlightLanguagesRepaintsOnTheOwnerLoop(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	ctx := t.Context()
	renderer := &repaintRecorder{}
	m := &InteractiveMode{tuiInst: renderer, uiTaskCh: make(chan func(), 1), backgroundCtx: ctx}
	// A code block drawn before the load asked for a language only the load registers.
	if tui.SupportsLanguage("ada") {
		t.Fatal("ada is registered before the load; the test needs a language only the load adds")
	}
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

// A start that looked up no language the load adds has nothing to redraw: the load posts no repaint, so no frame follows the first (D618; Pi writes
// a 29-byte cursor-only frame there, see parity scenario 69).
func TestLoadRemainingHighlightLanguagesPostsNoRepaintWhenNoLookupDiffers(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	renderer := &repaintRecorder{}
	m := &InteractiveMode{tuiInst: renderer, uiTaskCh: make(chan func(), 1), backgroundCtx: t.Context()}
	// Looking up an eager language is the same answer before and after the load.
	if !tui.SupportsLanguage("javascript") {
		t.Fatal("javascript is not an eager language")
	}
	m.loadRemainingHighlightLanguages()
	m.backgroundTasks.Wait()
	select {
	case <-m.uiTaskCh:
		t.Fatal("the grammar load posted a repaint although no earlier lookup differs")
	default:
	}
	if !tui.SupportsLanguage("ada") {
		t.Fatal("the load did not register the remaining languages")
	}
	if len(renderer.calls) != 0 {
		t.Fatalf("the load repainted: %v", renderer.calls)
	}
}

func TestLoadRemainingHighlightLanguagesSkipsTheRepaintAfterStop(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	renderer := &repaintRecorder{}
	m := &InteractiveMode{tuiInst: renderer, uiTaskCh: make(chan func()), backgroundCtx: ctx}
	tui.SupportsLanguage("ada") // an earlier lookup the load changes: a running mode would repaint
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

// The first frame and the startup loads leave free heap the runtime's background scavenger returns slowly; the task that registers the remaining grammars returns it once, after queueing the repaint (D172). The test runs in its own process so no other test's collection returns the garbage first.
func TestLoadRemainingHighlightLanguagesReturnsTheStartupHeap(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	for range 64 {
		startupGarbageSink = append(startupGarbageSink, make([]byte, 1<<20))
	}
	startupGarbageSink = nil
	before := heapReleasedBytes()
	m := &InteractiveMode{tuiInst: &repaintRecorder{}, uiTaskCh: make(chan func(), 1), backgroundCtx: t.Context()}
	m.loadRemainingHighlightLanguages()
	m.backgroundTasks.Wait()
	if released := heapReleasedBytes() - before; released < 40<<20 {
		t.Fatalf("the grammar-load task returned %d bytes of the 64 MiB the start left free, want at least 40 MiB", released)
	}
}
