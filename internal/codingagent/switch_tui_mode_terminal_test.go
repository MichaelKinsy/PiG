package codingagent

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// knownTerminalRenderer is a renderer whose Terminal is a known value.
type knownTerminalRenderer struct {
	*tui.TuiMainScreen
	terminal tui.Terminal
}

func (r *knownTerminalRenderer) Terminal() tui.Terminal { return r.terminal }

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:897,910-914 switchTuiMode reads `previousUi.terminal` and builds the next renderer over it, so a session keeps one terminal (and one PI_TUI_WRITE_LOG file) across renderer switches.
func TestSwitchTuiModeKeepsThePreviousRenderersTerminal(t *testing.T) {
	m := newSwitchTuiProbe(t)
	var out bytes.Buffer
	terminal := tui.NewProcessTerminalWithOutput(nil, nil, &out)
	m.tuiInst = &knownTerminalRenderer{TuiMainScreen: m.tuiInst.(*tui.TuiMainScreen), terminal: terminal}
	m.rendererOut = nil
	if !m.switchTuiMode("fullscreen", false, false) || m.altScreen == nil {
		t.Fatal("probe did not switch to fullscreen")
	}
	if m.tuiInst.Terminal() != tui.Terminal(terminal) {
		t.Fatalf("the fullscreen renderer draws on %v, want the previous renderer's terminal", m.tuiInst.Terminal())
	}
	if !m.switchTuiMode("regular", false, false) {
		t.Fatal("probe did not switch back to regular")
	}
	if m.tuiInst.Terminal() != tui.Terminal(terminal) {
		t.Fatalf("the regular renderer draws on %v, want the session's terminal", m.tuiInst.Terminal())
	}
}
