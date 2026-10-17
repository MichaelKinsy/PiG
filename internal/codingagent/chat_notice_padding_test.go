package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// directRenderTUI counts the renders the mode asks for, synchronous or requested.
type directRenderTUI struct {
	tui.TUI
	renders int
}

func (r *directRenderTUI) Render()               { r.renders++ }
func (r *directRenderTUI) RequestRender(...bool) { r.renders++ }

// interactive-mode.ts:4618-4633 showError / showWarning: an error is `Error: <msg>` in the error color padded by the output pad (Pi 1.1.0 outputPad), a warning is `Warning: <msg>` padded by
// one column, and each asks for a render (ui.requestRender()); the existing color test never looks at the padding. clearEditor empties the editor and asks for a render. PiG draws
// showWarning and clearEditor with a direct Render and showError with RequestRender; the test counts either, so it pins that a render follows, not which kind.
func TestChatNoticesCarryTheirPaddingAndTheEditorClearRerenders(t *testing.T) {
	restoreStartupTheme(t)
	render := func(m *InteractiveMode) string {
		return widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	}
	newMode := func(outputPad int) (*InteractiveMode, *directRenderTUI) {
		ui := &directRenderTUI{TUI: tui.NewWithOutput(io.Discard, 100, 30)}
		return &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: ui, outputPad: outputPad}, ui
	}

	for _, pad := range []int{0, 3} {
		m, _ := newMode(pad)
		m.showError("boom")
		var line string
		for candidate := range strings.SplitSeq(render(m), "\n") {
			if strings.Contains(candidate, "Error: boom") {
				line = candidate
			}
		}
		if want := strings.Repeat(" ", pad) + "Error: boom"; !strings.HasPrefix(line, want) || strings.HasPrefix(line, want+"x") || strings.HasPrefix(line, " "+want) {
			t.Errorf("outputPad %d: error line %q, want it to start with %q", pad, line, want)
		}
	}

	m, ui := newMode(3)
	m.showWarning("careful")
	var warning string
	for candidate := range strings.SplitSeq(render(m), "\n") {
		if strings.Contains(candidate, "Warning: careful") {
			warning = candidate
		}
	}
	if !strings.HasPrefix(warning, " Warning: careful") || strings.HasPrefix(warning, "  ") {
		t.Errorf("warning line %q, want exactly one column of padding", warning)
	}
	if ui.renders != 1 {
		t.Errorf("showWarning asked for %d renders, want one", ui.renders)
	}

	editor := tui.NewEditor()
	editor.SetText("draft")
	m2, ui2 := newMode(0)
	m2.editor = editor
	m2.ClearEditor()
	if editor.Text() != "" || ui2.renders != 1 {
		t.Errorf("ClearEditor left %q and asked for %d renders, want an empty editor and one render", editor.Text(), ui2.renders)
	}
}
