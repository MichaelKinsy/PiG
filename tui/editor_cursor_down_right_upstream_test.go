package tui

import "testing"

// withEditorKeybindings installs user bindings as the global TUI keybindings for the test, as the host does for keybindings.json.
func withEditorKeybindings(t *testing.T, user KeybindingsConfig) {
	t.Helper()
	previous := GetKeybindings()
	SetKeybindings(NewTUIKeybindingsManager(user))
	t.Cleanup(func() { SetKeybindings(previous) })
}

func editorAt(text string, line, col int) *Editor {
	e := NewEditor()
	e.SetText(text)
	e.cursor = [2]int{line, col}
	_ = e.Render(20)
	return e
}

// packages/tui/src/components/editor.ts:939-948 (tui.editor.cursorDown): below the last visual line the cursor jumps to the end of the line
// (moveToLineEnd); on any other line moveCursor(1, 0) keeps the column (:1893-1904, moveToVisualLine); with a history entry on screen, the last
// line steps forward through history (navigateHistory(1)), back to the draft.
// mutation-checked: cursorDown not stepping a visual line, not jumping to the line end on the last line, or not entering history forward fails it.
func TestEditorCursorDownMovesJumpsToTheLineEndAndWalksHistoryForward(t *testing.T) {
	e := editorAt("ab\ncd", 0, 1)
	e.HandleInput("\x1b[B")
	if got := e.cursor; got != [2]int{1, 1} {
		t.Fatalf("down from (0,1) = %v, want (1,1)", got)
	}
	e.HandleInput("\x1b[B")
	if got := e.cursor; got != [2]int{1, 2} {
		t.Fatalf("down on the last line = %v, want the end of the line (1,2)", got)
	}
	e.HandleInput("\x1b[B")
	if got := e.cursor; got != [2]int{1, 2} || e.Text() != "ab\ncd" {
		t.Fatalf("down at the end = %v %q, want nothing to change", got, e.Text())
	}

	h := NewEditor()
	h.AddToHistory("older")
	_ = h.Render(20)
	h.HandleInput("\x1b[A")
	if h.Text() != "older" {
		t.Fatalf("up on an empty editor = %q, want the history entry", h.Text())
	}
	h.HandleInput("\x1b[B")
	if h.Text() != "" {
		t.Fatalf("down from the newest history entry = %q, want the empty draft", h.Text())
	}
}

// editor.ts:950-958 (tui.editor.cursorRight / cursorLeft -> moveCursor(0, ±1)) and :1906-1940: right moves one grapheme (an emoji or a base
// character with its combining mark is two UTF-16 units but one step), at the end of a line it wraps to the start of the next, and at the end of
// the last line it stays.
// mutation-checked: stepping one UTF-16 unit, not wrapping to the next line, or wrapping past the last line fails it.
func TestEditorCursorRightMovesByGraphemeWrapsAndStopsAtTheEnd(t *testing.T) {
	e := editorAt("a😀b", 0, 0)
	for _, want := range []int{1, 3, 4} {
		e.HandleInput("\x1b[C")
		if got := e.cursor; got != [2]int{0, want} {
			t.Fatalf("right over %q = %v, want column %d", e.Text(), got, want)
		}
	}
	e = editorAt("e\u0301x", 0, 0)
	e.HandleInput("\x1b[C")
	if got := e.cursor; got != [2]int{0, 2} {
		t.Fatalf("right over a combining mark = %v, want column 2", got)
	}

	e = editorAt("ab\ncd", 0, 2)
	e.HandleInput("\x1b[C")
	if got := e.cursor; got != [2]int{1, 0} {
		t.Fatalf("right at the end of a line = %v, want the start of the next (1,0)", got)
	}
	e.cursor = [2]int{1, 2}
	e.HandleInput("\x1b[C")
	if got := e.cursor; got != [2]int{1, 2} {
		t.Fatalf("right at the end of the text = %v, want (1,2)", got)
	}
}

// editor.ts:939 and :950 resolve both keys through kb.matches: a user binding replaces the default keys, so the rebound key moves and the old
// arrow does nothing (keybindings.ts:73, :75 default "down" and "right"/"ctrl+f").
// mutation-checked: matching the literal arrow instead of the binding fails it.
func TestEditorCursorDownAndRightFollowRebinding(t *testing.T) {
	withEditorKeybindings(t, KeybindingsConfig{string(KBEditorCursorDown): {"ctrl+n"}, string(KBEditorCursorRight): {"ctrl+g"}})
	e := editorAt("abc\ndef", 0, 0)
	e.HandleInput("\x1b[B")
	e.HandleInput("\x1b[C")
	if got := e.cursor; got != [2]int{0, 0} {
		t.Fatalf("the replaced default keys moved the cursor to %v", got)
	}
	e.HandleInput("\x07")
	if got := e.cursor; got != [2]int{0, 1} {
		t.Fatalf("rebound right = %v, want (0,1)", got)
	}
	e.HandleInput("\x0e")
	if got := e.cursor; got != [2]int{1, 1} {
		t.Fatalf("rebound down = %v, want (1,1)", got)
	}
}
