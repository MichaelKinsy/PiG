package tui

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// These tests hold TuiSurface.Wheel on an extension's view (D91) to Pi's
// fullscreen renderer for a wheel over a component
// (tui-alt-screen.ts:719-733: a wheel event is offered to the component under
// the pointer first): the same wheel turn on the same element, made once by a
// session's Wheel through a ViewPath and once by a terminal's SGR wheel report
// at the cell the element shows at on the alternate screen, hands the
// component the same events at the same local cells with the same wheelDelta
// (negative up, the unit of TuiMouseEvent.WheelDelta).

// wheelCase is a mouseCase whose element is wheeled instead of clicked.
type wheelCase struct {
	mouseCase
	// delta is the session's Wheel.Delta; the terminal side wheels as many
	// lines per report.
	delta int
	// row and wcolumn are the cell of the wheeled node the session names.
	row, wcolumn int
}

// terminalWheel wheels c's element on a terminal in Pi's fullscreen mode:
// one report whose logical line count is |delta|, down for a positive delta.
func terminalWheel(t *testing.T, c wheelCase) (mouseLog, *kitProbe) {
	t.Helper()
	h := newAltHarness(t, mouseCols, mouseRows, TuiAltScreenOptions{})
	h.tui.SetWheelScrollLines(WheelScrollLines{Lines: float64(max(c.delta, -c.delta))})
	probe := newKitProbe(c.spec, c.takes)
	var dock []StackChild
	if c.dock {
		dock = []StackChild{{Component: NewText("status")}, {Component: probe}, {Component: NewText("footer")}}
	} else {
		dock = []StackChild{{Component: NewText("footer")}}
	}
	h.tui.SetLayoutRoot(NewVStack([]StackChild{
		{Component: NewScrollView(NewText("doc"), ScrollViewOptions{Follow: "end", Primary: true}), Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)},
		{Component: NewVStack(dock, StackOptions{}), Grow: new(0), Shrink: new(1), MinSize: new(1)},
	}, StackOptions{}))
	h.start()
	if !c.dock {
		h.tui.ShowOverlay(probe, c.opts)
	}
	h.render()
	row, col, ok := findOnScreen(h.viewport(), c.marker)
	if !ok {
		t.Fatalf("terminal: no %q on %q", c.marker, h.viewport())
	}
	button := 65 // wheel down
	if c.delta < 0 {
		button = 64
	}
	h.send(sgr(button, col, row, false))
	return mouseLog{events: probe.events, log: probe.log}, probe
}

// surfaceNode lays c's view out on a surface and returns the surface, the
// probe, the node that holds the view and the ViewPath of the wheeled
// element in it.
func surfaceNode(t *testing.T, c mouseCase, mouse func() bool) (*TuiSurface, *kitProbe, string, []int) {
	t.Helper()
	probe := newKitProbe(c.spec, c.takes)
	dock := NewContainer(NewText("footer"))
	if c.dock {
		dock = NewContainer(NewText("status"), probe, NewText("footer"))
	}
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, mouseCols, mouseRows, func(err error) { t.Fatalf("apply: %v", err) })
	surface.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	surface.SetLayout(NewContainer(NewText("doc")), dock)
	surface.Start()
	surface.SetHooks(SurfaceHooks{Mouse: mouse})
	region := frontend.RegionDock
	if !c.dock {
		surface.ShowOverlay(probe, c.opts)
		region = frontend.RegionOverlay
	}
	surface.Render()
	node := ""
	for _, entry := range session.tree[region] {
		switch n := entry.node.(type) {
		case frontend.Lines:
			if n.View != nil {
				node = entry.id
			}
		case frontend.Overlay:
			if n.View != nil {
				node = entry.id
			}
		}
	}
	if node == "" {
		t.Fatalf("surface: no node with a view in %v", session.tree[region])
	}
	path := slices.Clone(c.path)
	if c.dock {
		// The dock's view holds the dock's lines around the component's view:
		// the status line is child 0.
		path = append([]int{1}, path...)
	}
	return surface, probe, node, path
}

func surfaceWheel(t *testing.T, c wheelCase, mouse func() bool) (mouseLog, *kitProbe) {
	t.Helper()
	surface, probe, node, path := surfaceNode(t, c.mouseCase, mouse)
	surface.Wheel(frontend.Wheel{Node: node, ViewPath: path, Row: c.row, Column: c.wcolumn, Delta: c.delta})
	surface.Render()
	return mouseLog{events: probe.events, log: probe.log}, probe
}

