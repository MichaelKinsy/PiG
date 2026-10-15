package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func renderArminAt(a *ArminComponent, width int) []string { return (*ArminComponent).Render(a, width) }

// armin.ts:90-117 render(width): every grid row is " " + the row clipped to width-1 columns in the accent color + padding up to the width, and the last line is the label (" ARMIN SAYS HI" in Pi, arminLabel here)
// padded the same way (never clipped); the result is cached for a width until invalidate() clears the cached width (:84-86), so a theme change shows only after it.
func TestArminRenderClipsRowsToTheWidthPadsThemAndCachesUntilInvalidated(t *testing.T) {
	restoreStartupTheme(t)
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetThemeByName("dark")
	a := newArminComponent(arminTestRandom(1))
	a.currentGrid = a.finalGrid
	for _, width := range []int{1, 5, 20, arminWidth + 10} {
		lines := renderArminAt(a, width)
		if len(lines) != arminDisplayHeight+1 {
			t.Fatalf("width %d: %d lines, want %d rows and the label", width, len(lines), arminDisplayHeight+1)
		}
		for i, line := range lines[:arminDisplayHeight] {
			plain := widthx.StripAnsi(line)
			if got := len([]rune(plain)); got != width {
				t.Errorf("width %d row %d is %d columns wide: %q", width, i, got, plain)
			}
			wantClip := min(width-1, arminWidth)
			if want := " " + string(a.finalGrid[i][:wantClip]) + strings.Repeat(" ", width-1-wantClip); plain != want {
				t.Errorf("width %d row %d = %q, want %q", width, i, plain, want)
			}
		}
		label := widthx.StripAnsi(lines[arminDisplayHeight])
		if want := " " + arminLabel + strings.Repeat(" ", max(0, width-1-len(arminLabel))); label != want {
			t.Errorf("width %d label = %q, want %q", width, label, want)
		}
	}

	// At width 0 Pi's row.slice(0, width - 1) is slice(0, -1): every column but the last, with no padding (armin.ts:96-99).
	lines := renderArminAt(a, 0)
	for i, line := range lines[:arminDisplayHeight] {
		if got, want := widthx.StripAnsi(line), " "+string(a.finalGrid[i][:arminWidth-1]); got != want {
			t.Errorf("width 0 row %d = %q, want %q", i, got, want)
		}
	}
	if got := widthx.StripAnsi(lines[arminDisplayHeight]); got != " "+arminLabel {
		t.Errorf("width 0 label = %q", got)
	}

	dark := renderArminAt(a, 30)[0]
	tui.SetThemeByName("light")
	if got := renderArminAt(a, 30)[0]; got != dark {
		t.Error("a second render at the same width recomputed instead of using the cache")
	}
	a.Invalidate()
	if got := renderArminAt(a, 30)[0]; got == dark {
		t.Error("Invalidate did not clear the cached width, so the theme change never showed")
	}
}
