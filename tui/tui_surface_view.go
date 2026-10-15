package tui

import (
	"slices"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ViewSource is a component whose lines an extension's view of Pi's tui
// components describes (D107): a ui.custom overlay, a widget, the header or
// footer, or a renderer's output. FrontendView returns the structure of
// Render(width), or nil when it has none.
type ViewSource interface {
	FrontendView(width int) *frontend.View
}

// surfaceView returns c's view of lines, the lines it rendered at width, or
// nil when c has none or the view does not cover exactly those rows (an
// overlay cut at its maximum height): the lines stay authoritative.
func surfaceView(c Component, width int, lines []string) *frontend.View {
	source, ok := c.(ViewSource)
	if !ok {
		return nil
	}
	view := source.FrontendView(width)
	if view == nil || view.Root.Rows != len(lines) {
		return nil
	}
	return view
}

// FrontendView describes a titled overlay's last render (D107): the inner
// component's view inside the box drawBox drew around it. The top and bottom
// borders and any padding rows are lines ranges, and the side borders are
// one-column lines ranges beside the inner view in an hstack. It is nil when
// the inner component has no view, when the box was not drawn at width, or
// when the box cut the inner rows at its height: the lines stay authoritative.
func (m *modalOverlay) FrontendView(width int) *frontend.View {
	source, ok := m.component.(ViewSource)
	if !ok {
		return nil
	}
	if m.framed == nil {
		// Too narrow or too short for a box: the inner lines are drawn as is.
		return source.FrontendView(max(1, width))
	}
	if m.framedWidth != width {
		return nil
	}
	inner := source.FrontendView(width - 2)
	rows := 0
	if inner != nil {
		rows = inner.Root.Rows
	}
	framed := m.framed
	if inner == nil || rows > len(framed)-2 {
		return nil
	}
	border := func(lines []string) frontend.ViewNode {
		return frontend.ViewNode{Kind: frontend.ViewKindLines, Rows: len(lines), Width: lineCells(lines), Lines: surfaceLines(lines)}
	}
	side := slices.Repeat([]string{"│"}, rows)
	fixed := &frontend.ViewStack{Basis: 1, Shrink: 0, MaxSize: -1}
	left, right := border(side), border(side)
	left.Stack, right.Stack = fixed, fixed
	body := inner.Root
	body.Stack = &frontend.ViewStack{Basis: -1, Grow: 1, Shrink: 1, MaxSize: -1}
	children := []frontend.ViewNode{
		border(framed[:1]),
		{Kind: frontend.ViewKindHStack, Rows: rows, Width: width, Align: "stretch", Children: []frontend.ViewNode{left, body, right}},
	}
	if pad := framed[1+rows : len(framed)-1]; len(pad) > 0 {
		children = append(children, border(pad))
	}
	children = append(children, border(framed[len(framed)-1:]))
	return &frontend.View{
		Root:  frontend.ViewNode{Kind: frontend.ViewKindContainer, Rows: len(framed), Width: width, Children: children},
		Theme: inner.Theme, Focus: inner.Focus, Seq: inner.Seq,
	}
}

// dockPart is one dock component's lines and view, in dock order.
type dockPart struct {
	component Component
	lines     []string
	view      *frontend.View
}

// dockView combines the views of a dock blob's components (D107): nil when
// none has one, else a container of each view's root (with its overrides as
// the subtree's Theme) and a lines range for each component without one.
// Focus is the focus of the only part that has one.
func dockView(parts []dockPart) *frontend.View {
	if !slices.ContainsFunc(parts, func(p dockPart) bool { return p.view != nil }) {
		return nil
	}
	root := frontend.ViewNode{Kind: frontend.ViewKindContainer, Children: make([]frontend.ViewNode, 0, len(parts))}
	view := &frontend.View{}
	focused := 0
	for _, p := range parts {
		if p.view == nil {
			root.Children = append(root.Children, frontend.ViewNode{Kind: frontend.ViewKindLines, Rows: len(p.lines), Width: lineCells(p.lines), Lines: surfaceLines(p.lines)})
		} else {
			child := p.view.Root
			child.Theme = p.view.Theme
			root.Children = append(root.Children, child)
			view.Seq += p.view.Seq
			if p.view.Focus != "" {
				focused++
				view.Focus = p.view.Focus
			}
		}
		root.Rows += len(p.lines)
	}
	if focused != 1 {
		view.Focus = ""
	}
	for _, child := range root.Children {
		root.Width = max(root.Width, child.Width)
	}
	view.Root = root
	return view
}

func lineCells(lines []string) int {
	width := 0
	for _, line := range lines {
		width = max(width, widthx.VisibleWidth(line))
	}
	return width
}

// viewsEqual compares two views by the frame they describe: the producer's
// revision and the root's layout, not by a walk of the tree.
func viewsEqual(a, b *frontend.View) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Seq == b.Seq && a.Focus == b.Focus && a.Root.Width == b.Root.Width && a.Root.Rows == b.Root.Rows &&
		len(a.Root.Children) == len(b.Root.Children)
}

