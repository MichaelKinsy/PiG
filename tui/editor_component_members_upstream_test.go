package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Ports editor.ts:427-437 addToHistory, driven through EditorWithHistory: text is trimmed, blank text and a repeat of the newest
// entry are ignored, and the history keeps the newest 100 entries; Up recalls the newest first.
func TestEditorWithHistoryAddToHistory(t *testing.T) {
	var editor EditorWithHistory = NewEditor()
	editor.AddToHistory("  first  ")
	editor.AddToHistory("   ")
	editor.AddToHistory("")
	editor.AddToHistory("first")
	editor.AddToHistory("second")
	editor.HandleInput("\x1b[A")
	if got := editor.Text(); got != "second" {
		t.Fatalf("Up after AddToHistory = %q, want the newest entry", got)
	}
	editor.HandleInput("\x1b[A")
	if got := editor.Text(); got != "first" {
		t.Fatalf("second Up = %q, want the trimmed older entry", got)
	}
	editor.HandleInput("\x1b[A")
	if got := editor.Text(); got != "first" {
		t.Fatalf("Up past the oldest entry = %q, want it to stay on the oldest (only two entries were kept)", got)
	}

	capped := NewEditor()
	for i := range 105 {
		capped.AddToHistory(fmt.Sprintf("entry %d", i))
	}
	for range 101 {
		capped.HandleInput("\x1b[A")
	}
	if got := capped.Text(); got != "entry 5" {
		t.Fatalf("oldest kept entry = %q, want entry 5 (editor.ts:434 pops past 100)", got)
	}
}

// Ports editor.ts:397-415 setPaddingX / setAutocompleteMaxVisible, driven through EditorWithAppearance: padding is floored at 0 and
// shifts the content; the visible autocomplete rows are clamped to 3..20.
func TestEditorWithAppearanceSetters(t *testing.T) {
	editor := NewEditor()
	var appearance EditorWithAppearance = editor
	appearance.SetAutocompleteMaxVisible(1)
	if got := editor.AutocompleteMaxVisible(); got != 3 {
		t.Fatalf("SetAutocompleteMaxVisible(1) = %d, want 3", got)
	}
	appearance.SetAutocompleteMaxVisible(99)
	if got := editor.AutocompleteMaxVisible(); got != 20 {
		t.Fatalf("SetAutocompleteMaxVisible(99) = %d, want 20", got)
	}
	appearance.SetAutocompleteMaxVisible(8)
	if got := editor.AutocompleteMaxVisible(); got != 8 {
		t.Fatalf("SetAutocompleteMaxVisible(8) = %d, want 8", got)
	}

	editor.SetText("x")
	appearance.SetPaddingX(-4)
	if got := editor.PaddingX(); got != 0 {
		t.Fatalf("SetPaddingX(-4) = %d, want 0", got)
	}
	appearance.SetPaddingX(3)
	if got := editor.PaddingX(); got != 3 {
		t.Fatalf("SetPaddingX(3) = %d, want 3", got)
	}
	var content string
	for _, line := range editor.Render(20) {
		if strings.Contains(line, "x") {
			content = line
		}
	}
	if !strings.HasPrefix(content, "   x") {
		t.Fatalf("content row %q is not indented by the three padding columns", content)
	}
}

// Ports editor.ts:417-421 setAutocompleteProvider, driven through EditorWithAutocomplete: the provider answers the next query.
func TestEditorWithAutocompleteSetAutocomplete(t *testing.T) {
	provider := &editorRequestCounter{result: &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "@a", Label: "a"}}, Prefix: "@"}}
	editor := NewEditor()
	defer editor.AutocompleteCancel()
	var autocomplete EditorWithAutocomplete = editor
	autocomplete.SetAutocomplete(provider)
	editor.HandleInput("@")
	if provider.requests == 0 {
		t.Fatal("the provider installed through EditorWithAutocomplete was never asked")
	}
}

// Ports editor.ts:1102-1104 getExpandedText, driven through EditorWithExpandedText: a pasted block shows as a marker in the text
// and expands to the pasted content.
func TestEditorWithExpandedTextExpandsPasteMarkers(t *testing.T) {
	editor := NewEditor()
	pasted := strings.Repeat("line\n", 12) + "end"
	editor.HandleInput("\x1b[200~" + pasted + "\x1b[201~")
	if !strings.Contains(editor.Text(), "[paste #1") {
		t.Fatalf("a large paste should leave a marker, text = %q", editor.Text())
	}
	var expanded EditorWithExpandedText = editor
	if got := expanded.GetExpandedText(); got != pasted {
		t.Fatalf("GetExpandedText() = %q, want the pasted content", got)
	}
}

// Ports editor.ts:237-240,376 EditorTheme and the constructor's theme argument: the injected theme styles the border and the
// autocomplete list instead of the active theme.
func TestNewEditorWithEditorThemeStylesBorderAndAutocomplete(t *testing.T) {
	provider := &editorRequestCounter{result: &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "@a", Label: "alpha"}, {Value: "@b", Label: "beta"}}, Prefix: "@"}}
	theme := EditorTheme{
		BorderColor: func(s string) string { return "<B>" + s + "</B>" },
		SelectList: SelectListTheme{
			SelectedPrefix: func(s string) string { return "<P>" + s + "</P>" },
			SelectedText:   func(s string) string { return "<S>" + s + "</S>" },
			Description:    func(s string) string { return s },
			ScrollInfo:     func(s string) string { return s },
			NoMatch:        func(s string) string { return s },
		},
	}
	editor := NewEditor(WithEditorTheme(theme))
	defer editor.AutocompleteCancel()
	editor.SetAutocomplete(provider)
	editor.HandleInput("@")
	rendered := strings.Join(editor.Render(40), "\n")
	if !strings.Contains(rendered, "<B>") {
		t.Errorf("the border does not use the injected BorderColor:\n%s", rendered)
	}
	// select-list.ts:205,216 applies only selectedText to the selected row; selectedPrefix is declared and never called.
	if !strings.Contains(rendered, "<S>→ alpha</S>") {
		t.Errorf("the autocomplete list does not use the injected SelectList theme:\n%s", rendered)
	}
	if plain := strings.Join(NewEditor().Render(40), "\n"); strings.Contains(plain, "<B>") {
		t.Errorf("an editor built without the option must keep the active theme:\n%s", plain)
	}
}
