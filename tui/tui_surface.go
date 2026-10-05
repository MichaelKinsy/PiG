package tui

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D91): TuiSurface is a renderer Pi does not have. It keeps
// Pi's component tree and reports each frame to a Piglet frontend session as
// retained-tree ops instead of painting the terminal.

// TuiSurface draws the interactive layout through a [frontend.Session]. The
// document's leaf components become the session's main nodes and the dock
// renders as one dock node. Tool cards are reported as [frontend.Tool].
type TuiSurface struct {
	tuiBase

	session  frontend.Session
	document Component
	dock     Component
	started  bool
	// onApplyError receives a frame the session failed to draw.
	onApplyError func(error)

	ids    map[Component]string
	nextID int
	main   []surfaceEntry
	docked []surfaceEntry
	// resend replaces every node on the next frame.
	resend bool

	hasRendered           bool
	prevWidth, prevHeight int
	// prevMainCols and prevDockCols are the session widths of the last frame.
	prevMainCols, prevDockCols int
}

type surfaceEntry struct {
	id   string
	node frontend.Node
}

var _ Renderer = (*TuiSurface)(nil)

// NewTuiSurface creates a surface renderer for session at the process
// terminal's size. onApplyError, if set, receives every failed frame.
func NewTuiSurface(session frontend.Session, onApplyError func(error)) *TuiSurface {
	t := newTuiSurface(io.Discard, session, onApplyError)
	t.updateSize()
	return t
}

// NewTuiSurfaceWithSize creates a fixed-size surface renderer for tests.
func NewTuiSurfaceWithSize(session frontend.Session, cols, rows int, onApplyError func(error)) *TuiSurface {
	t := newTuiSurface(io.Discard, session, onApplyError)
	t.width, t.height, t.fixedSize = cols, rows, true
	return t
}

func newTuiSurface(out io.Writer, session frontend.Session, onApplyError func(error)) *TuiSurface {
	t := &TuiSurface{
		tuiBase: tuiBase{
			out:                out,
			terminalBackground: &terminalBackgroundQueries{},
			showHardwareCursor: os.Getenv("PI_HARDWARE_CURSOR") == "1",
			now:                time.Now,
			afterFunc: func(d time.Duration, fn func()) stoppableTimer {
				return time.AfterFunc(d, fn)
			},
		},
		session:      session,
		onApplyError: onApplyError,
		ids:          map[Component]string{},
	}
	t.render = t.doRender
	t.mountedRoots = t.getMountedRoots
	return t
}

// Session returns the frontend session the renderer draws through.
func (t *TuiSurface) Session() frontend.Session { return t.session }

// SetLayout sets the transcript document and the input dock.
func (t *TuiSurface) SetLayout(document, dock Component) {
	t.mu.Lock()
	t.document, t.dock = document, dock
	t.resend = true
	t.mu.Unlock()
	t.RequestRender()
}

func (t *TuiSurface) getMountedRoots() []Component {
	t.mu.Lock()
	defer t.mu.Unlock()
	roots := make([]Component, 0, 2)
	for _, root := range []Component{t.document, t.dock} {
		if root != nil {
			roots = append(roots, root)
		}
	}
	return roots
}

// HandleFrontendInput offers terminal input to the session. A sequence that
// changes the session's region widths repaints.
func (t *TuiSurface) HandleFrontendInput(data string) bool {
	if !t.session.HandleInput(data) {
		return false
	}
	mainCols, dockCols := t.session.Columns()
	t.mu.Lock()
	changed := t.hasRendered && (mainCols != t.prevMainCols || dockCols != t.prevDockCols)
	t.mu.Unlock()
	if changed {
		t.RequestRender()
	}
	return true
}

// Start begins rendering and draws the whole tree.
func (t *TuiSurface) Start() {
	t.mu.Lock()
	t.stopped = false
	t.started = true
	t.resend = true
	t.mu.Unlock()
	t.Render()
}

// Stop stops rendering.
func (t *TuiSurface) Stop() { t.StopWithOptions(StopOptions{}) }

