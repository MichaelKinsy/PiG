package tui

import (
	"strings"
	"testing"
)

// Pi LabelInput renders keyHint("tui.select.confirm", "save") and keyHint("tui.select.cancel", "cancel"); its keyHint resolves actions without capitalizing before styling (tree-selector.ts:1313).
func TestTreeLabelHintsResolveConfiguredKeys(t *testing.T) {
	for _, tc := range []struct {
		name            string
		bindings        map[string][]string
		confirm, cancel string
	}{
		{"defaults", nil, "enter", "escape/ctrl+c"},
		{"rebound", map[string][]string{KBSelectConfirm: {"ctrl+s"}, KBSelectCancel: {"ctrl+q"}}, "ctrl+s", "ctrl+q"},
		{"alternatives", map[string][]string{KBSelectConfirm: {"ctrl+s", "enter"}, KBSelectCancel: {"ctrl+q", "escape"}}, "ctrl+s/enter", "ctrl+q/escape"},
		{"unbound", map[string][]string{KBSelectConfirm: {}, KBSelectCancel: {}}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTreeKeybindings(t, tc.bindings)
			selector := NewTreeSelect("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "entry", label: "message"}}})
			selector.OnLabelEdit = func(string, string) {}
			selector.HandleInput("L")
			lines := selector.Render(120)
			want := "  " + KeyHint(tc.confirm, "save") + "  " + KeyHint(tc.cancel, "cancel")
			found := false
			for _, line := range lines {
				if line == want {
					found = true
				}
				if strings.Contains(line, KBSelectConfirm) || strings.Contains(line, KBSelectCancel) {
					t.Errorf("internal action identifier leaked into label UI: %q", line)
				}
			}
			if !found {
				t.Fatalf("missing exact configured hint row %q in %#v", want, lines)
			}
		})
	}
}
