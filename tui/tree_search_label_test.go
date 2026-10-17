package tui

import "testing"

type searchCountingNode struct {
	id, label, search string
	labels            *int
}

func (n *searchCountingNode) NodeID() string           { return n.id }
func (n *searchCountingNode) NodeChildren() []TreeNode { return nil }
func (n *searchCountingNode) NodeLabel() string {
	*n.labels++
	return n.label
}
func (n *searchCountingNode) NodeSearchableText() string { return n.search }

// A search over nodes that supply searchable text matches that text and formats no display label for it: on a long session tree each label
// can cost a read of the row's entry, and the search discarded it.
func TestTreeSearchDoesNotFormatLabelsOfNodesWithSearchableText(t *testing.T) {
	labels := 0
	var roots []TreeNode
	for i := range 200 {
		roots = append(roots, &searchCountingNode{id: string(rune('a'+i%26)) + string(rune('A'+i/26)), label: "row", search: "needle here", labels: &labels})
	}
	list := NewTreeSelectWithInitialFilter("t", treeRoots(roots), "all").GetTreeList()
	list.MaxVisibleLines = 5
	visibleLabels := labels
	for _, r := range list.allRows {
		list.rowSearchText(r)
	}
	if labels != visibleLabels {
		t.Fatalf("searching formatted %d labels", labels-visibleLabels)
	}
	if got := list.rowSearchText(list.allRows[0]); got != "needle here" {
		t.Fatalf("search text %q", got)
	}
}