// StopWithOptions stops rendering. The session owns the terminal surface, so
// PreserveScreen changes nothing.
func (t *TuiSurface) StopWithOptions(StopOptions) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	t.cancelPendingRenderLocked()
}

// ForceFullRender replaces every node on the next frame.
func (t *TuiSurface) ForceFullRender() {
	t.mu.Lock()
	t.resend = true
	t.mu.Unlock()
	t.RequestRender()
}

// RepaintAll replaces every node now.
func (t *TuiSurface) RepaintAll() {
	t.mu.Lock()
	t.resend = true
	t.mu.Unlock()
	t.Render()
}

// RenderSnapshot returns the document and dock lines at width.
func (t *TuiSurface) RenderSnapshot(width int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, root := range []Component{t.document, t.dock} {
		if root != nil {
			lines = append(lines, root.Render(width)...)
		}
	}
	return lines
}

// SetClearOnShrink has no effect: the session lays out its own document.
func (t *TuiSurface) SetClearOnShrink(bool) {}

// SetShowHardwareCursor records the setting; the session draws no hardware
// cursor.
func (t *TuiSurface) SetShowHardwareCursor(enabled bool) {
	t.mu.Lock()
	t.showHardwareCursor = enabled
	t.mu.Unlock()
}

// regionWidth caps a session's column count at the terminal width.
func regionWidth(cols, width int) int {
	if cols <= 0 || cols > width {
		return width
	}
	return cols
}

func (t *TuiSurface) doRender() {
	mainCols, dockCols := t.session.Columns()
	t.mu.Lock()
	if t.stopped || !t.started {
		t.mu.Unlock()
		return
	}
	t.lastRenderAt = t.now()
	t.updateSize()
	width, height := max(1, t.width), max(1, t.height)
	widthChanged := t.hasRendered && t.prevWidth != width
	heightChanged := t.hasRendered && t.prevHeight != height

	var ops []frontend.Op
	if t.resend {
		ops = appendRemoveAll(ops, frontend.RegionMain, t.main)
		ops = appendRemoveAll(ops, frontend.RegionDock, t.docked)
		t.main, t.docked = nil, nil
		t.resend = false
	}
	next := t.mainEntries(regionWidth(mainCols, width))
	ops, t.main = diffSurfaceRegion(ops, frontend.RegionMain, t.main, next)
	var dock []surfaceEntry
	if t.dock != nil {
		dockWidth := regionWidth(dockCols, width)
		dock = []surfaceEntry{{id: "dock", node: frontend.Lines{Lines: surfaceLines(t.composeOverlayLines(t.dock.Render(dockWidth), dockWidth, height))}}}
	}
	ops, t.docked = diffSurfaceRegion(ops, frontend.RegionDock, t.docked, dock)
	t.hasRendered = true
	t.prevWidth, t.prevHeight = width, height
	t.prevMainCols, t.prevDockCols = mainCols, dockCols
	onWidth, onHeight := t.onWidthChange, t.onHeightChange
	session, onApplyError := t.session, t.onApplyError
	t.mu.Unlock()

	if len(ops) > 0 {
		if err := session.Apply(frontend.Frame{Ops: ops}); err != nil && onApplyError != nil {
			onApplyError(err)
		}
	}
	if widthChanged && onWidth != nil {
		go onWidth(width)
	}
	if heightChanged && onHeight != nil {
		go onHeight(height)
	}
}

// mainEntries flattens the document's containers into its leaf components.
func (t *TuiSurface) mainEntries(width int) []surfaceEntry {
	if t.document == nil {
		clear(t.ids)
		return nil
	}
	var entries []surfaceEntry
	live := make(map[Component]string, len(t.ids))
	var walk func(Component)
	walk = func(c Component) {
		if container, ok := c.(*Container); ok {
			for _, child := range container.Children() {
				walk(child)
			}
			return
		}
		entries = append(entries, surfaceEntry{id: t.idFor(c, len(entries), live), node: surfaceNode(c, width)})
	}
	walk(t.document)
	// Only mounted components keep an id, so the map stays bounded.
	t.ids = live
	return entries
}

