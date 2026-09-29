package tui

// Ports packages/tui/src/components/editor.ts

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// autocompleteView preserves the provider's byte-offset contract while the editor stores UTF-16 positions. A position inside a surrogate pair needs a split WTF-8 view; its UTF-16 units are unchanged.
func (e *Editor) autocompleteView() ([]string, int, int) {
	row, col := e.cursor[0], e.cursor[1]
	line := e.lines[row]
	length := jsstring.Length(line)
	if col >= length {
		return e.lines, row, len(line) + col - length
	}
	if col < 0 {
		return e.lines, row, col
	}
	before := jsstring.Slice(line, 0, col)
	if strings.HasPrefix(line, before) {
		return e.lines, row, len(before)
	}
	lines := slices.Clone(e.lines)
	lines[row] = before + jsstring.Slice(line, col)
	return lines, row, len(before)
}

func (e *Editor) applyAutocompleteState(lines []string, row, byteCol int) {
	col := byteCol
	if row >= 0 && row < len(lines) && byteCol >= 0 {
		line := lines[row]
		col = jsstring.Length(line[:min(byteCol, len(line))]) + max(0, byteCol-len(line))
	}
	e.lines = make([]string, len(lines))
	for i, line := range lines {
		e.lines[i] = jsstring.Canonical(line)
	}
	e.cursor[0] = row
	e.setCursorCol(col)
}
