package tui

import "testing"

// TestEditorInputCopyKeyIsLeftToTheParent ports upstream editor.ts handleInput
// (packages/tui/src/components/editor.ts:745-748): "Ctrl+C - let parent handle
// (exit/clear)". When data matches tui.input.copy the editor returns without
// editing, and the match goes through the keybinding manager, so a user who
// rebinds tui.input.copy (keybindings.ts:146 default ctrl+c, "Copy selection")
// makes that key inert and frees ctrl+c.
func TestEditorInputCopyKeyIsLeftToTheParent(t *testing.T) {
	previous := globalTUIKeybindings.Load()
	t.Cleanup(func() { globalTUIKeybindings.Store(previous) })

	def, ok := NewTUIKeybindingsManager(nil).GetDefinition(KBInputCopy)
	if !ok || def.Description != "Copy selection" || len(def.DefaultKeys) != 1 || def.DefaultKeys[0] != "ctrl+c" {
		t.Fatalf("tui.input.copy definition = %+v, want ctrl+c / Copy selection (keybindings.ts:146)", def)
	}

	type step struct {
		name  string
		user  map[string][]string
		input string
		want  string
	}
	for _, tc := range []step{
		{"default: x is inserted", nil, "x", "x"},
		{"default: ctrl+c is left to the parent", nil, "\x03", ""},
		{"rebound: x is left to the parent", map[string][]string{string(KBInputCopy): {"x"}}, "x", ""},
	} {
		SetKeybindings(NewTUIKeybindingsManager(tc.user))
		e := NewEditor()
		e.HandleInput(tc.input)
		if got := e.Text(); got != tc.want {
			t.Errorf("%s: text = %q, want %q", tc.name, got, tc.want)
		}
	}
}