// idFor returns the stable id of a mounted component and records it in live.
// A component whose dynamic type is not comparable cannot be tracked, and a
// second mount of the same component needs its own node, so both are keyed by
// position.
func (t *TuiSurface) idFor(c Component, index int, live map[Component]string) string {
	if !reflect.TypeOf(c).Comparable() {
		return fmt.Sprintf("p%d", index)
	}
	if _, mounted := live[c]; mounted {
		return fmt.Sprintf("p%d", index)
	}
	id, ok := t.ids[c]
	if !ok {
		t.nextID++
		id = fmt.Sprintf("n%d", t.nextID)
	}
	live[c] = id
	return id
}

func surfaceNode(c Component, width int) frontend.Node {
	if tool, ok := c.(*ToolExecutionComponent); ok {
		return tool.frontendTool(width)
	}
	return frontend.Lines{Lines: surfaceLines(c.Render(width))}
}

// surfaceLines removes the cursor marker and OSC 133 zone prefixes, which only
// a terminal grid interprets.
func surfaceLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.ReplaceAll(stripOsc133ZonePrefix(line), widthx.CursorMarker, "")
	}
	return out
}

func (c *ToolExecutionComponent) frontendTool(width int) frontend.ToolCard {
	tool := frontend.ToolCard{
		Name:      c.Name,
		Arguments: c.decodedArguments(),
		Header:    widthx.StripAnsi(c.ArgsPreview),
		Output:    c.Output,
		Elapsed:   c.Elapsed,
		Expanded:  !c.Collapsed,
	}
	switch {
	case c.State == ToolStateError:
		tool.Status = frontend.ToolError
	case c.State == ToolStateDone:
		tool.Status = frontend.ToolDone
	case c.executionStarted:
		tool.Status = frontend.ToolRunning
	default:
		tool.Status = frontend.ToolPending
	}
	if c.definition != nil {
		if c.definitionDirty.Load() || c.definitionCall == nil {
			c.updateDefinition()
		}
		tool.Result = []string{}
		if c.definitionResultComponent != nil {
			tool.Result = surfaceLines(c.definitionResultComponent.Render(width))
		}
	}
	return tool
}

func appendRemoveAll(ops []frontend.Op, region frontend.Region, entries []surfaceEntry) []frontend.Op {
	for i := len(entries) - 1; i >= 0; i-- {
		ops = append(ops, frontend.Op{Kind: frontend.Remove, Region: region, ID: entries[i].id, Index: i})
	}
	return ops
}

// diffSurfaceRegion appends the ops that turn prev into next and returns next
// as the new retained state. Removed ids go first, from the end, then each
// position is updated in place, moved by remove and insert, or inserted.
func diffSurfaceRegion(ops []frontend.Op, region frontend.Region, prev, next []surfaceEntry) ([]frontend.Op, []surfaceEntry) {
	keep := make(map[string]bool, len(next))
	for _, entry := range next {
		keep[entry.id] = true
	}
	work := make([]surfaceEntry, 0, len(prev))
	for i := len(prev) - 1; i >= 0; i-- {
		if !keep[prev[i].id] {
			ops = append(ops, frontend.Op{Kind: frontend.Remove, Region: region, ID: prev[i].id, Index: i})
		}
	}
	for _, entry := range prev {
		if keep[entry.id] {
			work = append(work, entry)
		}
	}
	for i, entry := range next {
		switch {
		case i < len(work) && work[i].id == entry.id:
			if !reflect.DeepEqual(work[i].node, entry.node) {
				ops = append(ops, frontend.Op{Kind: frontend.Update, Region: region, ID: entry.id, Index: i, Node: entry.node})
			}
			work[i] = entry
			continue
		default:
			if at := slices.IndexFunc(work, func(e surfaceEntry) bool { return e.id == entry.id }); at >= 0 {
				ops = append(ops, frontend.Op{Kind: frontend.Remove, Region: region, ID: entry.id, Index: at})
				work = slices.Delete(work, at, at+1)
			}
			ops = append(ops, frontend.Op{Kind: frontend.Insert, Region: region, ID: entry.id, Index: i, Node: entry.node})
			work = slices.Insert(work, i, entry)
		}
	}
	return ops, work
}
