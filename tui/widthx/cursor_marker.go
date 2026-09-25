// Ports upstream CURSOR_MARKER constant and TUI.extractCursorPosition
// (tui.ts:68 + tui.ts:868).
//
// CURSOR_MARKER is the zero-width APC sequence `\x1b_pi:c\x07`. Components
// that contain a focusable cursor (Editor, Input, FilterableList) emit this
// at the cursor position in their rendered output. The TUI scans the bottom
// `height` rendered lines for the marker, computes the visual column via
// VisibleWidth(textBeforeMarker), strips the marker from the line, and uses
// the resulting (row, col) to position the hardware terminal cursor at the
// end of the frame.
//
// This is how upstream pi achieves a real blinking terminal cursor at the
// edit position while also rendering a visible reverse-video cursor cell.

package widthx

import "strings"

// CursorMarker is the APC sequence that signals "the focusable cursor is
// here" when emitted in rendered component output. Already covered by
// ExtractAnsi as a regular APC sequence (so it does NOT count toward
// VisibleWidth and IS stripped by StripAnsi).
const CursorMarker = "\x1b_pi:c\x07"

// CursorPosition is the (row, col) of an extracted cursor marker.
type CursorPosition struct {
	Row, Col int
}

// ExtractCursorPosition searches the bottom `height` lines of `lines` for
// the first occurrence of CursorMarker, mutates `lines` in place to strip
// the marker, and returns its position. Returns (CursorPosition{}, false)
// if no marker is present.
//
// Mirrors upstream `TUI.extractCursorPosition` (tui.ts:868). The visual
// column is computed via VisibleWidth on the text preceding the marker,
// so it is grapheme-aware and ignores any prior ANSI styling.
func ExtractCursorPosition(lines []string, height int) (CursorPosition, bool) {
	if len(lines) == 0 || height <= 0 {
		return CursorPosition{}, false
	}
	viewportTop := max(len(lines)-height, 0)
	for row := len(lines) - 1; row >= viewportTop; row-- {
		line := lines[row]
		before, after, ok := strings.Cut(line, CursorMarker)
		if !ok {
			continue
		}
		col := VisibleWidth(before)
		lines[row] = before + after
		return CursorPosition{Row: row, Col: col}, true
	}
	return CursorPosition{}, false
}
