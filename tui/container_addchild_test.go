package tui

import (
	"slices"
	"testing"
)

// Pi packages/tui/src/tui.ts:351-364 (Container.addChild appends,
// removeChild removes the first identical child and ignores an absent one).
func TestContainerAddChildRemoveChildMatchUpstream(t *testing.T) {
	a, b := NewText("a"), NewText("b")
	c := NewContainer()
	c.AddChild(a)
	c.AddChild(b)
	c.AddChild(a)
	if got := c.Children(); !slices.Equal(got, []Component{a, b, a}) {
		t.Fatalf("children after addChild = %v", got)
	}
	c.RemoveChild(a)
	if got := c.Children(); !slices.Equal(got, []Component{b, a}) {
		t.Fatalf("removeChild removed the first match only, got %v", got)
	}
	c.RemoveChild(NewText("absent"))
	if c.ChildCount() != 2 {
		t.Fatalf("removing an absent child changed the children: %d", c.ChildCount())
	}
}
