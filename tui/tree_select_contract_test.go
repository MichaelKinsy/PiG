package tui

import "testing"

// A folded row hides its descendants, so it has no visible children and is not foldable (tree-selector.ts isFoldable reads visibleChildrenMap). A second
// fold key therefore climbs to the branch segment start instead of folding again.
func TestTreeFoldedRowIsNotFoldable(t *testing.T) {
	useTreeKeybindings(t, nil)
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A", kids: []TreeNode{&fakeNode{id: "a1", label: "A1"}, &fakeNode{id: "a2", label: "A2"}}},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelectorComponent("", root)
	ts.cursor = 1
	if !ts.isFoldable(ts.rows[ts.cursor]) {
		t.Fatal("a branch segment start with visible children must be foldable")
	}
	ts.HandleInput("\x1b[1;5D")
	if got := ts.rows[ts.cursor].id; got != "a" || !ts.foldedNodes["a"] {
		t.Fatalf("fold left the cursor on %q, folded=%v", got, ts.foldedNodes)
	}
	if ts.isFoldable(ts.rows[ts.cursor]) {
		t.Error("a folded row has no visible children and must not be foldable")
	}
}
