package tui

import (
	"fmt"
	"testing"
)

// Ports "Editor component › Prompt history navigation" and the history cases
// of "Undo" (packages/tui/test/editor.test.ts), plus
// packages/tui/test/editor-history-keybindings.test.ts.

const (
	historyUp   = "\x1b[A"
	historyDown = "\x1b[B"
	historyLeft = "\x1b[D"
)

// kittyUndo is the platform undo key under the Kitty protocol (ctrl+- is
// \x1b[45;5u).
var kittyUndo = hostUndoKittyInput()

func assertEditorText(t *testing.T, e *Editor, want string) {
	t.Helper()
	if got := e.Text(); got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func assertEditorCursor(t *testing.T, e *Editor, line, col int) {
	t.Helper()
	if e.cursor != [2]int{line, col} {
		t.Fatalf("cursor = %v, want {%d %d}", e.cursor, line, col)
	}
}

func TestEditorHistoryUpDoesNothingWhenEmpty(t *testing.T) {
	e := NewEditor()
	e.HandleInput(historyUp)
	assertEditorText(t, e, "")
}

func TestEditorHistoryUpShowsMostRecentEntry(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("first prompt")
	e.AddToHistory("second prompt")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "second prompt")
}

func TestEditorHistoryUpCyclesEntries(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("first")
	e.AddToHistory("second")
	e.AddToHistory("third")
	for _, want := range []string{"third", "second", "first", "first"} {
		e.HandleInput(historyUp)
		assertEditorText(t, e, want)
	}
}

func TestEditorHistoryJumpsToStartBeforeEnteringFromDraft(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("prompt")
	e.SetText("draft")
	e.HandleInput(historyLeft)
	e.HandleInput(historyLeft)

	e.HandleInput(historyUp)
	assertEditorText(t, e, "draft")
	assertEditorCursor(t, e, 0, 0)

	e.HandleInput(historyUp)
	assertEditorText(t, e, "prompt")

	e.HandleInput(historyDown)
	assertEditorText(t, e, "draft")
	assertEditorCursor(t, e, 0, 0)
}

func TestEditorHistoryDownNavigatesForward(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("first")
	e.AddToHistory("second")
	e.AddToHistory("third")
	e.SetText("draft")
	for range 4 {
		e.HandleInput(historyUp)
	}
	for _, want := range []string{"second", "third", "draft"} {
		e.HandleInput(historyDown)
		assertEditorText(t, e, want)
	}
}

func TestEditorHistoryExitsOnTyping(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("old prompt")
	e.HandleInput(historyUp)
	e.HandleInput("x")
	assertEditorText(t, e, "xold prompt")
}

func TestEditorHistoryExitsOnSetText(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("first")
	e.AddToHistory("second")
	e.HandleInput(historyUp)
	e.SetText("")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "second")
}

func TestEditorHistorySkipsEmptyStrings(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("")
	e.AddToHistory("   ")
	e.AddToHistory("valid")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "valid")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "valid")
}

func TestEditorHistorySkipsConsecutiveDuplicates(t *testing.T) {
	e := NewEditor()
	for range 3 {
		e.AddToHistory("same")
	}
	e.HandleInput(historyUp)
	assertEditorText(t, e, "same")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "same")
}

func TestEditorHistoryKeepsNonConsecutiveDuplicates(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("first")
	e.AddToHistory("second")
	e.AddToHistory("first")
	for _, want := range []string{"first", "second", "first"} {
		e.HandleInput(historyUp)
		assertEditorText(t, e, want)
	}
}

func TestEditorHistoryUsesCursorMovementWithContent(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("history item")
	e.SetText("line1\nline2")
	e.HandleInput(historyUp)
	e.HandleInput("X")
	assertEditorText(t, e, "line1X\nline2")
}

func TestEditorHistoryLimitsTo100Entries(t *testing.T) {
	e := NewEditor()
	for i := range 105 {
		e.AddToHistory(fmt.Sprintf("prompt %d", i))
	}
	for range 100 {
		e.HandleInput(historyUp)
	}
	assertEditorText(t, e, "prompt 5")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "prompt 5")
}

