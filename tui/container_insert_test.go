package tui

import "testing"

func TestContainerInsertBefore(t *testing.T) {
	a, b, x := NewText("a"), NewText("b"), NewText("x")
	c := NewContainer(a, b)
	if !c.InsertBefore(b, x) {
		t.Fatal("InsertBefore reported the reference missing")
	}
	if got := c.Children(); len(got) != 3 || got[0] != a || got[1] != x || got[2] != b {
		t.Fatalf("children = %v", got)
	}
	if c.InsertBefore(NewText("gone"), NewText("y")) || len(c.Children()) != 3 {
		t.Fatal("a missing reference must change nothing")
	}
}
