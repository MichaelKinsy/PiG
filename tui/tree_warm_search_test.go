package tui

import (
	"fmt"
	"testing"
)

type warmCountingNode struct {
	id, search string
	calls      *int
}

func (n *warmCountingNode) NodeID() string           { return n.id }
func (n *warmCountingNode) NodeLabel() string        { return n.id }
func (n *warmCountingNode) NodeChildren() []TreeNode { return nil }
func (n *warmCountingNode) NodeSearchableText() string {
	*n.calls++
	return n.search
}

// handoffs/P1-real-session.md proposal 6: WarmSearchTexts reads every row's searchable text once, so the first search keystroke reads none, and the
// matches are those of a keystroke typed without the warm-up.
// mutation-checked: a WarmSearchTexts that does nothing leaves the first keystroke to read all 300 texts and fails the count.
func TestWarmSearchTextsMakesTheFirstSearchKeystrokeAScan(t *testing.T) {
	build := func() (*TreeList, *int) {
		calls := 0
		var roots []TreeNode
		for i := range 300 {
			text := "plain row"
			if i%3 == 0 {
				text = "Needle in row"
			}
			roots = append(roots, &warmCountingNode{id: fmt.Sprintf("n%d", i), search: text, calls: &calls})
		}
		return NewTreeSelectWithInitialFilter("t", treeRoots(roots), "all").GetTreeList(), &calls
	}
	warm, warmCalls := build()
	warm.WarmSearchTexts()
	if *warmCalls != 300 {
		t.Fatalf("warming read %d texts, want one per row (300)", *warmCalls)
	}
	warm.searchQuery = "needle"
	warm.recomputeVisible()
	if *warmCalls != 300 {
		t.Fatalf("the first keystroke read %d more texts after the warm-up", *warmCalls-300)
	}
	cold, coldCalls := build()
	cold.searchQuery = "needle"
	cold.recomputeVisible()
	if *coldCalls != 300 {
		t.Fatalf("without the warm-up the first keystroke read %d texts, want 300", *coldCalls)
	}
	if len(warm.rows) != 100 || len(cold.rows) != 100 {
		t.Fatalf("matches: warm %d, cold %d, want 100", len(warm.rows), len(cold.rows))
	}
}
