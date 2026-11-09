package codingagent

import (
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// requestCountingTUI signals render requests once counting is on: the selector's own render is a direct Render call, so the
// requests of a picker that stays open without input are the animation's.
type requestCountingTUI struct {
	tui.TUI
	counting  atomic.Bool
	requested chan struct{}
}

func (u *requestCountingTUI) RequestRender(force ...bool) {
	if u.counting.Load() {
		select {
		case u.requested <- struct{}{}:
		default:
		}
	}
	u.TUI.RequestRender(force...)
}

func brandAnimationGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	count := 0
	for block := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(block, "tui.StartBrandAnimation.func1") {
			count++
		}
	}
	return count
}

// interactive-mode.ts passes this.ui to the OAuthSelectorComponent of /login and /logout, so a selected partner row
// animates and dispose ends the animation when the selector closes: SelectAuthProvider hands the picker the TUI's render
// request, and disposes it when the selection ends.
func TestSelectAuthProviderAnimatesAPartnerRowAndDisposesTheAnimation(t *testing.T) {
	providers := []tui.OAuthProvider{
		{ID: "radius", Name: "Radius", AuthType: "oauth"},
		{ID: "anthropic", Name: "Anthropic", AuthType: "api_key"},
	}
	input := make(chan []byte)
	m := &InteractiveMode{runCtx: t.Context(), editor: tui.NewEditor(), editorContainer: tui.NewContainer(), layout: tui.NewContainer(), modalInputCh: input}
	ui := &requestCountingTUI{TUI: tui.NewWithOutput(io.Discard, 120, 40), requested: make(chan struct{}, 1)}
	m.tuiInst = ui
	m.tuiInst.AddChild(m.editorContainer) // the selector animates once the TUI renders it
	m.tuiInst.SetFocus(m.editor)
	ui.counting.Store(true)
	before := brandAnimationGoroutines()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.buildSlashContext(t.Context()).SelectAuthProvider("login", providers, "")
	}()
	// No input is sent: only the animation can request a render.
	select {
	case <-ui.requested:
	case <-time.After(30 * time.Second):
		t.Fatal("the animation requested no render while the Radius row was selected")
	}
	if got := brandAnimationGoroutines(); got != before+1 {
		t.Errorf("%d animation goroutines while the picker is open, want %d", got, before+1)
	}
	input <- []byte("\x1b")
	<-done
	if got := brandAnimationGoroutines(); got != before {
		t.Errorf("%d animation goroutines after the picker closed, want %d", got, before)
	}
}