func TestEditorHistoryUpwardPlacesCursorAtStart(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("older entry")
	e.AddToHistory("line1\nline2\nline3")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "line1\nline2\nline3")
	assertEditorCursor(t, e, 0, 0)
	e.HandleInput(historyUp)
	assertEditorText(t, e, "older entry")
	assertEditorCursor(t, e, 0, 0)
}

func TestEditorHistoryDownwardPlacesCursorAtEnd(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("older entry")
	e.AddToHistory("line1\nline2\nline3")
	e.AddToHistory("newer entry")
	for range 3 {
		e.HandleInput(historyUp)
	}
	e.HandleInput(historyDown)
	assertEditorText(t, e, "line1\nline2\nline3")
	assertEditorCursor(t, e, 2, 5)
	e.HandleInput(historyDown)
	assertEditorText(t, e, "newer entry")
}

func TestEditorHistoryAllowsOppositeCursorMovementInMultilineEntry(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("line1\nline2\nline3")
	e.HandleInput(historyUp)
	assertEditorCursor(t, e, 0, 0)
	e.HandleInput(historyDown)
	assertEditorText(t, e, "line1\nline2\nline3")
	assertEditorCursor(t, e, 1, 0)
	e.HandleInput(historyUp)
	assertEditorText(t, e, "line1\nline2\nline3")
	assertEditorCursor(t, e, 0, 0)
}

func typeInto(e *Editor, text string) {
	for _, r := range text {
		e.HandleInput(string(r))
	}
}

func TestEditorUndoExitsHistoryBrowsing(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("hello")
	assertEditorText(t, e, "")
	typeInto(e, "world")
	assertEditorText(t, e, "world")
	e.HandleInput("\x17") // Ctrl+W
	assertEditorText(t, e, "")

	e.HandleInput(historyUp)
	assertEditorText(t, e, "hello")

	e.HandleInput(kittyUndo)
	assertEditorText(t, e, "")
	e.HandleInput(kittyUndo)
	assertEditorText(t, e, "world")
}

func TestEditorUndoRestoresPreHistoryStateAfterSeveralNavigations(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("first")
	e.AddToHistory("second")
	e.AddToHistory("third")
	typeInto(e, "current")
	assertEditorText(t, e, "current")
	e.HandleInput("\x17") // Ctrl+W
	assertEditorText(t, e, "")

	for _, want := range []string{"third", "second", "first"} {
		e.HandleInput(historyUp)
		assertEditorText(t, e, want)
	}
	e.HandleInput(kittyUndo)
	assertEditorText(t, e, "")
	e.HandleInput(kittyUndo)
	assertEditorText(t, e, "current")
}

// Ports "Editor prompt history keybindings › browses history directly
// without first moving the cursor".
func TestEditorHistoryBindingsBrowseWithoutMovingTheCursor(t *testing.T) {
	previous := GetTUIKeybindings()
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{
		KBEditorHistoryPrevious: {"ctrl+p"},
		KBEditorHistoryNext:     {"ctrl+n"},
	}))
	e := NewEditor()
	e.AddToHistory("older prompt")
	e.AddToHistory("newer\nmultiline prompt")
	e.SetText("draft")
	e.HandleInput(historyLeft)
	e.HandleInput(historyLeft)

	e.HandleInput("\x10") // Ctrl+P
	assertEditorText(t, e, "newer\nmultiline prompt")
	assertEditorCursor(t, e, 0, 0)

	e.HandleInput("\x10")
	assertEditorText(t, e, "older prompt")

	e.HandleInput("\x0e") // Ctrl+N
	assertEditorText(t, e, "newer\nmultiline prompt")
	assertEditorCursor(t, e, 1, 16)

	e.HandleInput("\x0e")
	assertEditorText(t, e, "draft")
	assertEditorCursor(t, e, 0, 3)
}

// The history actions have no default key, as upstream.
func TestEditorHistoryBindingsAreUnboundByDefault(t *testing.T) {
	kb := NewTUIKeybindingsManager(nil)
	for _, id := range []string{KBEditorHistoryPrevious, KBEditorHistoryNext} {
		if keys := kb.GetKeys(id); len(keys) != 0 {
			t.Errorf("%s defaults = %v, want none", id, keys)
		}
		if !kb.HasBinding(id) {
			t.Errorf("%s is not a defined action", id)
		}
	}
}
