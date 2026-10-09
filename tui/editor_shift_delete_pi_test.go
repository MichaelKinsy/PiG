package tui

import "testing"

// TestEditorShiftBackspaceAndShiftDeleteDeleteACharacter ports upstream editor.ts handleInput (packages/tui/src/components/editor.ts:840-847):
// deleteCharBackward is `tui.editor.deleteCharBackward || matchesKey(data, "shift+backspace")` and deleteCharForward is
// `tui.editor.deleteCharForward || matchesKey(data, "shift+delete")`, so the shifted keys delete like the plain ones and are not inserted
// as text, whatever the user bound the actions to.
func TestEditorShiftBackspaceAndShiftDeleteDeleteACharacter(t *testing.T) {
	previous := globalTUIKeybindings.Load()
	t.Cleanup(func() { globalTUIKeybindings.Store(previous) })
	for _, tc := range []struct {
		name  string
		user  map[string][]string
		input string
		home  bool
		want  string
	}{
		{"shift+backspace (kitty)", nil, "\x1b[127;2u", false, "ab"},
		{"shift+delete (CSI ~)", nil, "\x1b[3;2~", true, "bc"},
		{"shift+delete (rxvt)", nil, "\x1b[3$", true, "bc"},
		{"shift+backspace with deleteCharBackward rebound", map[string][]string{string(KBEditorDeleteCharBack): {"ctrl+h"}}, "\x1b[127;2u", false, "ab"},
		{"shift+delete with deleteCharForward rebound", map[string][]string{string(KBEditorDeleteCharForward): {"ctrl+g"}}, "\x1b[3;2~", true, "bc"},
	} {
		SetKeybindings(NewTUIKeybindingsManager(tc.user))
		e := NewEditor()
		e.SetText("abc")
		if tc.home {
			e.HandleInput("\x01")
		}
		e.HandleInput(tc.input)
		if got := e.Text(); got != tc.want {
			t.Errorf("%s: text = %q, want %q", tc.name, got, tc.want)
		}
	}
}
