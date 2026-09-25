package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Upstream Editor.handleMouse delegates to SelectList, whose mousePressedIndex
// keeps a click attached to its pressed item when the press scrolls the list.
func TestEditorAutocompleteClickKeepsPressedItem(t *testing.T) {
	editor := NewEditor()
	editor.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	editor.SetText("/")
	editor.AutocompleteMove(6)
	before := editor.Render(40)
	start, _ := editor.autocompleteVisibleRange()
	row := editor.renderedVisibleLineCount + 3
	want := "/" + editor.autocompleteItems[start].Value + " "
	if !strings.Contains(stripANSI(before[row]), editor.autocompleteItems[start].Value) {
		t.Fatalf("pressed row does not display target: %q", before[row])
	}
	DispatchMouseEvent(editor, componentMouseEvent(MousePress, 1, row))
	editor.Render(40)
	DispatchMouseEvent(editor, componentMouseEvent(MouseClick, 1, row))
	if got := editor.Text(); got != want {
		t.Fatalf("click after press-induced scroll completed %q, want %q", got, want)
	}
}

func TestEditorAutocompletePressClearedByNewSuggestions(t *testing.T) {
	editor := NewEditor()
	editor.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	editor.SetText("/")
	editor.AutocompleteMove(6)
	editor.Render(40)
	row := editor.renderedVisibleLineCount + 3
	DispatchMouseEvent(editor, componentMouseEvent(MousePress, 1, row))
	editor.SetText("/mo")
	editor.Render(40)
	DispatchMouseEvent(editor, componentMouseEvent(MouseClick, 1, row))
	if got := editor.Text(); got != "/model " {
		t.Fatalf("new suggestions inherited the old press: %q", got)
	}
}

func TestEditorAutocompleteDeferredSuggestionsClearPress(t *testing.T) {
	editor := NewEditor()
	editor.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	editor.SetText("/")
	editor.AutocompleteMove(5)
	var apply func()
	editor.SetAsyncApply(func(next func()) { apply = next })
	editor.SetText("/mo")
	editor.Render(40)
	row := editor.renderedVisibleLineCount + 3
	DispatchMouseEvent(editor, componentMouseEvent(MousePress, 1, row))
	apply()
	editor.Render(40)
	DispatchMouseEvent(editor, componentMouseEvent(MouseClick, 1, row))
	if got := editor.Text(); got != "/model " {
		t.Fatalf("published suggestions inherited the old press: %q", got)
	}
}

func BenchmarkEditorAutocompleteMouseClick(b *testing.B) {
	editor := NewEditor()
	editor.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	b.ReportAllocs()
	for b.Loop() {
		editor.SetText("/")
		editor.AutocompleteMove(6)
		editor.Render(80)
		row := editor.renderedVisibleLineCount + 3
		DispatchMouseEvent(editor, componentMouseEvent(MousePress, 1, row))
		editor.Render(80)
		DispatchMouseEvent(editor, componentMouseEvent(MouseClick, 1, row))
	}
}

func TestEditorAutocompletePressedItemThroughAltScreen(t *testing.T) {
	h := newAltHarness(t, 40, 20, TuiAltScreenOptions{})
	editor := NewEditor()
	editor.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	editor.SetText("/")
	editor.AutocompleteMove(6)
	h.tui.Add(editor)
	h.start()
	start, _ := editor.autocompleteVisibleRange()
	want := "/" + editor.autocompleteItems[start].Value + " "
	row := editor.renderedVisibleLineCount + 3 + 1 // SGR coordinates are one-based.
	h.send(fmt.Sprintf("\x1b[<0;2;%dM", row))
	h.send(fmt.Sprintf("\x1b[<0;2;%dm", row))
	if got := editor.Text(); got != want {
		t.Fatalf("terminal click after press-induced scroll completed %q, want %q", got, want)
	}
}
