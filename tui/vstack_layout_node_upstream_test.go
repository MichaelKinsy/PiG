package tui

import "testing"

// Pi components/v-stack.ts: VStack inherits Stack[LAYOUT_NODE](), which reports a "vstack" node carrying its entries; getLayoutNode finds it on a VStack.
func TestVStackExposesAVStackLayoutNode(t *testing.T) {
	v := NewVStack(nil, StackOptions{})
	node, ok := getLayoutNode(v).(StackLayoutNode)
	if !ok || node.Type != "vstack" {
		t.Fatalf("layout node = %#v, want a vstack StackLayoutNode", getLayoutNode(v))
	}
	var component LayoutComponent = v
	if component.LayoutNode() == nil {
		t.Fatal("VStack must satisfy LayoutComponent")
	}
}