// findViewNode returns the node of root with id, or nil.
func findViewNode(root *frontend.ViewNode, id string) *frontend.ViewNode {
	if root.ID == id {
		return root
	}
	for i := range root.Children {
		if n := findViewNode(&root.Children[i], id); n != nil {
			return n
		}
	}
	return nil
}

// viewActionNode returns the focused list of view as the Selector or
// Settings node the native actions step against, with each select list
// item's Value as its ID (D107, Action.ViewNode).
func viewActionNode(view *frontend.View, id string) frontend.Node {
	if view == nil || id == "" || view.Focus != id {
		return nil
	}
	n := findViewNode(&view.Root, id)
	if n == nil {
		return nil
	}
	switch n.Kind {
	case frontend.ViewKindSelectList:
		sel := frontend.Selector{Items: make([]frontend.SelectorItem, len(n.Items)), Query: n.Query}
		for i, item := range n.Items {
			sel.Items[i] = frontend.SelectorItem{ID: item.Value, Label: item.Label, Detail: item.Description}
		}
		if n.Selected >= 0 && n.Selected < len(n.Items) {
			sel.Selected = n.Items[n.Selected].Value
		}
		return sel
	case frontend.ViewKindSettingsList:
		set := frontend.Settings{Items: make([]frontend.Setting, len(n.Items)), Query: n.Query, Searchable: n.Searchable}
		for i, item := range n.Items {
			set.Items[i] = frontend.Setting{ID: item.ID, Label: item.Label, Description: item.Description, Value: item.CurrentValue, Values: item.Values, Submenu: item.Submenu}
		}
		if n.Selected >= 0 && n.Selected < len(n.Items) {
			set.Selected = n.Items[n.Selected].ID
		}
		return set
	}
	return nil
}

// viewSourceRef is a component whose view holds a focused list, and the
// width the last frame laid it out at.
type viewSourceRef struct {
	component Component
	width     int
}

func (t *TuiSurface) recordViewSource(id string, c Component, width int) {
	if t.viewSources == nil {
		t.viewSources = map[string]viewSourceRef{}
	}
	t.viewSources[id] = viewSourceRef{component: c, width: width}
}

// dockPartsView returns the view of a dock blob, records its focused
// component for Action.ViewNode, and records its components for a click on
// the view (D91).
func (t *TuiSurface) dockPartsView(id string, parts []dockPart, width int) *frontend.View {
	view := dockView(parts)
	if view == nil {
		return nil
	}
	if view.Focus != "" {
		for _, p := range parts {
			if p.view != nil && p.view.Focus == view.Focus {
				t.recordViewSource(id, p.component, width)
			}
		}
	}
	if t.dockLayouts == nil {
		t.dockLayouts = map[string]dockLayout{}
	}
	components := make([]Component, len(parts))
	for i, p := range parts {
		components[i] = p.component
	}
	t.dockLayouts[id] = dockLayout{components: components, width: width}
	return view
}

// dockLayout is the components of a dock blob with a view, in order, and
// the width the last frame laid them out at.
type dockLayout struct {
	components []Component
	width      int
}

// dockBlobView returns the view of the whole dock drawn as one blob, lines,
// when a component in it has a view: the dock's components walked as
// dockEntries walks them, each with its lines (D91).
func (t *TuiSurface) dockBlobView(id string, lines []string, width int) *frontend.View {
	leaves := dockLeaves(nil, t.dock)
	if !slices.ContainsFunc(leaves, func(c Component) bool { _, ok := c.(ViewSource); return ok }) {
		return nil
	}
	parts := make([]dockPart, len(leaves))
	for i, c := range leaves {
		rows := c.Render(width)
		parts[i] = dockPart{component: c, lines: rows, view: surfaceView(c, width, rows)}
	}
	if dockPartsRows(parts) != len(lines) {
		return nil
	}
	return t.dockPartsView(id, parts, width)
}

// dockLeaves appends the components c draws through containers.
func dockLeaves(leaves []Component, c Component) []Component {
	container, ok := c.(*Container)
	if !ok {
		return append(leaves, c)
	}
	for _, child := range container.Children() {
		leaves = dockLeaves(leaves, child)
	}
	return leaves
}

func dockPartsRows(parts []dockPart) int {
	rows := 0
	for _, p := range parts {
		rows += len(p.lines)
	}
	return rows
}

// viewNativeNode returns the focused list a frontend action names through
// Action.ViewNode, as the last frame reported its node, now.
func (t *TuiSurface) viewNativeNode(action frontend.Action) frontend.Node {
	ref, ok := t.viewSources[action.Node]
	if !ok {
		return nil
	}
	source, ok := ref.component.(ViewSource)
	if !ok {
		return nil
	}
	return viewActionNode(source.FrontendView(ref.width), action.ViewNode)
}
