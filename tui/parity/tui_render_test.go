// Live parity tests for the TUI render loop. These run the actual
// tui.TUI against an in-memory writer + termsim grid and assert
// the resulting visible state matches the upstream pi-tui algorithm
// reproduced in-test.
//
// These tests serve two purposes:
//  1. Lock the byte-level behavior so future refactors can't silently drift.
//  2. Document the upstream algorithm's observable shape as Go reference
//     code, since the TypeScript source isn't grep-able from tests.
//
// added 2026-05-11.
package parity

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/termsim"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// staticLines is a Component that returns a fixed []string per render.
type staticLines struct{ lines []string }

func (s *staticLines) Render(width int) []string { return s.lines }
func (s *staticLines) Invalidate()               {}

// captureTUIRender feeds the given lines into a real *tui.TUI rendering to
// an in-memory writer, and returns the resulting bytes and the simulated
// terminal grid.
func captureTUIRender(t *testing.T, rows, cols int, lines []string) (*termsim.Grid, []byte) {
	t.Helper()
	var buf bytes.Buffer
	ti := tui.NewWithOutput(&buf, cols, rows)
	ti.Add(&staticLines{lines: lines})
	ti.Render()
	g := termsim.New(rows, cols)
	g.Write(buf.Bytes())
	return g, buf.Bytes()
}

// upstreamRender reproduces upstream pi-tui's first-render byte stream for a
// known set of lines. Source: tui.ts::doRender → fullRender(false) path:
//
//	let buffer = "\x1b[?2026h";
//	for (let i = 0; i < newLines.length; i++) {
//	    if (i > 0) buffer += "\r\n";
//	    buffer += newLines[i];
//	}
//	buffer += "\x1b[?2026l";
//
// applyLineResets has already been called on newLines before this point,
// appending SEGMENT_RESET to every non-image line.
func upstreamFirstRender(lines []string) []byte {
	resetLines := widthx.ApplyLineResets(lines)
	var buf bytes.Buffer
	buf.WriteString("\x1b[?2026h")
	for i, line := range resetLines {
		if i > 0 {
			buf.WriteString("\r\n")
		}
		buf.WriteString(line)
	}
	buf.WriteString("\x1b[?2026l")
	return buf.Bytes()
}

// TestParityTUI_FirstRender_VisibleState asserts the visible state of a
// first render matches between pig's TUI and upstream's algorithm.
// We accept pig emitting extra ANSI (e.g. \x1b[2K erase-line per row) as
// long as the resulting visible state in the simulated terminal is
// identical: that is the byte-parity floor.
func TestParityTUI_FirstRender_VisibleState(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
	}{
		{"single line", []string{"hello"}},
		{"two plain lines", []string{"first", "second"}},
		{"styled", []string{"\x1b[1;31mred bold\x1b[0m", "plain"}},
		{"with hyperlink", []string{"\x1b]8;;https://e.com\x07link\x1b]8;;\x07 tail"}},
		{"wide cjk", []string{"a世b 你好"}},
		{"empty middle line", []string{"top", "", "bottom"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotGrid, _ := captureTUIRender(t, 5, 30, tc.lines)
			wantGrid := termsim.New(5, 30)
			wantGrid.Write(upstreamFirstRender(tc.lines))
			if gotGrid.String() != wantGrid.String() {
				t.Errorf("visible state mismatch.\n--- pig ---\n%s\n--- upstream ---\n%s",
					withRowMarkers(gotGrid.String()), withRowMarkers(wantGrid.String()))
			}
		})
	}
}

// TestParityTUI_FirstRender_LineResetsApplied asserts that the rendered
// byte stream contains the SEGMENT_RESET barrier for every non-image
// line in the output: proving applyLineResets is wired in. This is a
// regression test for the τ.1 fix.
func TestParityTUI_FirstRender_LineResetsApplied(t *testing.T) {
	lines := []string{"alpha", "beta", "gamma"}
	_, raw := captureTUIRender(t, 5, 20, lines)
	got := string(raw)
	// Each non-image line should be followed by SEGMENT_RESET before
	// the next "\r\n" or the final mode-2026 close.
	occurrences := strings.Count(got, widthx.SegmentReset)
	if occurrences < len(lines) {
		t.Errorf("SegmentReset appears %d times in output, want >= %d.\nraw bytes:\n%q",
			occurrences, len(lines), got)
	}
}

// TestParityTUI_FirstRender_SyncOutputBracketed asserts mode 2026 brackets
// the content render. Upstream may emit post-frame cursor visibility
// control (e.g. hide cursor when no CURSOR_MARKER is present), so the
// frame need not END with the disable sequence: it just must contain it
// after the rendered content.
func TestParityTUI_FirstRender_SyncOutputBracketed(t *testing.T) {
	_, raw := captureTUIRender(t, 5, 20, []string{"hello"})
	got := string(raw)
	if !strings.HasPrefix(got, "\x1b[?2026h") {
		t.Errorf("frame does not start with mode-2026 enable. raw: %q", got)
	}
	if !strings.Contains(got, "\x1b[?2026l") {
		t.Errorf("frame does not contain mode-2026 disable. raw: %q", got)
	}
}
