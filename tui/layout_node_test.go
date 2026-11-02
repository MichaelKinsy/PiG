package tui

import (
	"reflect"
	"testing"
)

// plainLayoutStub implements Component but not LayoutComponent.
type plainLayoutStub struct{}

func (plainLayoutStub) Render(width int) []string { return nil }
func (plainLayoutStub) Invalidate()               {}

// layoutComponentStub implements LayoutComponent.
type layoutComponentStub struct {
	node LayoutNode
}

func (layoutComponentStub) Render(width int) []string { return nil }
func (layoutComponentStub) Invalidate()               {}
func (s layoutComponentStub) LayoutNode() LayoutNode  { return s.node }

// Compile-time proof both node kinds satisfy the sealed union.
var (
	_ LayoutNode = StackLayoutNode{}
	_ LayoutNode = ScrollLayoutNode{}
)

func TestGetLayoutNodeReturnsNodeForLayoutComponent(t *testing.T) {
	want := StackLayoutNode{Type: "vstack", Gap: 1, Align: "stretch"}
	got := getLayoutNode(layoutComponentStub{node: want})
	node, ok := got.(StackLayoutNode)
	if !ok {
		t.Fatalf("getLayoutNode returned %T, want StackLayoutNode", got)
	}
	if !reflect.DeepEqual(node, want) {
		t.Fatalf("getLayoutNode = %+v, want %+v", node, want)
	}
}

func TestGetLayoutNodeReturnsNilForPlainComponent(t *testing.T) {
	if got := getLayoutNode(plainLayoutStub{}); got != nil {
		t.Fatalf("getLayoutNode(plain) = %v, want nil", got)
	}
}
