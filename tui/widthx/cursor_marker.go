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

// FakeCursorStart and FakeCursorEnd are the zero-width APC markers around the text a component draws as its cursor
// (tui.ts FAKE_CURSOR_START / FAKE_CURSOR_END). ResolveFakeCursors turns them into reverse video, or drops the cursor the terminal
// draws itself.
const (
	FakeCursorStart = "\x1b_pi:fc\x07"
	FakeCursorEnd   = "\x1b_pi:/fc\x07"
)

// RenderFakeCursor wraps text in the fake cursor markers (tui.ts renderFakeCursor).
func RenderFakeCursor(text string) string {
	return FakeCursorStart + text + FakeCursorEnd
}

// focusedFakeCursor is CURSOR_MARKER directly followed by FAKE_CURSOR_START: the cursor of the focused component, at the position
// the hardware cursor takes.
const focusedFakeCursor = CursorMarker + FakeCursorStart

// ResolveFakeCursors resolves the fake cursor markers of lines to reverse video (tui.ts TUI.resolveFakeCursors). When the hardware
// cursor is shown, the fake cursor directly after CursorMarker is dropped, because the terminal draws the cursor there: its text
// stays, and a line truncated right after the cursor cell, which lost the end marker, loses everything after the cursor marker.
// The fake cursor of a component without focus has no CursorMarker and stays visible. Lines are returned as given when none holds
// a marker; otherwise a copy is returned and the argument is not modified.
func ResolveFakeCursors(lines []string, showHardwareCursor bool) []string {
	var out []string
	for i, line := range lines {
		if !strings.Contains(line, FakeCursorStart) {
			continue
		}
		if out == nil {
			out = make([]string, len(lines))
			copy(out, lines)
		}
		out[i] = resolveFakeCursorLine(line, showHardwareCursor)
	}
	if out == nil {
		return lines
	}
	return out
}

// ResolveFakeCursorsInPlace is ResolveFakeCursors for a frame the caller owns: each line that holds a marker is replaced by its
// resolved line in lines itself, so resolving a frame makes no copy of it.
func ResolveFakeCursorsInPlace(lines []string, showHardwareCursor bool) {
	for i, line := range lines {
		if strings.Contains(line, FakeCursorStart) {
			lines[i] = resolveFakeCursorLine(line, showHardwareCursor)
		}
	}
}

// ResolveFakeCursorLine is ResolveFakeCursors for one line.
func ResolveFakeCursorLine(line string, showHardwareCursor bool) string {
	if !strings.Contains(line, FakeCursorStart) {
		return line
	}
	return resolveFakeCursorLine(line, showHardwareCursor)
}

func resolveFakeCursorLine(line string, showHardwareCursor bool) string {
	if showHardwareCursor {
		line = dropFocusedFakeCursor(line)
	}
	return replaceFakeCursorMarkers(line)
}

// fakeCursorMarkerPrefix starts both fake cursor markers (and CursorMarker).
const fakeCursorMarkerPrefix = "\x1b_pi:"

// replaceFakeCursorMarkers is tui.ts's replaceAll(FAKE_CURSOR_START, "\x1b[7m") then replaceAll(FAKE_CURSOR_END, "\x1b[27m") in
// one pass: the markers never overlap and neither replacement can complete a marker, so the line is the same. The replacements
// are shorter than the markers, so the line's length bounds the result and it is one allocation.
func replaceFakeCursorMarkers(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	for {
		at := strings.Index(line, fakeCursorMarkerPrefix)
		if at < 0 {
			break
		}
		rest := line[at:]
		switch {
		case strings.HasPrefix(rest, FakeCursorStart):
			b.WriteString(line[:at])
			b.WriteString("\x1b[7m")
			line = rest[len(FakeCursorStart):]
		case strings.HasPrefix(rest, FakeCursorEnd):
			b.WriteString(line[:at])
			b.WriteString("\x1b[27m")
			line = rest[len(FakeCursorEnd):]
		default:
			// No marker begins inside the prefix: ESC is only its first byte.
			b.WriteString(line[:at+len(fakeCursorMarkerPrefix)])
			line = rest[len(fakeCursorMarkerPrefix):]
		}
	}
	b.WriteString(line)
	return b.String()
}

// dropFocusedFakeCursor replaces the first CursorMarker + FakeCursorStart + text + FakeCursorEnd with CursorMarker + text. A missing
// end marker (truncation) takes the text to the end of the line (the regexp `(\x1b_pi:c\x07)\x1b_pi:fc\x07(.*?)(?:\x1b_pi:\/fc\x07|$)`
// with the s flag).
func dropFocusedFakeCursor(line string) string {
	before, after0, ok := strings.Cut(line, focusedFakeCursor)
	if !ok {
		return line
	}
	rest := after0
	text, after, _ := strings.Cut(rest, FakeCursorEnd)
	return before + CursorMarker + text + after
}
