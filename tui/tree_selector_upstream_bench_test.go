package tui

import (
	"fmt"
	"testing"
)

func BenchmarkTreeSelectorUpstreamPath(b *testing.B) {
	for _, size := range []int{100, 10000} {
		root := &fakeNodeTagged{id: "root"}
		parent := root
		for i := range size {
			node := &fakeNodeTagged{id: fmt.Sprint(i), label: fmt.Sprintf("message %d", i)}
			if i%2 == 0 {
				node.tags = []string{"user"}
			}
			parent.kids = []TreeNode{node}
			parent = node
		}
		b.Run(fmt.Sprintf("construct/%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				selector := NewTreeSelectorComponent("Session tree", root)
				selector.SetInitialCursor(fmt.Sprint(size-1), "")
				selector.Render(120)
			}
		})
		b.Run(fmt.Sprintf("navigate/%d", size), func(b *testing.B) {
			selector := NewTreeSelectorComponent("Session tree", root)
			selector.SetInitialCursor(fmt.Sprint(size/2), "")
			selector.Render(120)
			b.ReportAllocs()
			for b.Loop() {
				selector.HandleInput("\x1b[A")
				selector.Render(120)
				selector.HandleInput("\x1b[B")
				selector.Render(120)
			}
		})
	}
}
