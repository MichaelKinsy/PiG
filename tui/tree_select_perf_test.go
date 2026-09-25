package tui

import (
	"fmt"
	"testing"
	"time"
)

// TestTreeSelectLargeNavigationFast guards against the O(n^2)/O(n^3)
// regression where isFoldable rebuilt O(n) maps per row and
// visibleChildrenCount looped all rows calling it, so each Render over a
// large session did hundreds of millions of operations and /tree became
// unresponsive (the user's 2446-entry session). With the foldableCache
// computed once per recomputeVisible, a Render plus a sweep of cursor
// moves must complete well under this generous bound.
func TestTreeSelectLargeNavigationFast(t *testing.T) {
	// Build a wide+deep tree: a root with many branch points, each with
	// a chain of children: exercises the visible-parent / child-count
	// paths that were quadratic.
	const branches = 60
	const depth = 40 // 60*40 = 2400 nodes, ~the reported session size
	kids := make([]TreeNode, branches)
	for b := range branches {
		// deepest-first chain build
		var node *fakeNode
		for d := depth - 1; d >= 0; d-- {
			id := fmt.Sprintf("b%dd%d", b, d)
			n := &fakeNode{id: id, label: id}
			if node != nil {
				n.kids = []TreeNode{node}
			}
			node = n
		}
		kids[b] = node
	}
	root := &fakeNode{id: "root", kids: kids}

	start := time.Now()
	ts := NewTreeSelect("big", root)
	if len(ts.rows) < branches*depth {
		t.Fatalf("expected >= %d rows, got %d", branches*depth, len(ts.rows))
	}

	// One full render + a sweep of down moves (each triggers a render in
	// the host loop). Render exercises isFoldable for the visible window.
	_ = ts.Render(100)
	for range 200 {
		ts.HandleInput("\x1b[B") // down
		_ = ts.Render(100)
	}
	elapsed := time.Since(start)

	// The old code took tens of seconds at this size. 5s is generous
	// enough to never flake yet fails hard on a quadratic regression.
	if elapsed > 5*time.Second {
		t.Fatalf("large tree navigation too slow: %v (quadratic regression?)", elapsed)
	}
}
