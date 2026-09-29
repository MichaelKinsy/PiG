package tui

import (
	"strings"
	"testing"
)

// Pi editor.ts:2237 uses SelectList's default layout for argument completions;
// select-list.ts:196-211 still renders the description in its 32-cell primary-column layout.
func TestEditorArgumentAutocompleteDescription(t *testing.T) {
	editor := NewEditor()
	editor.autocompletePrefix = "open"
	editor.autocompleteItems = []AutocompleteItem{{Value: "openrouter", Label: "openrouter", Description: "OpenRouter · subscription/API key"}}
	got := strings.TrimRight(stripANSI(editor.renderAutocomplete(100)[0]), " ")
	want := "→ openrouter" + strings.Repeat(" ", 32-len("openrouter")) + "OpenRouter · subscription/API key"
	if got != want {
		t.Fatalf("popup = %q, want %q", got, want)
	}
	if strings.Contains(stripANSI(editor.renderAutocomplete(40)[0]), "subscription") {
		t.Fatal("narrow popup should omit description")
	}
}
