package codingagent

import (
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

// markRecorder records the calls the grammar repaint makes on the renderer, and the startup marks, in one order; any other Renderer call panics.
type markRecorder struct {
	tui.TUI
	calls []string
}

func (r *markRecorder) Invalidate()           { r.calls = append(r.calls, "invalidate") }
func (r *markRecorder) RequestRender(...bool) { r.calls = append(r.calls, "requestRender") }

// The task that loads the remaining grammars ends its repaint with the startup mark "highlight-repaint-requested", on the owner loop, after Pi's two
// calls (interactive-mode.ts:1163-1167): the start's last repaint request, which a startup profile and a pty child that must not be ready before it
// wait for. The mark is made whether or not a grammar was loaded: a build that strips syntax highlighting loads none and still requests the repaint, so
// this test runs in that build too (-tags pig_strip_syntax_highlight). The grammar load runs once per process, so the test runs in a process of its own.
func TestLoadRemainingHighlightLanguagesMarksTheRepaintRequest(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	renderer := &markRecorder{}
	opts := InteractiveModeOptions{StartupMark: func(label string) { renderer.calls = append(renderer.calls, "mark "+label) }}
	m := &InteractiveMode{tuiInst: renderer, uiTaskCh: make(chan func(), 1), backgroundCtx: t.Context(), opts: opts}
	m.loadRemainingHighlightLanguages()

	var task func()
	select {
	case task = <-m.uiTaskCh:
	case <-time.After(30 * time.Second):
		t.Fatal("the grammar load posted no repaint to the owner loop")
	}
	if len(renderer.calls) != 0 {
		t.Fatalf("the background task made calls off the owner loop: %v", renderer.calls)
	}
	task()
	if want := []string{"invalidate", "requestRender", "mark highlight-repaint-requested"}; !slices.Equal(renderer.calls, want) {
		t.Fatalf("calls %v, want %v", renderer.calls, want)
	}
	m.backgroundTasks.Wait()
}
