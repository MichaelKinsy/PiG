package tui

import "github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/widthx"

// CursorMarker is pi-tui's CURSOR_MARKER: a zero-width APC sequence a focused component emits at its cursor position, which the
// renderer finds, strips and turns into the hardware cursor. visibleWidth counts it as zero columns.
const CursorMarker = "\x1b_pi:c\x07"

// DefaultEllipsis is truncateToWidth's default ellipsis.
const DefaultEllipsis = "..."

// VisibleWidth is pi-tui's visibleWidth: the terminal columns of text, ignoring ANSI/OSC/APC sequences and counting each grapheme
// cluster as the terminal draws it.
func VisibleWidth(text string) int { return widthx.VisibleWidth(text) }

// StripTerminalSequences is pi-tui's stripTerminalSequences: text without its CSI, OSC and APC (and other ESC) sequences.
func StripTerminalSequences(text string) string { return widthx.StripTerminalSequences(text) }

// TruncateToWidth is pi-tui's truncateToWidth: text cut to at most maxWidth columns with ellipsis appended when it was cut, keeping
// ANSI state valid. Pass DefaultEllipsis for upstream's default. With pad, a shorter result is padded with spaces to maxWidth.
func TruncateToWidth(text string, maxWidth int, ellipsis string, pad bool) string {
	return widthx.TruncateToWidth(text, maxWidth, ellipsis, pad)
}

// WrapTextWithAnsi is pi-tui's wrapTextWithAnsi: text word-wrapped to width columns, carrying active SGR styling and OSC 8 links
// from line to line.
func WrapTextWithAnsi(text string, width int) []string { return widthx.WrapTextWithAnsi(text, width) }

// SliceByColumn is pi-tui's sliceByColumn: the visible columns [startCol, startCol+length) of line with ANSI codes preserved. With
// strict, a wide character that straddles the end is left out.
func SliceByColumn(line string, startCol, length int, strict bool) string {
	return widthx.SliceByColumn(line, startCol, length, strict)
}

// SliceResult is the value of pi-tui's sliceWithWidth.
type SliceResult struct {
	Text  string
	Width int
}

// SliceWithWidth is pi-tui's sliceWithWidth: SliceByColumn with the width of the returned text.
func SliceWithWidth(line string, startCol, length int, strict bool) SliceResult {
	r := widthx.SliceWithWidth(line, startCol, length, strict)
	return SliceResult{Text: r.Text, Width: r.Width}
}

// SegmentsResult is the value of pi-tui's extractSegments.
type SegmentsResult struct {
	Before      string
	BeforeWidth int
	After       string
	AfterWidth  int
}

// ExtractSegments is pi-tui's extractSegments: the columns before beforeEnd and the afterLen columns from afterStart in one pass.
func ExtractSegments(line string, beforeEnd, afterStart, afterLen int, strictAfter bool) SegmentsResult {
	r := widthx.ExtractSegments(line, beforeEnd, afterStart, afterLen, strictAfter)
	return SegmentsResult{Before: r.Before, BeforeWidth: r.BeforeWidth, After: r.After, AfterWidth: r.AfterWidth}
}

// GetOsc8LinkAtColumn is pi-tui's getOsc8LinkAtColumn: the OSC 8 hyperlink target covering the visible column of line.
func GetOsc8LinkAtColumn(line string, column int) (url string, ok bool) {
	return widthx.GetOsc8LinkAtColumn(line, column)
}

// GetActiveBackgroundAnsi is pi-tui's getActiveBackgroundAnsi: the background SGR sequence still active at the end of text.
func GetActiveBackgroundAnsi(text string) string { return widthx.GetActiveBackgroundAnsi(text) }
