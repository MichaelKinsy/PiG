package tui

import (
	"strings"
	"testing"
)

// Pi's overflow check ignores image rows, and D53's full clear exists for wrapped text rows. An image row whose raw escape
// sequence is wider than the terminal must not turn a later in-place edit into a clearing redraw (found by the
// main-screen differential: Pi diff-renders, PiG cleared the screen).
func TestMainScreenEditBelowImageRowDoesNotClear(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
	t.Cleanup(ResetCapabilitiesCache)
	// An overlay row cut by the compositor leaves an unterminated image sequence whose text counts as visible width.
	cutImage := "\x1b]1337;File=inline=1;" + strings.Repeat("A", 30) + "\x1b[0m"
	lines := &oracleLines{lines: []string{"first", cutImage, "last"}}
	var out strings.Builder
	ui := NewWithOutput(&out, 20, 5)
	ui.Add(lines)
	ui.Render()
	out.Reset()
	lines.lines = []string{"first", "\x1b]1337;File=inline=1;AAAA\x07", "next"}
	ui.Render()
	if got := out.String(); strings.Contains(got, "\x1b[2J") || !strings.Contains(got, "next") {
		t.Fatalf("edit below an image row repainted the screen: %q", got)
	}
}