func wheelCases() map[string]wheelCase {
	list := kitSpec{kind: frontend.ViewKindSelectList, id: "tracks", items: labels("it", 20), maxVisible: 5}
	return map[string]wheelCase{
		"overlay takes":  {mouseCase: mouseCase{spec: marked("@@"), takes: true, path: []int{1, 0}, marker: "@@"}, delta: 1},
		"overlay leaves": {mouseCase: mouseCase{spec: marked("@@"), path: []int{1, 0}, marker: "@@"}, delta: -1},
		"dock takes":     {mouseCase: mouseCase{spec: marked("@@"), takes: true, dock: true, path: []int{1, 0}, marker: "@@"}, delta: 3},
		"dock leaves":    {mouseCase: mouseCase{spec: marked("@@"), dock: true, path: []int{1, 0}, marker: "@@"}, delta: -3},
		// A select list moves its selection one item per wheel line; the
		// first item row is the list's row 0 and its text starts after the
		// two cells of the selection prefix.
		"list down":      {mouseCase: mouseCase{spec: listView(list), path: []int{1}, marker: "it00"}, delta: 1, wcolumn: 2},
		"list up":        {mouseCase: mouseCase{spec: listView(list), path: []int{1}, marker: "it00"}, delta: -1, wcolumn: 2},
		"dock list down": {mouseCase: mouseCase{spec: listView(list), dock: true, path: []int{1}, marker: "it00"}, delta: 3, wcolumn: 2},
	}
}

// A wheel over an extension's overlay, dock view or list reaches the
// component with the event, the cell and the wheelDelta a terminal's wheel
// report gives it (tui-alt-screen.ts:719-733).
func TestTuiSurfaceWheelMatchesTheTerminalWheelOverAComponent(t *testing.T) {
	for name, c := range wheelCases() {
		t.Run(name, func(t *testing.T) {
			terminal, _ := terminalWheel(t, c)
			surface, _ := surfaceWheel(t, c, func() bool { return true })
			if len(terminal.events) != 1 || terminal.events[0].Type != MouseWheel || terminal.events[0].WheelDelta != c.delta {
				t.Fatalf("terminal events = %+v, want one wheel of %d", terminal.events, c.delta)
			}
			if !reflect.DeepEqual(surface, terminal) {
				t.Fatalf("surface wheel differs from the terminal's:\nsurface  %+v\nterminal %+v", surface, terminal)
			}
		})
	}
}

// The wheel moves a list's selection as it does on a terminal, one item for
// a wheel of any size (SelectList.HandleMouse), so a session scrolls a native
// view of an extension's list through the extension's own component and
// leaves the meaning of Delta to it.
func TestTuiSurfaceWheelMovesAnExtensionsListSelection(t *testing.T) {
	c := wheelCases()["list down"]
	c.delta = 3
	got, _ := surfaceWheel(t, c, func() bool { return true })
	want := []string{"selectionChange:1:vit01"}
	if !slices.Equal(got.log, want) {
		t.Fatalf("selection log = %q, want %q", got.log, want)
	}
}

// A wheel that names no extension view, a cell outside its node, a Delta of
// 0, or a run outside fullscreen mode (a terminal there reports no mouse)
// hands nothing to any component.
func TestTuiSurfaceWheelDoesNothingWithoutAViewUnderIt(t *testing.T) {
	c := wheelCases()["overlay takes"]
	yes := func() bool { return true }
	for _, tc := range []struct {
		name  string
		mouse func() bool
		wheel func(node string, path []int) frontend.Wheel
	}{
		{"unknown node", yes, func(string, []int) frontend.Wheel { return frontend.Wheel{Node: "nope", Row: 0, Delta: 1} }},
		{"row below the lines", yes, func(node string, _ []int) frontend.Wheel { return frontend.Wheel{Node: node, Row: 99, Delta: 1} }},
		{"column past the width", yes, func(node string, _ []int) frontend.Wheel {
			return frontend.Wheel{Node: node, Row: 1, Column: 999, Delta: 1}
		}},
		{"path to no node", yes, func(node string, _ []int) frontend.Wheel {
			return frontend.Wheel{Node: node, ViewPath: []int{9, 9}, Delta: 1}
		}},
		{"zero delta", yes, func(node string, path []int) frontend.Wheel { return frontend.Wheel{Node: node, ViewPath: path} }},
		{"regular mode", func() bool { return false }, func(node string, path []int) frontend.Wheel {
			return frontend.Wheel{Node: node, ViewPath: path, Delta: 1}
		}},
		{"no mouse hook", nil, func(node string, path []int) frontend.Wheel {
			return frontend.Wheel{Node: node, ViewPath: path, Delta: 1}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			surface, probe, node, path := surfaceNode(t, c.mouseCase, tc.mouse)
			surface.Wheel(tc.wheel(node, path))
			surface.Render()
			if len(probe.events) != 0 || len(probe.log) != 0 {
				t.Fatalf("the view got events %+v, log %q", probe.events, probe.log)
			}
		})
	}
}

// A wheel never counts toward a double click: it changes neither the click
// count of the next click nor the click that follows it.
func TestTuiSurfaceWheelLeavesTheClickCountAlone(t *testing.T) {
	c := wheelCases()["overlay takes"]
	surface, probe, node, path := surfaceNode(t, c.mouseCase, func() bool { return true })
	surface.Click(frontend.Click{Node: node, ViewPath: path})
	surface.Wheel(frontend.Wheel{Node: node, ViewPath: path, Delta: 1})
	surface.Click(frontend.Click{Node: node, ViewPath: path})
	if got := clickCounts(probe.events); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("click counts = %v, want [1 2] (a wheel between two clicks is not one)", got)
	}
}
