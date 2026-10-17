package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts:1000-1006 and pi-logo.ts:32-34 (Pi 1.0.0): Apple Terminal (darwin with TERM_PROGRAM=Apple_Terminal) cannot render the half-block logo, so the header shows the "Pi" wordmark with the version on the first line and the key hints on the second line. Upstream has no test for it; the case asserts the layout the source defines on the platform where the source applies it.
// pig divergence (D2): the wordmark is the one-line "PiG." mark, not "Pi".
func TestBuiltInHeaderShowsTheWordmarkInAppleTerminalUpstream(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	for _, expanded := range []bool{false, true} {
		m := &InteractiveMode{
			opts:        InteractiveModeOptions{LoginVisible: true},
			keybindings: km,
			extHeader:   newSpecialLinesComponent(nil),
			tuiInst:     tui.NewWithOutput(io.Discard, 100, 40),
		}
		m.restoreBuiltInHeader()
		m.setAllToolsExpanded(expanded)
		lines := strings.Split(stripANSITest(strings.Join(m.extHeader.Render(100), "\n")), "\n")
		if len(lines) < 3 {
			t.Fatalf("header = %q", lines)
		}
		if got, want := strings.TrimRight(lines[0], " "), " PiG. v"+pigversion.Version; got != want {
			t.Errorf("expanded=%v first line = %q, want %q", expanded, got, want)
		}
		wantHint := " escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more"
		if expanded {
			wantHint = " escape to interrupt"
		}
		if got := strings.TrimRight(lines[1], " "); got != wantHint {
			t.Errorf("expanded=%v second line = %q, want %q", expanded, got, wantHint)
		}
	}
}
