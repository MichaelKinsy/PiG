package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D91): a frontend session that draws an extension's view
// natively reports a click on it, and PiG hands it to the extension's
// component as the terminal's click at the cell the clicked element
// occupies in Pi's fullscreen layout, which PiG knows because its terminal
// lines are Pi's. The component cannot tell the session from a terminal.
// Pi has no frontend; with none, nothing here runs.

// mouseTarget is a component a click reaches, the screen cell of the first
// cell of its node's lines, and the lines' bounds.
type mouseTarget struct {
	component Component
	// row and column are the screen cell of the node's first cell; top is
	// the node row the component's first line is on.
	row, column, top int
	width, height    int
	// lines are the component's lines, from its first.
	lines []string
	// nodeRows are the rows of the node's lines.
	nodeRows int
	view     *frontend.View
}

// clickExtension hands a click on an extension's overlay or on a dock blob
// with a view to the component under it, and reports whether the click named
// one. A fullscreen overlay, PiG's own, is left to Click.
func (t *TuiSurface) clickExtension(click frontend.Click) bool {
	row := click.Row
	if click.ViewPath != nil {
		row = -1 // the view names the row
	}
	target, ok := t.extensionMouseTarget(click.Node, row)
	if !ok {
		return false
	}
	if t.hooks.Mouse == nil || !t.hooks.Mouse() {
		// A terminal outside fullscreen mode reports no mouse.
		return true
	}
	cell, ok := resolveViewClick(target.view, click, target.nodeRows, target.width)
	if !ok {
		return true
	}
	if click.ViewPath != nil {
		if target, ok = t.extensionMouseTarget(click.Node, cell.row); !ok {
			return true
		}
	}
	event := func(row, column int) TuiMouseEvent {
		local := row - target.top
		return TuiMouseEvent{
			Button: MouseButtonLeft, X: column, Y: local,
			ScreenX: target.column + column, ScreenY: target.row + row,
			Width: target.width, Height: target.height,
		}
	}
	for range max(cell.wheel, -cell.wheel) {
		wheel := event(cell.wheelRow, cell.wheelColumn)
		wheel.Type, wheel.Button, wheel.WheelDelta = MouseWheel, MouseButtonNone, 1
		if cell.wheel < 0 {
			wheel.WheelDelta = -1
		}
		DispatchMouseEvent(target.component, wheel)
	}
	at := event(cell.row, cell.column)
	if at.ClickCount = t.sessionClickCount(target.component, at.ScreenX, at.ScreenY, click.Count); at.ClickCount > 0 {
		var line string
		if at.Y >= 0 && at.Y < len(target.lines) {
			line = target.lines[at.Y]
		}
		clickGesture(target.component, at, selectionClicks(line, at.X, at.ScreenX, at.ClickCount))
	}
	return true
}

// sessionClickCount counts a session's click at screen cell x, y of
// component c as Pi counts a terminal's (getComponentClickCount): the click
// after one on the same component and cell, within the double-click
// interval, is its second, then its third. A click the session counted
// itself (want > 0) counts want, or 0 when the click counted last there
// already reached want: the session reports again a click that arrived.
// Call it on the owner loop.
func (t *TuiSurface) sessionClickCount(c Component, x, y, want int) int {
	now := t.now()
	previous := t.lastSessionClick
	count := 1
	if previous != nil && now.Sub(previous.timestamp) <= altDoubleClickInterval &&
		previous.component == c && previous.x == x && previous.y == y {
		if want > 0 && previous.count >= want {
			return 0
		}
		count = previous.count%3 + 1
	}
	if want > 0 {
		count = want
	}
	t.lastSessionClick = &componentClick{timestamp: now, count: count, component: c, x: x, y: y}
	return count
}

// selectionClicks reports whether Pi's text selection reports a click of
// count at column of line, which shows at screen column screenX
// (handleSelectionRelease): a release without a drag is a click when it is
// on the selection's anchor, which a double click puts at the start of the
// word under it and a triple click at the start of the screen's line.
// The word is read from the component's line: a frontend does not draw
// Pi's screen beside an overlay, so a word that runs into the overlay's
// first cell from the screen beside it is not seen (D91 records the limit).
func selectionClicks(line string, column, screenX, count int) bool {
	switch count {
	case 2:
		segments := wordSegments(widthx.StripTerminalSequences(line))
		at := slices.IndexFunc(segments, func(s wordSegment) bool { return column >= s.start && column < s.end })
		if at < 0 {
			return false
		}
		for at > 0 && segments[at-1].selectable && segments[at].selectable && (segments[at-1].joiner || segments[at].joiner) {
			at--
		}
		return segments[at].start == column
	case 3:
		return screenX == 0
	}
	return true
}

