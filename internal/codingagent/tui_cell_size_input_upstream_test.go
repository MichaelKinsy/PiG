package codingagent

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's TuiMainScreen owns terminal input; PiG's InteractiveMode owns that same
// dispatch path. These cases route through it, not just the response parser.
func TestUpstreamTUICellSizeInput(t *testing.T) {
	newMode := func(t *testing.T, initial *tui.CellDimensions) (*InteractiveMode, *focusedInputProbe) {
		t.Helper()
		capabilities, dimensions := tui.GetCapabilities(), tui.GetCellDimensions()
		t.Cleanup(func() { tui.SetCapabilities(capabilities); tui.SetCellDimensions(dimensions) })
		t.Setenv("TERM_PROGRAM", "ghostty")
		// Keep the fixture on Ghostty itself, independent of the test runner's multiplexer, Herdr session (D44 detects Herdr before Ghostty) or PI_IMAGE_PROTOCOL override (terminal-image.ts detectCapabilities lets it replace detection).
		for _, name := range []string{"TERM", "GHOSTTY_RESOURCES_DIR", "TMUX", "HERDR_ENV", "HERDR_KITTY_GRAPHICS", "PI_IMAGE_PROTOCOL"} {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
		tui.ResetCapabilitiesCache()
		if initial != nil {
			tui.SetCellDimensions(*initial)
		}
		// Mount through the production startup owner, not a direct query call that could conceal missing startup wiring.
		mode, terminal := newTickRenderProbe(t, "regular")
		t.Cleanup(mode.tuiInst.Stop)
		probe := &focusedInputProbe{}
		mode.tuiInst.SetFocus(probe)
		if !strings.Contains(terminal.take(), "\x1b[16t") {
			t.Fatal("image terminal did not receive startup query")
		}
		return mode, probe
	}
	// .upstream/v0.87.1/packages/tui/test/tui-cell-size-input.test.ts:46
	t.Run("forwards bare escape even when a cell size query was sent at startup", func(t *testing.T) {
		mode, probe := newMode(t, nil)
		if err := mode.dispatchKey(t.Context(), "\x1b"); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(probe.inputs, []string{"\x1b"}) {
			t.Fatalf("focused component inputs = %q, want Escape", probe.inputs)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/tui-cell-size-input.test.ts:62
	t.Run("consumes cell size responses and still forwards later user input", func(t *testing.T) {
		mode, probe := newMode(t, &tui.CellDimensions{WidthPx: 9, HeightPx: 18})
		if err := mode.dispatchKey(t.Context(), "\x1b[6;20;10t"); err != nil {
			t.Fatal(err)
		}
		if len(probe.inputs) != 0 {
			t.Fatalf("cell response reached focused component: %q", probe.inputs)
		}
		if got, want := tui.GetCellDimensions(), (tui.CellDimensions{WidthPx: 10, HeightPx: 20}); got != want {
			t.Fatalf("dimensions = %#v, want %#v", got, want)
		}
		if err := mode.dispatchKey(t.Context(), "q"); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(probe.inputs, []string{"q"}) {
			t.Fatalf("focused component inputs = %q, want q", probe.inputs)
		}
	})
}
