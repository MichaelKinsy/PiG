package tui

import (
	"sync/atomic"
	"testing"
	"time"
)

func treeSelectorTestNodes() []TreeNode {
	return []TreeNode{&editableTreeTestNode{fakeNodeWithBranchLabel: fakeNodeWithBranchLabel{fakeNode: fakeNode{id: "a", label: "first", kids: []TreeNode{
		&fakeNode{id: "b", label: "second", kids: []TreeNode{&editableTreeTestNode{fakeNodeWithBranchLabel: fakeNodeWithBranchLabel{fakeNode: fakeNode{id: "c", label: "third"}}}}},
	}}}}}
}

// tree-selector.ts constructor: the window is max(5, floor(terminalHeight / 2)) rows, and currentLeafId / initialSelectedId choose the
// opening row (the leaf by default, the selected id when given).
func TestNewTreeSelectorComponentFromOpeningState(t *testing.T) {
	leaf, selected := "c", "a"
	for _, tc := range []struct {
		name           string
		height         int
		leaf, selected *string
		wantVisible    int
		wantID         string
	}{
		{"tall terminal, current leaf", 40, &leaf, nil, 20, "c"},
		{"short terminal keeps five rows", 6, &leaf, nil, 5, "c"},
		{"initialSelectedId wins over the leaf", 24, &leaf, &selected, 12, "a"},
		{"no leaf opens on the last row", 24, nil, nil, 12, "c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := NewTreeSelectorComponentFrom(treeSelectorTestNodes(), tc.leaf, tc.height, nil, nil, nil, tc.selected, "default")
			if got := selector.visibleLines(); got != tc.wantVisible {
				t.Errorf("visible rows = %d, want %d", got, tc.wantVisible)
			}
			node := selector.GetTreeList().GetSelectedNode()
			if node == nil || node.NodeID() != tc.wantID {
				t.Errorf("opened on %v, want %s", node, tc.wantID)
			}
		})
	}
	// initialFilterMode selects the first visibility pass; an unknown mode is the default.
	if got := NewTreeSelectorComponentFrom(treeSelectorTestNodes(), nil, 24, nil, nil, nil, nil, "labeled-only").filterMode; got != "labeled-only" {
		t.Errorf("filter mode = %q, want labeled-only", got)
	}
	if got := NewTreeSelectorComponentFrom(treeSelectorTestNodes(), nil, 24, nil, nil, nil, nil, "bogus").filterMode; got != "default" {
		t.Errorf("unknown filter mode = %q, want default", got)
	}
}

// tree-selector.ts constructor: onSelect and onCancel are the TreeList callbacks and onLabelChange gets (entryId, label | undefined).
func TestNewTreeSelectorComponentFromCallbacks(t *testing.T) {
	treeHelpTestKeybindings(t, nil)
	var selected, labelled string
	var label *string
	cancelled := 0
	selector := NewTreeSelectorComponentFrom(treeSelectorTestNodes(), nil, 24,
		func(id string) { selected = id },
		func() { cancelled++ },
		func(id string, value *string) { labelled, label = id, value },
		nil, "default")
	selector.HandleInput("\r")
	if selected != "c" {
		t.Fatalf("onSelect got %q, want c", selected)
	}
	selector = NewTreeSelectorComponentFrom(treeSelectorTestNodes(), nil, 24, nil, func() { cancelled++ }, nil, nil, "default")
	selector.HandleInput("\x1b")
	if cancelled != 1 {
		t.Fatalf("onCancel ran %d times, want 1", cancelled)
	}
	selector = NewTreeSelectorComponentFrom(treeSelectorTestNodes(), nil, 24, nil, nil, func(id string, value *string) { labelled, label = id, value }, nil, "default")
	selector.HandleInput("L")
	selector.HandleInput("x")
	selector.HandleInput("\r")
	if labelled != "c" || label == nil || *label != "x" {
		t.Fatalf("onLabelChange got %q, %v; want c, x", labelled, label)
	}
	// A cleared label is undefined, not the empty string.
	selector.HandleInput("L")
	selector.HandleInput("\x15")
	selector.HandleInput("\x0b")
	selector.HandleInput("\r")
	if labelled != "c" || label != nil {
		t.Fatalf("a cleared label arrived as %q, %v; want c, nil", labelled, label)
	}
}

// tree-selector.ts constructor: an empty tree runs onCancel from setTimeout(…, 100), not at once.
func TestNewTreeSelectorComponentFromEmptyTreeCancelsAfterADelay(t *testing.T) {
	var cancelled atomic.Int32
	NewTreeSelectorComponentFrom(nil, nil, 24, nil, func() { cancelled.Add(1) }, nil, nil, "default")
	if cancelled.Load() != 0 {
		t.Fatal("an empty tree cancelled synchronously")
	}
	time.Sleep(40 * time.Millisecond)
	if cancelled.Load() != 0 {
		t.Fatal("an empty tree cancelled before 100ms")
	}
	deadline := time.Now().Add(2 * time.Second)
	for cancelled.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cancelled.Load() != 1 {
		t.Fatalf("an empty tree called onCancel %d times, want 1", cancelled.Load())
	}
	// A non-empty tree never self-cancels.
	NewTreeSelectorComponentFrom(treeSelectorTestNodes(), nil, 24, nil, func() { cancelled.Add(1) }, nil, nil, "default")
	time.Sleep(200 * time.Millisecond)
	if cancelled.Load() != 1 {
		t.Fatal("a non-empty tree self-cancelled")
	}
}