// clickGesture delivers a stationary left click with event's ClickCount as
// Pi's fullscreen renderer does (tui-alt-screen.ts handleMouseEvent,
// handleSelectionMouseEvent): the press to c; when a component takes it,
// the release and then the click to that component; otherwise the release
// to c, as to whatever is under the pointer, and, when c leaves it too and
// the text selection the press started reports a click (selected), the
// click.
func clickGesture(c Component, event TuiMouseEvent, selected bool) {
	count := event.ClickCount
	event.Type, event.ClickCount = MousePress, 0
	result := DispatchMouseEvent(c, event)
	release := event
	release.Type = MouseRelease
	event.Type, event.ClickCount = MouseClick, count
	if result == nil {
		if DispatchMouseEvent(c, release) == nil && selected {
			DispatchMouseEvent(c, event)
		}
		return
	}
	DispatchMouseEvent(result.Target.Component, RetargetMouseEvent(release, result.Target))
	DispatchMouseEvent(result.Target.Component, RetargetMouseEvent(event, result.Target))
}

// extensionMouseTarget returns the component that drew row of node id, as
// the last frame laid it out: an overlay's (other than a fullscreen one) or
// a dock blob's component with a view.
func (t *TuiSurface) extensionMouseTarget(id string, row int) (mouseTarget, bool) {
	t.mu.Lock()
	width, height := regionWidth(t.prevDockCols, max(1, t.prevWidth)), max(1, t.prevHeight)
	layout, docked := t.dockLayouts[id]
	t.mu.Unlock()
	if rest, ok := strings.CutPrefix(id, "overlay."); ok {
		n, err := strconv.ParseUint(rest, 10, 64)
		if err != nil {
			return mouseTarget{}, false
		}
		snapshot := t.overlaySnapshot()
		for i := range snapshot.Entries {
			entry := &snapshot.Entries[i]
			if uint64(entry.id) != n || !entry.visible() || entry.opts.Fullscreen {
				continue
			}
			initial := resolveOverlayLayout(entry.opts, 0, width, height)
			lines := renderOverlayEntry(entry, initial.width, initial.maxHeight, initial.hasMaxHeight)
			final := resolveOverlayLayout(entry.opts, len(lines), width, height)
			return mouseTarget{
				component: entry.component, row: final.row, column: final.col, width: final.width, height: len(lines),
				lines: lines, nodeRows: len(lines), view: surfaceView(entry.component, final.width, lines),
			}, true
		}
		return mouseTarget{}, false
	}
	if !docked || t.dock == nil {
		return mouseTarget{}, false
	}
	// Pi's fullscreen layout puts the dock at the bottom of the screen; a
	// component's first line is its row among the dock's.
	dockRows := 0
	start := -1
	var component Component
	var componentLines []string
	nodeTop, nodeRow := 0, 0
	parts := make([]dockPart, len(layout.components))
	for i, c := range layout.components {
		lines := c.Render(layout.width)
		parts[i] = dockPart{component: c, lines: lines, view: surfaceView(c, layout.width, lines)}
		if component == nil && row >= nodeRow && row < nodeRow+len(lines) {
			component, componentLines, nodeTop = c, lines, nodeRow
		}
		nodeRow += len(lines)
	}
	for _, c := range dockLeaves(nil, t.dock) {
		if c == component {
			start = dockRows
		}
		dockRows += len(c.Render(layout.width))
	}
	if row < 0 {
		// Only the view is asked for.
		return mouseTarget{width: layout.width, nodeRows: nodeRow, view: dockView(parts)}, true
	}
	if component == nil || start < 0 {
		return mouseTarget{}, false
	}
	return mouseTarget{
		component: component, row: height - dockRows + start - nodeTop, column: 0, top: nodeTop,
		width: layout.width, height: len(componentLines), lines: componentLines, nodeRows: nodeRow, view: dockView(parts),
	}, true
}

