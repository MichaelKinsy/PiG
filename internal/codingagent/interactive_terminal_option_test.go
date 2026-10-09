package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// terminalMarker is an injected terminal that behaves as the process terminal; its identity is what the tests compare.
type terminalMarker struct{ tui.Terminal }

func newTerminalMarker() *terminalMarker {
	return &terminalMarker{Terminal: tui.NewStdioProcessTerminal()}
}

// interactive-mode.ts:447,626: InteractiveModeOptions.terminal is the terminal createInteractiveTui builds the renderer on, in both TUI modes; tui.ts:532 and tui-alt-screen.ts:258 keep it as the renderer's terminal.
func TestInteractiveTuiRendererDrivesTheInjectedTerminal(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		injected := newTerminalMarker()
		ui := CreateInteractiveTui(InteractiveTuiOptions{TuiMode: mode, Terminal: injected})
		if ui.Terminal() != tui.Terminal(injected) {
			t.Fatalf("%s: renderer terminal = %v, want the injected terminal", mode, ui.Terminal())
		}
		if def := CreateInteractiveTui(InteractiveTuiOptions{TuiMode: mode}); def.Terminal() == tui.Terminal(injected) {
			t.Fatalf("%s: the default renderer must use the process terminal", mode)
		}
	}
}

// The mode passes its option through its own renderer construction (interactive-mode.ts:626), regular and fullscreen alike.
func TestInteractiveModeBuildsItsRendererOnTheTerminalOption(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		injected := newTerminalMarker()
		m := &InteractiveMode{opts: InteractiveModeOptions{TuiMode: mode, Terminal: injected}}
		handle := m.createInteractiveTui(t.Context())
		t.Cleanup(handle.cleanup)
		if got := m.tuiInst.Terminal(); got != tui.Terminal(injected) {
			t.Fatalf("%s: mode renderer terminal = %v, want the injected terminal", mode, got)
		}
	}
}
