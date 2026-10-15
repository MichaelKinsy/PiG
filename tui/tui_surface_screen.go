package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// pig additive (D91): a frontend session that cannot report where the pointer
// is clicks the part of a node that takes clicks, and one that can cover its
// view shows fullscreen overlays. Pi has neither; with no frontend session
// nothing here runs.

// Clickable is a component that takes a click on part of the lines it draws,
// which a TuiSurface reports as its node's Click area.
type Clickable interface {
	// ClickArea reports the cells of the lines last rendered that take a
	// click, and false when none do.
	ClickArea() (frontend.Area, bool)
}

// Screen reports the screen a fullscreen overlay covers, its size counted
// from the terminal's where the session gives zero or less, and false when
// the session is not a [frontend.ScreenSession] or cannot show one now. Call
// it on the owner loop.
func (t *TuiSurface) Screen() (frontend.Screen, bool) {
	session, ok := t.session.(frontend.ScreenSession)
	if !ok {
		return frontend.Screen{}, false
	}
	screen, ok := session.Screen()
	if !ok {
		return frontend.Screen{}, false
	}
	if screen.Columns <= 0 {
		screen.Columns = max(1, t.Width()+screen.Columns)
	}
	if screen.Rows <= 0 {
		screen.Rows = max(1, t.Height()+screen.Rows)
	}
	return screen, true
}

// ScreenLines draws the screen a fullscreen overlay covers as the session
// shows it: the dock's lines at the bottom, unless the session shows the
// dock itself, and above them the document's
// end, or, while a click is dispatched, the document from the clicked
// component when the end would leave it out of view. It reports nil when
// the session shows no screen. Call it on the owner loop.
func (t *TuiSurface) ScreenLines() []string {
	if t.clickScreen != nil {
		return slices.Clone(t.clickScreen)
	}
	screen, ok := t.Screen()
	if !ok {
		return nil
	}
	lines, _ := t.composeScreen(screen, nil)
	return lines
}

// composeScreen draws the screen with anchor, a component of the document or
// nil, in view, and reports the screen row of anchor's first line (-1 when it
// is not in the document).
func (t *TuiSurface) composeScreen(screen frontend.Screen, anchor Component) ([]string, int) {
	mainCols, dockCols := t.session.Columns()
	t.mu.Lock()
	document, dock := t.document, t.dock
	width := max(1, t.width)
	t.mu.Unlock()
	at := -1
	var doc, docked []string
	if document != nil {
		doc = surfaceLines(appendScreenLines(nil, document, regionWidth(mainCols, width), anchor, &at))
	}
	if dock != nil && !screen.ShowsDock {
		docked = surfaceLines(dock.Render(regionWidth(dockCols, width)))
	}
	rows := screen.Rows
	docked = docked[max(0, len(docked)-rows):]
	top := max(0, screen.MainRow)
	room := max(0, rows-len(docked)-top)
	start := max(0, len(doc)-room)
	if at >= 0 && at < start {
		start = at
	}
	lines := make([]string, rows)
	for i := range room {
		if start+i >= len(doc) {
			break
		}
		lines[top+i] = indent(screen.MainColumn, doc[start+i])
	}
	for i, line := range docked {
		lines[rows-len(docked)+i] = indent(screen.DockColumn, line)
	}
	row := -1
	if at >= start && at < start+room {
		row = top + at - start
	}
	return lines, row
}

// appendScreenLines appends the lines component c renders at width and
// records in at where anchor's start, until it is found. A container is
// walked through its children, which render what it renders, unless it
// caps its lines.
func appendScreenLines(lines []string, c Component, width int, anchor Component, at *int) []string {
	if c == anchor {
		*at = len(lines)
	}
	if container, ok := c.(*Container); ok && anchor != nil && *at < 0 && container.maxLines.Load() == 0 {
		container.mu.RLock()
		children := slices.Clone(container.children)
		container.mu.RUnlock()
		for _, child := range children {
			lines = appendScreenLines(lines, child, width, anchor, at)
		}
		return lines
	}
	return append(lines, c.Render(width)...)
}

func indent(columns int, line string) string {
	if columns <= 0 {
		return line
	}
	return strings.Repeat(" ", columns) + line
}

// clickArea reports c's Click area, or nil.
func clickArea(c Component) *frontend.Area {
	clickable, ok := c.(Clickable)
	if !ok {
		return nil
	}
	area, ok := clickable.ClickArea()
	if !ok || area.Rows <= 0 || area.Columns <= 0 {
		return nil
	}
	return &area
}

func areasEqual(a, b *frontend.Area) bool {
	return a == b || (a != nil && b != nil && *a == *b)
}

func inArea(area *frontend.Area, row, column int) bool {
	return area != nil && row >= area.Row && row < area.Row+area.Rows && column >= area.Column && column < area.Column+area.Columns
}

// Click hands a session's click to the component that drew the node: an
// extension's overlay or dock component as the terminal's click at that cell
// (D91), a main node's component inside its Click area, with the screen
// cell it shows at when the session covers its view, or a fullscreen
// overlay's component. While the component handles it, ScreenLines draws
// the screen with the component in view. Call it on the owner loop.
func (t *TuiSurface) Click(click frontend.Click) {
	if t.clickExtension(click) {
		return
	}
	if comp, ok := t.clickedOverlay(click.Node); ok {
		if handler, ok := comp.(MouseHandler); ok {
			handler.HandleMouse(TuiMouseEvent{
				Type: MouseClick, Button: MouseButtonLeft, ClickCount: 1,
				X: click.Column, Y: click.Row, ScreenX: click.Column, ScreenY: click.Row,
			})
		}
		return
	}
	t.mu.Lock()
	var comp Component
	height := 0
	if t.started && !t.stopped {
		for c, state := range t.ids {
			if state.id == click.Node && state.mounted {
				comp, height = c, len(state.rendered)
				break
			}
		}
	}
	t.mu.Unlock()
	handler, ok := comp.(MouseHandler)
	if !ok || !inArea(clickArea(comp), click.Row, click.Column) {
		return
	}
	event := TuiMouseEvent{
		Type: MouseClick, Button: MouseButtonLeft, ClickCount: 1,
		X: click.Column, Y: click.Row, ScreenX: click.Column, ScreenY: click.Row,
		Width: t.mainRenderWidth(), Height: height,
	}
	if screen, ok := t.Screen(); ok {
		var row int
		t.clickScreen, row = t.composeScreen(screen, comp)
		if row >= 0 {
			event.ScreenX, event.ScreenY = screen.MainColumn+click.Column, row+click.Row
		}
		defer func() { t.clickScreen = nil }()
	}
	handler.HandleMouse(event)
}

// mainRenderWidth is the width main's lines render at.
func (t *TuiSurface) mainRenderWidth() int {
	mainCols, _ := t.session.Columns()
	t.mu.Lock()
	defer t.mu.Unlock()
	return regionWidth(mainCols, max(1, t.width))
}

// clickedOverlay returns the component of the fullscreen overlay with node
// id, which takes a click anywhere. An overlay is fullscreen only while the
// session shows a screen; it reports true for every overlay id.
func (t *TuiSurface) clickedOverlay(id string) (Component, bool) {
	rest, ok := strings.CutPrefix(id, "overlay.")
	if !ok {
		return nil, false
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	if _, screen := t.Screen(); err != nil || !screen {
		return nil, true
	}
	snapshot := t.overlaySnapshot()
	for i := range snapshot.Entries {
		entry := &snapshot.Entries[i]
		if uint64(entry.id) == n && entry.visible() && entry.opts.Fullscreen {
			return entry.component, true
		}
	}
	return nil, true
}
