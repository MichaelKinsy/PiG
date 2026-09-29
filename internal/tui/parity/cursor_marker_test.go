// Live parity tests for CURSOR_MARKER extraction through the real
// tui.TUI render loop.
//
// (cursor marker subset): added 2026-05-11.
package parity

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestParityTUI_CursorMarker_StrippedAndPositioned verifies that when a
// component emits CURSOR_MARKER in its rendered output, the TUI:
//  1. Strips the marker from the visible output (it must not appear in
//     the byte stream as bytes: only as a positioning escape).
//  2. Emits a relative cursor positioning escape (\x1b[<n>A/B + \x1b[<c>G)
//     so the hardware terminal cursor lands at the marker's column.
func TestParityTUI_CursorMarker_StrippedAndPositioned(t *testing.T) {
	// Single line "hello<marker>world": cursor at col 5.
	lines := []string{"hello" + widthx.CursorMarker + "world"}
	var buf bytes.Buffer
	ti := tui.NewWithOutput(&buf, 30, 5)
	ti.Add(&staticLines{lines: lines})
	ti.Render()

	out := buf.String()
	// Marker must NOT appear in the output byte stream: it was extracted.
	if strings.Contains(out, widthx.CursorMarker) {
		t.Errorf("CURSOR_MARKER leaked into output: %q", out)
	}
	// A CHA escape with column 6 (col+1) must be present.
	if !strings.Contains(out, "\x1b[6G") {
		t.Errorf("expected CHA to col 6 in output, got %q", out)
	}
	// The visible "helloworld" should still be there.
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("expected 'hello' and 'world' in output: %q", out)
	}
}

// TestParityTUI_CursorMarker_NoMarkerNoPositioning ensures that when no
// component emits a marker, no cursor-positioning escape is emitted (the
// legacy "cursor lands at end of last line" behavior is preserved).
func TestParityTUI_CursorMarker_NoMarkerNoPositioning(t *testing.T) {
	lines := []string{"hello", "world"}
	var buf bytes.Buffer
	ti := tui.NewWithOutput(&buf, 30, 5)
	ti.Add(&staticLines{lines: lines})
	ti.Render()

	out := buf.String()
	// CHA (\x1b[NG) must not appear: no marker, no positioning.
	for c := 1; c < 30; c++ {
		needle := "\x1b[" + itoa(c) + "G"
		if strings.Contains(out, needle) {
			t.Errorf("unexpected CHA escape %q in output: %q", needle, out)
		}
	}
}

// TestParityTUI_CursorMarker_AfterWideChar verifies the column is computed
// in display columns (grapheme/east-asian aware), not bytes or runes.
func TestParityTUI_CursorMarker_AfterWideChar(t *testing.T) {
	// "a你b" is 4 columns wide (1+2+1). Marker after → col 4.
	lines := []string{"a你b" + widthx.CursorMarker}
	var buf bytes.Buffer
	ti := tui.NewWithOutput(&buf, 30, 5)
	ti.Add(&staticLines{lines: lines})
	ti.Render()

	out := buf.String()
	if !strings.Contains(out, "\x1b[5G") {
		t.Errorf("expected CHA to col 5 (after wide char), got %q", out)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
