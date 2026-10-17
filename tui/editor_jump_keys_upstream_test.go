package tui

import "testing"

// packages/tui/src/components/editor.ts:974-981 (tui.editor.jumpBackward and jumpForward arm a jump mode), :698-713 (the next printable key performs
// the jump; either jump key again, or a control key, cancels; a control key then also runs as a normal key) and :2137-2165 jumpToChar: the
// current line is searched after (forward) or before (backward) the cursor, other lines in full; no match leaves the cursor in place.
// mutation-checked: a jump that searches from the cursor itself, skips the other lines, or ignores the jumpBackward key fails it.
func TestEditorCharacterJumpFollowsPiJumpToChar(t *testing.T) {
	forward := "\x1d"      // ctrl+] (keybindings.ts default tui.editor.jumpForward)
	backward := "\x1b\x1d" // ctrl+alt+] (default tui.editor.jumpBackward)
	steps := func(e *Editor, keys ...string) [2]int {
		for _, key := range keys {
			e.HandleInput(key)
		}
		return e.cursor
	}

	e := editorAt("abcabc", 0, 0)
	if got := steps(e, forward, "c"); got != [2]int{0, 2} {
		t.Fatalf("forward to c = %v, want (0,2): the search starts after the cursor", got)
	}
	if got := steps(e, forward, "c"); got != [2]int{0, 5} {
		t.Fatalf("forward to the next c = %v, want (0,5)", got)
	}
	if got := steps(e, forward, "c"); got != [2]int{0, 5} {
		t.Fatalf("forward with no later c = %v, want the cursor to stay at (0,5)", got)
	}
	if got := steps(e, backward, "b"); got != [2]int{0, 4} {
		t.Fatalf("backward to b = %v, want (0,4)", got)
	}
	if got := steps(e, backward, "b"); got != [2]int{0, 1} {
		t.Fatalf("backward to the previous b = %v, want (0,1): the search starts before the cursor", got)
	}
	if got := steps(e, backward, "b"); got != [2]int{0, 1} {
		t.Fatalf("backward with no earlier b = %v, want the cursor to stay at (0,1)", got)
	}

	// Other lines are searched in full, nearest first.
	e = editorAt("ab\ncd\nxc", 0, 0)
	if got := steps(e, forward, "c"); got != [2]int{1, 0} {
		t.Fatalf("forward across lines = %v, want (1,0)", got)
	}
	e = editorAt("ab\ncd\nxc", 2, 1)
	if got := steps(e, backward, "d"); got != [2]int{1, 1} {
		t.Fatalf("backward across lines = %v, want (1,1)", got)
	}
	// String.prototype.lastIndexOf clamps a negative start to 0, so a backward jump from column 0 still matches the character at column 0
	// and never reaches an earlier line.
	e = editorAt("x\nxax", 1, 0)
	if got := steps(e, backward, "x"); got != [2]int{1, 0} {
		t.Fatalf("backward to x from column 0 = %v, want (1,0): lastIndexOf(char, -1) matches index 0", got)
	}
}

// editor.ts:698-713: pressing either jump key again cancels the jump, so the next printable key is typed; a control key cancels and then runs
// as the ordinary key it is.
// mutation-checked: not cancelling on the same key, or on the other jump key, or swallowing the control key fails it.
func TestEditorCharacterJumpIsCancelledByAJumpKeyOrAControlKey(t *testing.T) {
	forward, backward := "\x1d", "\x1b\x1d"
	e := editorAt("abc", 0, 0)
	e.HandleInput(forward)
	e.HandleInput(forward)
	e.HandleInput("c")
	if e.Text() != "cabc" {
		t.Fatalf("after cancelling with the same key, c typed %q, want it inserted", e.Text())
	}
	e = editorAt("abc", 0, 0)
	e.HandleInput(forward)
	e.HandleInput(backward)
	e.HandleInput("c")
	if e.Text() != "cabc" {
		t.Fatalf("after cancelling with the other jump key, c typed %q, want it inserted", e.Text())
	}
	e = editorAt("abc", 0, 2)
	e.HandleInput(backward)
	e.HandleInput("\x01") // ctrl+a: line start
	if e.cursor != [2]int{0, 0} {
		t.Fatalf("a control key after a jump key = %v, want it to cancel and then move to the line start", e.cursor)
	}
	e.HandleInput("c")
	if e.Text() != "cabc" {
		t.Fatalf("after the control key, c typed %q, want it inserted (the jump was cancelled)", e.Text())
	}
}

// editor.ts:974-981 resolve both keys through kb.matches: a user binding replaces the default keys.
// mutation-checked: matching the literal default key instead of the binding fails it.
func TestEditorCharacterJumpFollowsRebinding(t *testing.T) {
	withEditorKeybindings(t, KeybindingsConfig{string(KBEditorJumpForward): {"ctrl+g"}, string(KBEditorJumpBackward): {"ctrl+o"}})
	e := editorAt("abcabc", 0, 0)
	e.HandleInput("\x1d") // the replaced default: nothing is armed, so c is typed
	e.HandleInput("c")
	if e.Text() != "cabcabc" {
		t.Fatalf("the replaced default jump key armed a jump: text %q", e.Text())
	}
	e = editorAt("abcabc", 0, 0)
	e.HandleInput("\x07") // ctrl+g
	e.HandleInput("c")
	if e.cursor != [2]int{0, 2} || e.Text() != "abcabc" {
		t.Fatalf("rebound forward jump = %v %q, want (0,2) and no insertion", e.cursor, e.Text())
	}
	e.HandleInput("\x0f") // ctrl+o
	e.HandleInput("a")
	if e.cursor != [2]int{0, 0} {
		t.Fatalf("rebound backward jump = %v, want (0,0)", e.cursor)
	}
}
