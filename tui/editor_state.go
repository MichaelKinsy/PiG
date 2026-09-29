package tui

// Ports packages/tui/src/components/editor.ts

import "slices"

// EditorCursor is a logical line and UTF-16 column, not a terminal-cell position.
type EditorCursor struct {
	Line int
	Col  int
}

// GetCursor returns the logical cursor position in JavaScript string units.
func (e *Editor) GetCursor() EditorCursor {
	return EditorCursor{Line: e.cursor[0], Col: e.cursor[1]}
}

// GetLines returns a defensive copy of the editor's logical lines.
func (e *Editor) GetLines() []string { return slices.Clone(e.lines) }
