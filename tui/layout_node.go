package tui

// Ports pi-tui's layout-node.ts: the contract layer shared by the layout engine
// (layout.go), the stack components (stack.go), and the scroll viewport
// (scroll_view.go). Names and structure mirror upstream 1:1; the only forced Go
// mechanics are the LAYOUT_NODE symbol becoming a method, the discriminated
// LayoutNode union becoming a sealed interface, and ScrollLayoutState's readonly
// fields becoming getter methods (a Go interface cannot carry fields).

// LayoutViewport is the available render area a stack entry's visibility
// predicate is evaluated against.
type LayoutViewport struct {
	Width  int
	Height int
}

// StackLayoutEntry is one child in a stack with flexbox sizing. The pointer
// fields are upstream's optional properties: nil means "unset", and the
// allocator applies the upstream default (Basis nil == "auto" -> intrinsic size,
// Grow 0, Shrink 1, MinSize 0, MaxSize maxSafeInteger).
type StackLayoutEntry struct {
	Component Component
	Basis     *int
	Grow      *int
	Shrink    *int
	MinSize   *int
	MaxSize   *int
	Visible   func(viewport LayoutViewport) bool
}

// StackLayoutNode is a vstack or hstack layout node. Type holds upstream's
// literal discriminant ("vstack" | "hstack"); Align holds "stretch" | "start" |
// "center" | "end".
type StackLayoutNode struct {
	Type    string
	Entries []StackLayoutEntry
	Gap     int
	Align   string
}

func (StackLayoutNode) isLayoutNode() {}

// ScrollLayoutState is the narrow contract the layout engine sees for a scroll
// node; *ScrollView implements it and is passed as the node's State. Upstream's
// readonly fields (scrollTop, primary, overscroll, viewportHeight) are getter
// methods here because a Go interface cannot carry fields.
type ScrollLayoutState interface {
	ScrollTop() int
	Primary() bool
	Overscroll() string
	ViewportHeight() int
	GetContentWidth(width int) int
	UpdateLayout(contentHeight, viewportHeight int, requestRender func())
}

// ScrollLayoutNode is a scroll layout node. State holds the narrow interface,
// not the concrete ScrollView, preserving upstream's intentional narrowing.
type ScrollLayoutNode struct {
	Component Component
	State     ScrollLayoutState
}

func (ScrollLayoutNode) isLayoutNode() {}

// LayoutNode is the sealed union of layout node kinds (upstream's
// StackLayoutNode | ScrollLayoutNode discriminated union). Only the two node
// types in this package implement it.
type LayoutNode interface {
	isLayoutNode()
}

// LayoutComponent is a Component that exposes a layout node. It ports upstream's
// LayoutComponent interface; the LAYOUT_NODE symbol method becomes LayoutNode().
type LayoutComponent interface {
	Component
	LayoutNode() LayoutNode
}

// getLayoutNode returns the component's layout node, or nil when the component
// is not a LayoutComponent (upstream's getLayoutNode over the LAYOUT_NODE
// symbol dispatch).
func getLayoutNode(component Component) LayoutNode {
	if lc, ok := component.(LayoutComponent); ok {
		return lc.LayoutNode()
	}
	return nil
}
