package tui

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi tree-selector.ts:1006-1115 orders matching actions explicitly, even when a user binds more than one action to the same key.
func TestTreeInputBindingPriority(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, initial, want string
		copy, done                         bool
	}{
		{"up before copy", KBSelectUp, "app.message.copy", "b", "a", false, false},
		{"up before confirm", KBSelectUp, KBSelectConfirm, "b", "a", false, false},
		{"up before cancel", KBSelectUp, KBSelectCancel, "b", "a", false, false},
		{"down before copy", KBSelectDown, "app.message.copy", "a", "b", false, false},
		{"copy before cancel", "app.message.copy", KBSelectCancel, "b", "b", true, false},
		{"confirm before copy", KBSelectConfirm, "app.message.copy", "b", "b", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			treeHelpTestKeybindings(t, map[string][]string{tc.first: {"ctrl+x"}, tc.second: {"ctrl+x"}})
			selector := NewTreeSelect("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "a", label: "a"}, &fakeNode{id: "b", label: "b"}}})
			selector.SetInitialCursor(tc.initial, "")
			copied := false
			selector.OnCopy = func(*string) { copied = true }
			selector.HandleInput("\x18")
			if got := selector.rows[selector.cursor].id; got != tc.want || copied != tc.copy || selector.Done() != tc.done {
				t.Fatalf("selected=%q copied=%v done=%v; want %q %v %v", got, copied, selector.Done(), tc.want, tc.copy, tc.done)
			}
		})
	}
	t.Run("fold before page", func(t *testing.T) {
		treeHelpTestKeybindings(t, map[string][]string{"app.tree.foldOrUp": {"ctrl+x"}, KBSelectPageUp: {"ctrl+x"}})
		selector := NewTreeSelect("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "a", label: "a", kids: []TreeNode{&fakeNode{id: "child", label: "child"}}}, &fakeNode{id: "b", label: "b"}}})
		selector.SetInitialCursor("a", "")
		selector.HandleInput("\x18")
		if !selector.foldedNodes["a"] {
			t.Fatal("page action won over branch folding")
		}
	})
	t.Run("backspace before label editing", func(t *testing.T) {
		treeHelpTestKeybindings(t, map[string][]string{KBEditorDeleteCharBack: {"ctrl+g"}, "app.tree.editLabel": {"ctrl+g"}})
		selector := NewTreeSelect("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "a", label: "a"}}})
		selector.OnLabelEdit = func(string, string) {}
		selector.HandleInput("ab")
		selector.HandleInput("\x07")
		if selector.searchQuery != "a" || selector.labelInput != nil {
			t.Fatalf("query=%q editingLabel=%v", selector.searchQuery, selector.labelInput != nil)
		}
	})
}

// Pi binds filter cycling to app.tree.filter.cycleForward/Backward, not raw Tab bytes.
func TestTreeTabRequiresFilterBinding(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound", true: "bound"}[bound], func(t *testing.T) {
			var bindings map[string][]string
			if bound {
				bindings = map[string][]string{"app.tree.filter.cycleForward": {"tab"}, "app.tree.filter.cycleBackward": {"shift+tab"}}
			}
			treeHelpTestKeybindings(t, bindings)
			selector := NewTreeSelect("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "a", label: "a"}}})
			selector.HandleInput("\t")
			want := "default"
			if bound {
				want = "no-tools"
			}
			if selector.filterMode != want {
				t.Fatalf("Tab filter=%q, want %q", selector.filterMode, want)
			}
			selector.HandleInput("\x1b[Z")
			if selector.filterMode != "default" {
				t.Fatalf("Shift+Tab filter=%q", selector.filterMode)
			}
		})
	}
}

// Pi tree-selector.ts:1092 removes one UTF-16 code unit with searchQuery.slice(0, -1).
func TestTreeSearchBackspaceUsesUTF16Units(t *testing.T) {
	treeHelpTestKeybindings(t, nil)
	selector := NewTreeSelect("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "a", label: "a😀"}}})
	selector.HandleInput("a😀")
	for _, want := range [][]uint16{{'a', 0xd83d}, {'a'}, {}} {
		selector.HandleInput("\x7f")
		if got := jsstring.ToUTF16(selector.searchQuery); !slices.Equal(got, want) {
			t.Fatalf("query units=%x, want %x", got, want)
		}
	}
}