// viewCell is the node cell a click lands on, and the wheel steps (each one
// line, negative up) at the wheel cell that bring a list item into view
// first.
type viewCell struct {
	row, column           int
	wheel                 int
	wheelRow, wheelColumn int
}

// resolveViewClick returns the cell of a node's lines, rows by columns, that
// click names: its Row and Column, or, with a ViewPath, the cell of the view
// node it names.
func resolveViewClick(view *frontend.View, click frontend.Click, rows, columns int) (viewCell, bool) {
	if click.ViewPath == nil {
		inside := click.Row >= 0 && click.Row < rows && click.Column >= 0 && click.Column < columns
		return viewCell{row: click.Row, column: click.Column}, inside
	}
	if view == nil {
		return viewCell{}, false
	}
	node, row, column, ok := viewNodeAt(&view.Root, click.ViewPath)
	if !ok || node.Rows <= 0 {
		return viewCell{}, false
	}
	at := func(r, c int) (int, int) {
		return row + min(max(r, 0), node.Rows-1), column + min(max(c, 0), max(node.Width-1, 0))
	}
	if click.Item == "" {
		r, c := at(click.Row, click.Column)
		return viewCell{row: r, column: c}, true
	}
	index, offset := -1, 0
	switch node.Kind {
	case frontend.ViewKindSelectList:
		index = slices.IndexFunc(node.Items, func(item frontend.ViewItem) bool { return item.Value == click.Item })
	case frontend.ViewKindSettingsList:
		if node.Submenu != nil {
			return viewCell{}, false
		}
		index = slices.IndexFunc(node.Items, func(item frontend.ViewItem) bool { return item.ID == click.Item })
		if node.Searchable {
			offset = 2 // the search input and the blank row under it
		}
	}
	if index < 0 {
		return viewCell{}, false
	}
	selected, visible := max(node.Selected, 0), max(node.MaxVisible, 1)
	start := func(selected int) int { return max(0, min(selected-visible/2, len(node.Items)-visible)) }
	// The wheel moves the selection one item a step (SelectList and
	// SettingsList handleMouse), and the window follows it.
	wheel := 0
	for index < start(selected) || index >= start(selected)+visible {
		if index < selected {
			selected--
			wheel--
		} else {
			selected++
			wheel++
		}
	}
	r, c := at(offset+index-start(selected), click.Column)
	wr, wc := at(offset, 0)
	return viewCell{row: r, column: c, wheel: wheel, wheelRow: wr, wheelColumn: wc}, true
}

// viewNodeAt returns the node at path under root and the cell of its first
// line in root's lines, laid out as [frontend.ViewNode] describes.
func viewNodeAt(root *frontend.ViewNode, path []int) (*frontend.ViewNode, int, int, bool) {
	node, row, column := root, 0, 0
	for _, i := range path {
		if i < 0 || i >= len(node.Children) {
			return nil, 0, 0, false
		}
		r, c := childOffset(node, i)
		row, column = row+r, column+c
		node = &node.Children[i]
	}
	return node, row, column, true
}

// childOffset returns the cell child i of parent starts at in parent's lines.
func childOffset(parent *frontend.ViewNode, i int) (row, column int) {
	children := parent.Children
	switch parent.Kind {
	case frontend.ViewKindHStack:
		for _, child := range children[:i] {
			column += child.Width + parent.Gap
		}
		switch parent.Align {
		case "center":
			row = (parent.Rows - children[i].Rows) / 2
		case "end":
			row = parent.Rows - children[i].Rows
		}
		return max(row, 0), column
	case frontend.ViewKindVStack:
		for _, child := range children[:i] {
			row += stackSlot(child) + parent.Gap
		}
		return row, 0
	case frontend.ViewKindBox:
		row, column = parent.PaddingY, parent.PaddingX
	}
	for _, child := range children[:i] {
		row += child.Rows
	}
	return row, column
}

// stackSlot is the rows a vstack gives child: its own, or its basis,
// within its minimum and maximum (allocateStackSizes without an available
// size).
func stackSlot(child frontend.ViewNode) int {
	size := child.Rows
	st := child.Stack
	if st == nil {
		return size
	}
	if st.Basis >= 0 {
		size = st.Basis
	}
	low := max(0, st.MinSize)
	high := st.MaxSize
	if high < 0 {
		return max(low, size)
	}
	return max(low, min(max(low, high), size))
}
