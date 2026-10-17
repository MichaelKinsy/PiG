//go:build !race && linux

package tui

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// treeSizes are the node counts N, 10N and 100N of the /tree scaling gates.
var treeSizes = [3]int{100, 1000, 10000}

// chainTree is a session that never branched: size nodes, each the only child of the one before.
func chainTree(size int) (root TreeNode, last string) {
	rootNode := &fakeNodeTagged{id: "root"}
	parent := rootNode
	for i := range size {
		node := &fakeNodeTagged{id: fmt.Sprint(i), label: fmt.Sprintf("message %d", i)}
		if i%2 == 0 {
			node.tags = []string{"user"}
		}
		parent.kids = []TreeNode{node}
		parent = node
	}
	return rootNode, fmt.Sprint(size - 1)
}

// Opening /tree over a session of N entries builds and renders one selector: the bytes grow linearly with N (one row record per entry, and the render draws one screen) and the allocation count almost does not grow. tui.tree_open pins those exponents; a pass per row (the quadratic isFoldable and visibleChildrenCount of the regression TestTreeSelectLargeNavigationFast describes) would measure 2.
func TestTreeOpenScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "tui.tree_open", treeSizes, func(size int) func() {
		root, last := chainTree(size)
		return func() {
			selector := NewTreeSelectorComponent("Session tree", root)
			selector.SetInitialCursor(last, "")
			selector.Render(120)
		}
	})
}

// One cursor move and the frame that follows it draw one screen of rows, whatever the size of the session: tui.tree_navigate pins the cost of a keystroke in /tree to the visible window, exponent 0.
func TestTreeNavigateScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "tui.tree_navigate", treeSizes, func(size int) func() {
		root, _ := chainTree(size)
		selector := NewTreeSelectorComponent("Session tree", root)
		selector.SetInitialCursor(fmt.Sprint(size/2), "")
		selector.Render(120)
		return func() {
			selector.HandleInput("\x1b[A")
			selector.Render(120)
			selector.HandleInput("\x1b[B")
			selector.Render(120)
		}
	})
}

// A typed search character and its frame allocate for the visible rows only: the filter walks the flat row list without allocating per row, so tui.tree_search pins the allocation of one search keystroke at exponent 0.
func TestTreeSearchScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "tui.tree_search", treeSizes, func(size int) func() {
		root, _ := chainTree(size)
		selector := NewTreeSelectorComponent("Session tree", root)
		selector.SetInitialCursor(fmt.Sprint(size/2), "")
		selector.Render(120)
		return func() {
			selector.HandleInput("m")
			selector.Render(120)
			selector.HandleInput("\x1b")
			selector.Render(120)
		}
	})
}

// branchingSession is a session of about size entries in branches of ten: size/10 branches under one root.
func branchingSession(size int) (root TreeNode, last string) {
	branches := max(size/10, 1)
	return branchingTree(branches, 10), fmt.Sprintf("b%dd9", branches-1)
}

// A session that forked often has foldable branch points on every screen: opening /tree over it costs the same orders as the chain, and a cursor move recomputes no per-row state.
func TestTreeOpenBranchingScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "tui.tree_open_branching", treeSizes, func(size int) func() {
		root, last := branchingSession(size)
		return func() {
			selector := NewTreeSelectorComponent("Session tree", root)
			selector.SetInitialCursor(last, "")
			selector.Render(120)
		}
	})
}

func TestTreeNavigateBranchingScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "tui.tree_navigate_branching", treeSizes, func(size int) func() {
		root, last := branchingSession(size)
		selector := NewTreeSelectorComponent("Session tree", root)
		selector.SetInitialCursor(last, "")
		selector.Render(120)
		return func() {
			selector.HandleInput("\x1b[A")
			selector.Render(120)
			selector.HandleInput("\x1b[B")
			selector.Render(120)
		}
	})
}
