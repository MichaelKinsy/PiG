package codingagent

import "testing"

// editor.ts:373-381 and custom-editor.ts:26-30 (Pi 1.1.0): the constructor options set the padding (default 0, never negative), the autocomplete row limit (default 5, kept within 3 to 20) and embedWorkingStatus (default false).
func TestNewEditorWithOptionsAppliesTheConstructorOptions(t *testing.T) {
	ptr := func(n int) *int { return &n }
	for _, tc := range []struct {
		name         string
		options      CustomEditorOptions
		padding, max int
		embed        bool
	}{
		{"omitted options", CustomEditorOptions{}, 0, 5, false},
		{"values", CustomEditorOptions{PaddingX: ptr(2), AutocompleteMaxVisible: ptr(8), EmbedWorkingStatus: true}, 2, 8, true},
		{"negative padding", CustomEditorOptions{PaddingX: ptr(-3)}, 0, 5, false},
		{"limit below range", CustomEditorOptions{AutocompleteMaxVisible: ptr(1)}, 0, 3, false},
		{"limit above range", CustomEditorOptions{AutocompleteMaxVisible: ptr(99)}, 0, 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editor := NewEditorWithOptions(tc.options)
			if editor.PaddingX() != tc.padding || editor.AutocompleteMaxVisible() != tc.max || editor.EmbedWorkingStatus != tc.embed {
				t.Fatalf("editor = padding %d, max visible %d, embed %v; want %d, %d, %v", editor.PaddingX(), editor.AutocompleteMaxVisible(), editor.EmbedWorkingStatus, tc.padding, tc.max, tc.embed)
			}
		})
	}
}
