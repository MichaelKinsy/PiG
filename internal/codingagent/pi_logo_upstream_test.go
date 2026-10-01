package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/components/pi-logo.ts (piLogoLines) and the header layout of interactive-mode.ts:971-1023 (upstream 0.99.1). Upstream has no test file for either; these cases assert the sequences the source defines.

func TestPiLogoLinesUpstream(t *testing.T) {
	const reset = "\x1b[0m"
	t.Run("truecolor", func(t *testing.T) {
		top, bottom := piLogoLines(tui.TerminalColorModeTrueColor)
		wantTop := "\x1b[38;2;228;138;122m\x1b[48;2;79;142;179m▀" + reset + "\x1b[38;2;228;138;122m▀█" + reset + " "
		wantBottom := "\x1b[38;2;79;142;179m█▀" + reset + " \x1b[38;2;234;182;93m█" + reset
		if top != wantTop || bottom != wantBottom {
			t.Errorf("lines = %q, %q\nwant    %q, %q", top, bottom, wantTop, wantBottom)
		}
	})
	t.Run("the brand colors follow the terminal's color mode", func(t *testing.T) {
		top, bottom := piLogoLines(tui.TerminalColorMode256)
		coral, blue, yellow := rgbColorForTest(t, 228, 138, 122), rgbColorForTest(t, 79, 142, 179), rgbColorForTest(t, 234, 182, 93)
		wantTop := tui.ForegroundAnsi(coral, tui.TerminalColorMode256) + tui.BackgroundAnsi(blue, tui.TerminalColorMode256) + "▀" + reset + tui.ForegroundAnsi(coral, tui.TerminalColorMode256) + "▀█" + reset + " "
		wantBottom := tui.ForegroundAnsi(blue, tui.TerminalColorMode256) + "█▀" + reset + " " + tui.ForegroundAnsi(yellow, tui.TerminalColorMode256) + "█" + reset
		if top != wantTop || bottom != wantBottom {
			t.Errorf("lines = %q, %q\nwant    %q, %q", top, bottom, wantTop, wantBottom)
		}
		if strings.Contains(top+bottom, "38;2;") {
			t.Errorf("256-color logo contains a truecolor sequence: %q %q", top, bottom)
		}
	})
}

func rgbColorForTest(t *testing.T, r, g, b float64) tui.Color {
	t.Helper()
	color, err := tui.NewRgbColor(r, g, b)
	if err != nil {
		t.Fatal(err)
	}
	return color
}

// interactive-mode.ts:977-1023: the logo's first line carries the version, its second line the first line of key hints, and the product name no longer appears.
func TestBuiltInHeaderShowsThePiLogoUpstream(t *testing.T) {
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	for _, expanded := range []bool{false, true} {
		m := &InteractiveMode{
			opts:        InteractiveOptions{LoginVisible: true},
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
		if got, want := strings.TrimRight(lines[0], " "), " ▀▀█  v"+pigversion.Version; got != want {
			t.Errorf("expanded=%v first line = %q, want %q", expanded, got, want)
		}
		wantHint := " █▀ █ escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more"
		if expanded {
			wantHint = " █▀ █ escape to interrupt"
		}
		if got := strings.TrimRight(lines[1], " "); got != wantHint {
			t.Errorf("expanded=%v second line = %q, want %q", expanded, got, wantHint)
		}
		if strings.Contains(strings.Join(lines, "\n"), "pig v") {
			t.Errorf("expanded=%v: the header still names the product before the version: %q", expanded, lines)
		}
	}
}
