package tui

import (
	"slices"
	"testing"
)

func TestContainerChildrenSnapshot(t *testing.T) {
	container := NewContainer()
	if got := container.Children(); len(got) != 0 {
		t.Fatalf("empty children=%v", got)
	}
	first, second := NewText("first"), NewText("second")
	container.Add(first)
	container.Add(second)
	children := container.Children()
	if !slices.Equal(children, []Component{first, second}) {
		t.Fatal("snapshot lost insertion order or child identity")
	}
	children[0] = second
	if !slices.Equal(container.Children(), []Component{first, second}) {
		t.Fatal("snapshot aliases container storage")
	}
	container.Remove(first)
	if !slices.Equal(container.Children(), []Component{second}) {
		t.Fatal("snapshot retains removed child")
	}
	container.Clear()
	if got := container.Children(); len(got) != 0 {
		t.Fatal("snapshot retains cleared children")
	}
}
