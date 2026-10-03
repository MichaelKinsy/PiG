package codingagent

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// repaintRecorder records the repaint calls a deferred grammar load makes; any other Renderer call panics.
type repaintRecorder struct {
	tui.Renderer
	calls []string
}

func (r *repaintRecorder) Invalidate()    { r.calls = append(r.calls, "invalidate") }
func (r *repaintRecorder) RequestRender() { r.calls = append(r.calls, "requestRender") }

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
