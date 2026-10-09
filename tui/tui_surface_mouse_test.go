package tui

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// surfaceMouseSelectTheme and surfaceMouseSettingsTheme are the identity
// themes of upstream packages/tui/test/mouse-components.test.ts.
var surfaceMouseSelectTheme = SelectListTheme{
	SelectedPrefix: func(text string) string { return text },
	SelectedText:   func(text string) string { return text },
	Description:    func(text string) string { return text },
	ScrollInfo:     func(text string) string { return text },
	NoMatch:        func(text string) string { return text },
}

var surfaceMouseSettingsTheme = SettingsListTheme{
	Label:       func(text string, _ bool) string { return text },
	Value:       func(text string, _ bool) string { return text },
	Description: func(text string) string { return text },
	Cursor:      "> ",
	Hint:        func(text string) string { return text },
}

// These tests hold TuiSurface.Click on an extension's view (D91) to Pi's
// fullscreen renderer: the same click on the same element, made once through
// a session's ViewPath and once by a terminal's SGR reports at the cell the
// element shows at on the alternate screen, hands the component the same
// events with the same local cells. The terminal side finds each element on
// the screen it drew, as a user would, and wheels a list as a user would.

// kitSpec is a view node as an extension's kit sends it.
type kitSpec struct {
	kind       frontend.ViewKind
	id         string
	text       string
	padX, padY int
	gap        int
	align      string
	basis      *int
	children   []kitSpec
	items      []string
	maxVisible int
	selected   int
	searchable bool
}

// kitRecorder records the rows and width a node drew, as the host's
// viewRecorder does, and hands the mouse to the component it wraps.
type kitRecorder struct {
	Component
	node *frontend.ViewNode
}

func (r *kitRecorder) Render(width int) []string {
	lines := r.Component.Render(width)
	r.node.Rows, r.node.Width = len(lines), width
	return lines
}

func (r *kitRecorder) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	return DispatchMouseEvent(r.Component, event)
}

// kitProbe is an extension's ui.custom component with a kit view, as the
// host mounts it: the view's own components take the mouse first, and the
// component takes the rest when takes is set (customOverlay over
// viewSurface.HandleViewMouse). It records every event it is handed and the
// lists' callbacks.
type kitProbe struct {
	root    Component
	tree    *frontend.ViewNode
	refresh []func()
	takes   bool
	// takesRelease takes a release the view's components leave.
	takesRelease bool
	events       []TuiMouseEvent
	log          []string
	target       *TuiMouseDispatchTarget
	released     bool
}

func (p *kitProbe) Render(width int) []string { return p.root.Render(width) }
func (p *kitProbe) Invalidate()               { p.root.Invalidate() }

func (p *kitProbe) FrontendView(width int) *frontend.View {
	p.root.Render(width)
	for _, refresh := range p.refresh {
		refresh()
	}
	return &frontend.View{Root: cloneTestViewNode(*p.tree)}
}

func cloneTestViewNode(n frontend.ViewNode) frontend.ViewNode {
	n.Items = slices.Clone(n.Items)
	if n.Children != nil {
		children := make([]frontend.ViewNode, len(n.Children))
		for i, child := range n.Children {
			children[i] = cloneTestViewNode(child)
		}
		n.Children = children
	}
	return n
}

func (p *kitProbe) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	p.events = append(p.events, event)
	var result *TuiMouseDispatchResult
	if p.target != nil && (!p.released || event.Type == MouseClick) {
		result = DispatchMouseEvent(p.target.Component, RetargetMouseEvent(event, *p.target))
		switch event.Type {
		case MouseRelease:
			p.released = true
		case MouseClick:
			p.target, p.released = nil, false
		}
	} else {
		p.target, p.released = nil, false
		result = DispatchMouseEvent(p.root, event)
		if result != nil && event.Type == MousePress {
			target := result.Target
			p.target = &target
		}
	}
	if result == nil && !p.takes && (!p.takesRelease || event.Type != MouseRelease) {
		return nil
	}
	return &TuiMouseDispatchResult{
		TuiMouseEventResult: TuiMouseEventResult{Handled: true},
		Target:              TuiMouseDispatchTarget{Component: p, OriginX: event.ScreenX - event.X, OriginY: event.ScreenY - event.Y, Width: event.Width, Height: event.Height},
	}
}

// newKitProbe builds spec's components as the host's view builder does.
func newKitProbe(spec kitSpec, takes bool) *kitProbe {
	p := &kitProbe{tree: &frontend.ViewNode{}, takes: takes}
	p.root = p.build(spec, p.tree)
	return p
}

func (p *kitProbe) build(spec kitSpec, out *frontend.ViewNode) Component {
	out.Kind, out.ID = spec.kind, spec.id
	if spec.basis != nil {
		out.Stack = &frontend.ViewStack{Basis: *spec.basis, Shrink: 1, MaxSize: -1}
	}
	children := func() []Component {
		out.Children = make([]frontend.ViewNode, len(spec.children))
		comps := make([]Component, len(spec.children))
		for i := range spec.children {
			comps[i] = p.build(spec.children[i], &out.Children[i])
		}
		return comps
	}
	var c Component
	switch spec.kind {
	case frontend.ViewKindContainer:
		c = NewContainer(children()...)
	case frontend.ViewKindBox:
		out.PaddingX, out.PaddingY = spec.padX, spec.padY
		box := NewPaddedBox(spec.padX, spec.padY, nil)
		for _, child := range children() {
			box.AddChild(child)
		}
		c = box
	case frontend.ViewKindText:
		out.Text, out.PaddingX, out.PaddingY = spec.text, spec.padX, spec.padY
		c = NewPaddedText(spec.text, spec.padX, spec.padY, nil)
	case frontend.ViewKindHStack, frontend.ViewKindVStack:
		comps := children()
		entries := make([]StackChild, len(comps))
		for i, comp := range comps {
			entries[i] = StackChild{Component: comp}
			if b := spec.children[i].basis; b != nil {
				entries[i].Basis = new(*b)
			}
		}
		out.Gap, out.Align = spec.gap, spec.align
		if out.Align == "" {
			out.Align = "stretch"
		}
		opts := StackOptions{Gap: new(spec.gap), Align: spec.align}
		if spec.kind == frontend.ViewKindHStack {
			c = NewHStack(entries, opts)
		} else {
			c = NewVStack(entries, opts)
		}
	case frontend.ViewKindSelectList:
		items := make([]SelectItem, len(spec.items))
		for i, label := range spec.items {
			items[i] = SelectItem{Value: "v" + label, Label: label}
		}
		list := NewSelectList(items, spec.maxVisible, surfaceMouseSelectTheme, SelectListLayoutOptions{})
		list.SetSelectedIndex(spec.selected)
		list.OnSelectionChange = func(item SelectItem) {
			p.log = append(p.log, fmt.Sprintf("selectionChange:%d:%s", list.SelectedIndex(), item.Value))
		}
		list.OnSelect = func(item SelectItem) {
			p.log = append(p.log, fmt.Sprintf("select:%d:%s", list.SelectedIndex(), item.Value))
		}
		p.refresh = append(p.refresh, func() {
			out.Items = out.Items[:0]
			for _, item := range list.FilteredItems() {
				out.Items = append(out.Items, frontend.ViewItem{Value: item.Value, Label: item.Label})
			}
			out.Selected, out.MaxVisible = list.SelectedIndex(), list.MaxVisible()
		})
		c = list
	case frontend.ViewKindSettingsList:
		items := make([]SettingItem, len(spec.items))
		for i, label := range spec.items {
			items[i] = SettingItem{ID: "id" + label, Label: label, CurrentValue: "off", Values: []string{"off", "on"}}
		}
		list := NewSettingsList(items, spec.maxVisible, surfaceMouseSettingsTheme, func(id, value string) { p.log = append(p.log, "change:"+id+":"+value) }, nil, SettingsListOptions{EnableSearch: spec.searchable})
		list.SetSelectedIndex(spec.selected)
		p.refresh = append(p.refresh, func() {
			out.Items = out.Items[:0]
			for _, item := range list.DisplayedItems() {
				out.Items = append(out.Items, frontend.ViewItem{ID: item.ID, Label: item.Label, CurrentValue: item.CurrentValue})
			}
			out.Selected, out.MaxVisible, out.Searchable = list.SelectedIndex(), list.MaxVisible(), list.SearchEnabled()
		})
		c = list
	default:
		panic("kit kind " + spec.kind)
	}
	return &kitRecorder{Component: c, node: out}
}

func kitText(text string) kitSpec {
	return kitSpec{kind: frontend.ViewKindText, text: text}
}

// mouseCase is one click: on the element at path that shows marker (its
// first cell, or Item's row), in a view placed as an overlay with opts or,
// when dock is set, between two dock lines.
type mouseCase struct {
	spec  kitSpec
	takes bool
	// takesRelease: the component takes only a release.
	takesRelease bool
	dock         bool
	opts         OverlayOptions
	path         []int
	item         string
	column       int
	// marker is the text the clicked cell starts, column cells before it;
	// listMarker, for an Item click, the text right above the list's first
	// row.
	marker, listMarker string
	listRows           int // rows from listMarker to the list's first item row
	wheelUp            bool
}

// mouseLog is what a click handed the component.
type mouseLog struct {
	events []TuiMouseEvent
	log    []string
}

const mouseCols, mouseRows = 60, 20

// findOnScreen returns the cell text starts at on rows.
func findOnScreen(rows []string, text string) (row, col int, ok bool) {
	for r, line := range rows {
		plain := widthx.StripAnsi(line)
		if before, _, ok := strings.Cut(plain, text); ok {
			return r, widthx.VisibleWidth(before), true
		}
	}
	return 0, 0, false
}

func sgr(button, col, row int, release bool) string {
	end := "M"
	if release {
		end = "m"
	}
	return fmt.Sprintf("\x1b[<%d;%d;%d%s", button, col+1, row+1, end)
}

// clickClock is the time base of the clicks of a sequence.
var clickClock = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// terminalClick clicks c's element on a terminal in Pi's fullscreen mode.
func terminalClick(t *testing.T, c mouseCase) (mouseLog, *kitProbe) {
	t.Helper()
	return terminalClicks(t, c, []time.Duration{0})
}

// terminalClicks clicks c's element on a terminal in Pi's fullscreen mode
// once per pause, each that long after the one before.
func terminalClicks(t *testing.T, c mouseCase, pauses []time.Duration) (mouseLog, *kitProbe) {
	t.Helper()
	h := newAltHarness(t, mouseCols, mouseRows, TuiAltScreenOptions{})
	now := clickClock
	h.tui.now = func() time.Time { return now }
	h.tui.SetWheelScrollLines(WheelScrollLines{Lines: 1})
	probe := newKitProbe(c.spec, c.takes)
	probe.takesRelease = c.takesRelease
	var dock []StackChild
	if c.dock {
		dock = []StackChild{{Component: NewText("status")}, {Component: probe}, {Component: NewText("footer")}}
	} else {
		dock = []StackChild{{Component: NewText("footer")}}
	}
	h.tui.SetLayoutRoot(NewVStack([]StackChild{
		{Component: NewScrollView(NewText("doc"), ScrollViewOptions{Follow: "end", Primary: true}), StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)}},
		{Component: NewVStack(dock, StackOptions{}), StackEntryOptions: StackEntryOptions{Grow: new(0), Shrink: new(1), MinSize: new(1)}},
	}, StackOptions{}))
	h.start()
	if !c.dock {
		h.tui.ShowOverlay(probe, c.opts)
	}
	h.render()
	for _, pause := range pauses {
		now = now.Add(pause)
		if c.item == "" {
			row, col, ok := findOnScreen(h.viewport(), c.marker)
			if !ok {
				t.Fatalf("terminal: no %q on %q", c.marker, h.viewport())
			}
			h.send(sgr(0, col+c.column, row, false), sgr(0, col+c.column, row, true))
			continue
		}
		r, col, ok := findOnScreen(h.viewport(), c.listMarker)
		if !ok {
			t.Fatalf("terminal: no %q on %q", c.listMarker, h.viewport())
		}
		wheelRow := r + c.listRows
		for step := 0; ; step++ {
			if _, _, ok := findOnScreen(h.viewport(), c.marker); ok {
				break
			}
			if step > 40 {
				t.Fatalf("terminal: wheeling never showed %q", c.marker)
			}
			button := 65 // wheel down
			if c.wheelUp {
				button = 64
			}
			h.send(sgr(button, col, wheelRow, false))
		}
		row, _, _ := findOnScreen(h.viewport(), c.marker)
		h.send(sgr(0, col+c.column, row, false), sgr(0, col+c.column, row, true))
	}
	return mouseLog{events: probe.events, log: probe.log}, probe
}

// surfaceClick clicks c's element through a session's ViewPath.
func surfaceClick(t *testing.T, c mouseCase, mouse func() bool) (mouseLog, *kitProbe) {
	t.Helper()
	return surfaceClicks(t, c, mouse, []sessionClick{{}})
}

// sessionClick is a session's click on a mouseCase's element, or on its
// list's item when item is set, after pause, with Click.Count count.
type sessionClick struct {
	pause time.Duration
	count int
	item  string
}

// surfaceClicks makes a session's clicks on c's element through its
// ViewPath, each its pause after the one before.
func surfaceClicks(t *testing.T, c mouseCase, mouse func() bool, clicks []sessionClick) (mouseLog, *kitProbe) {
	t.Helper()
	probe := newKitProbe(c.spec, c.takes)
	probe.takesRelease = c.takesRelease
	dock := NewContainer(NewText("footer"))
	if c.dock {
		dock = NewContainer(NewText("status"), probe, NewText("footer"))
	}
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	// The clicks and frames run on the test goroutine, as the owner loop
	// runs them; a frame a component requests while one renders must not
	// render on a timer goroutine beside them.
	surface.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	surface.SetLayout(NewContainer(NewText("doc")), dock)
	surface.Start()
	now := clickClock
	surface.now = func() time.Time { return now }
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
		// The dock's view holds the dock's lines around the component's
		// view: the status line is child 0.
		path = append([]int{1}, path...)
	}
	for _, click := range clicks {
		now = now.Add(click.pause)
		item := c.item
		if click.item != "" {
			item = click.item
		}
		surface.Click(frontend.Click{Node: node, ViewPath: path, Item: item, Column: c.column, Count: click.count})
		surface.Render()
	}
	return mouseLog{events: probe.events, log: probe.log}, probe
}

// localCell returns the cell marker starts at in the probe's own lines.
func localCell(t *testing.T, p *kitProbe, width int, marker string) (int, int) {
	t.Helper()
	row, col, ok := findOnScreen(p.Render(width), marker)
	if !ok {
		t.Fatalf("no %q in the component's lines", marker)
	}
	return row, col
}

func runMouseCase(t *testing.T, c mouseCase) mouseLog {
	t.Helper()
	terminal, _ := terminalClick(t, c)
	surface, probe := surfaceClick(t, c, func() bool { return true })
	if len(terminal.events) == 0 {
		t.Fatal("the terminal click reached nothing")
	}
	if !reflect.DeepEqual(surface, terminal) {
		t.Fatalf("surface click differs from the terminal's:\nsurface  %+v\nterminal %+v", surface, terminal)
	}
	if c.item == "" {
		// The click lands on the element's first cell, as the component
		// laid it out.
		last := terminal.events[len(terminal.events)-1]
		row, col := localCell(t, probe, last.Width, c.marker)
		if last.Y != row || last.X != col {
			t.Fatalf("click at %d,%d, element %q at %d,%d", last.Y, last.X, c.marker, row, col)
		}
	}
	return terminal
}

func eventTypes(events []TuiMouseEvent) []TuiMouseEventType {
	types := make([]TuiMouseEventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

// marked is a box of two padded rows around a text that starts with marker,
// for an overlay that varies only in where it shows.
func marked(marker string) kitSpec {
	return kitSpec{kind: frontend.ViewKindContainer, children: []kitSpec{
		kitText("head"),
		{kind: frontend.ViewKindBox, padX: 2, padY: 1, children: []kitSpec{kitText(marker + " body")}},
	}}
}

// An overlay at each anchor, size and margin takes the click at the cell
// the compositor draws it at. A component that takes the press gets the
// press, then the release and the click as its gesture's target. One that
// leaves the press gets the release as whatever is under the pointer, and
// the click a release without a drag reports when it leaves the release
// too; one that takes only the release gets no click.
func TestTuiSurfaceClickMatchesTheTerminalOnOverlays(t *testing.T) {
	width := func(v float64, percent bool) *OverlayValue { return &OverlayValue{Value: v, Percent: percent} }
	placements := []struct {
		name string
		opts OverlayOptions
	}{
		{"center", OverlayOptions{}},
		{"top-left", OverlaySpec{Anchor: "top-left", Width: width(20, false)}.Options()},
		{"bottom-right", OverlaySpec{Anchor: "bottom-right", Width: width(30, false), OffsetX: -1, OffsetY: -2}.Options()},
		{"top-center percent", OverlaySpec{Anchor: "top-center", Width: width(50, true), Margin: OverlayMarginAll(2)}.Options()},
		{"right-center", OverlaySpec{Anchor: "right-center", Width: width(24, false)}.Options()},
		{"row col", OverlaySpec{Row: width(3, false), Col: width(7, false), Width: width(26, false)}.Options()},
		{"titled", OverlayOptions{Title: "Pick"}},
	}
	for _, place := range placements {
		path := []int{1, 0}
		if place.opts.Title != "" {
			path = []int{1, 1, 1, 0} // the frame's hstack, then the body
		}
		for _, mode := range []struct {
			name                string
			takes, takesRelease bool
			want                []TuiMouseEventType
		}{
			{"takes", true, false, []TuiMouseEventType{MousePress, MouseRelease, MouseClick}},
			{"leaves", false, false, []TuiMouseEventType{MousePress, MouseRelease, MouseClick}},
			{"release only", false, true, []TuiMouseEventType{MousePress, MouseRelease}},
		} {
			t.Run(place.name+" "+mode.name, func(t *testing.T) {
				got := runMouseCase(t, mouseCase{spec: marked("@@"), takes: mode.takes, takesRelease: mode.takesRelease, opts: place.opts, path: path, marker: "@@"})
				if !slices.Equal(eventTypes(got.events), mode.want) {
					t.Fatalf("events = %v, want %v", eventTypes(got.events), mode.want)
				}
			})
		}
	}
}

// A ViewPath names a node through containers, a box's padding, an hstack's
// widths, gap and alignment and a vstack's slots and gap: the click lands on
// the cell that node starts at.
func TestTuiSurfaceClickMatchesTheTerminalThroughViewPaths(t *testing.T) {
	three := 3
	spec := kitSpec{kind: frontend.ViewKindContainer, children: []kitSpec{
		kitText("A0 first"),
		{kind: frontend.ViewKindBox, padX: 3, padY: 2, children: []kitSpec{kitText("B0 one"), kitText("B1 two")}},
		{kind: frontend.ViewKindHStack, gap: 2, align: "center", children: []kitSpec{
			{kind: frontend.ViewKindText, text: "H0\nx\nx\nx\nx", basis: new(10)},
			{kind: frontend.ViewKindText, text: "H1", basis: new(8)},
			{kind: frontend.ViewKindText, text: "H2\ny", basis: new(9)},
		}},
		{kind: frontend.ViewKindHStack, gap: 1, align: "end", children: []kitSpec{
			{kind: frontend.ViewKindText, text: "E0\nz\nz", basis: new(12)},
			{kind: frontend.ViewKindText, text: "E1", basis: new(12)},
		}},
		{kind: frontend.ViewKindVStack, gap: 1, children: []kitSpec{
			{kind: frontend.ViewKindText, text: "V0", basis: &three},
			kitText("V1 next"),
		}},
	}}
	opts := OverlaySpec{Anchor: "top-left", Width: &OverlayValue{Value: 50}}.Options()
	for _, c := range []struct {
		path   []int
		marker string
	}{
		{[]int{0}, "A0"},
		{[]int{1, 0}, "B0"},
		{[]int{1, 1}, "B1"},
		{[]int{2, 0}, "H0"},
		{[]int{2, 1}, "H1"},
		{[]int{2, 2}, "H2"},
		{[]int{3, 1}, "E1"},
		{[]int{4, 0}, "V0"},
		{[]int{4, 1}, "V1"},
	} {
		t.Run(c.marker, func(t *testing.T) {
			runMouseCase(t, mouseCase{spec: spec, takes: true, opts: opts, path: c.path, marker: c.marker})
		})
	}
}

// listView is a select list or settings list under a marker row, in a box.
func listView(list kitSpec) kitSpec {
	return kitSpec{kind: frontend.ViewKindBox, padX: 1, padY: 1, children: []kitSpec{
		kitText("@list"),
		list,
	}}
}

func labels(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%02d", prefix, i)
	}
	return out
}

// An item scrolled out of a select list's window is wheeled into view, one
// line a step at the list's first row as a user would, then clicked: the
// list selects and chooses it.
func TestTuiSurfaceClickWheelsASelectListItemIntoView(t *testing.T) {
	for _, c := range []struct {
		item, marker string
		selected     int
		dock         bool
	}{
		{"vit03", "it03", 2, false},
		{"vit15", "it15", 2, false},
		{"vit01", "it01", 18, false},
		{"vit12", "it12", 2, true},
		{"vit04", "it04", 17, true},
	} {
		list := kitSpec{kind: frontend.ViewKindSelectList, id: "tracks", items: labels("it", 20), maxVisible: 5, selected: c.selected}
		t.Run(fmt.Sprintf("%s dock=%v", c.marker, c.dock), func(t *testing.T) {
			got := runMouseCase(t, mouseCase{
				spec: listView(list), dock: c.dock, path: []int{1}, item: c.item, column: 2, wheelUp: c.selected > 10,
				marker: c.marker, listMarker: "@list", listRows: 1,
			})
			want := "select:" + strings.TrimLeft(strings.TrimPrefix(c.marker, "it"), "0") + ":" + c.item
			if len(got.log) == 0 || got.log[len(got.log)-1] != want {
				t.Fatalf("log = %q, want it to end with %q", got.log, want)
			}
		})
	}
}

// A searchable settings list draws its search input and a blank row above
// its rows: the click lands on the setting's row below them, after wheeling
// it into view, and the setting cycles.
func TestTuiSurfaceClickReachesASettingsRowUnderItsSearch(t *testing.T) {
	for _, searchable := range []bool{false, true} {
		rows := 1
		if searchable {
			rows = 3
		}
		for _, c := range []struct {
			item, marker string
			selected     int
		}{{"ids01", "s01", 0}, {"ids08", "s08", 0}, {"ids00", "s00", 9}} {
			t.Run(fmt.Sprintf("%s searchable=%v", c.marker, searchable), func(t *testing.T) {
				list := kitSpec{kind: frontend.ViewKindSettingsList, id: "opts", items: labels("s", 10), maxVisible: 4, searchable: searchable, selected: c.selected}
				got := runMouseCase(t, mouseCase{
					spec: listView(list), path: []int{1}, item: c.item, column: 3, wheelUp: c.selected > 0,
					marker: c.marker, listMarker: "@list", listRows: rows,
				})
				if !slices.Equal(got.log, []string{"change:" + c.item + ":on"}) {
					t.Fatalf("log = %q", got.log)
				}
			})
		}
	}
}

// A dock view sits at the bottom of the screen with the dock's other lines
// around it: the click reaches the element's cell there, taken or not.
func TestTuiSurfaceClickMatchesTheTerminalOnDockRows(t *testing.T) {
	spec := kitSpec{kind: frontend.ViewKindContainer, children: []kitSpec{
		kitText("D0 first"),
		{kind: frontend.ViewKindBox, padX: 4, padY: 1, children: []kitSpec{kitText("D1 boxed")}},
		kitText("D2 last"),
	}}
	for _, c := range []struct {
		path   []int
		marker string
	}{{[]int{0}, "D0"}, {[]int{1, 0}, "D1"}, {[]int{2}, "D2"}} {
		for _, takes := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s takes=%v", c.marker, takes), func(t *testing.T) {
				runMouseCase(t, mouseCase{spec: spec, takes: takes, dock: true, path: c.path, marker: c.marker})
			})
		}
	}
}

// Outside fullscreen mode a terminal reports no mouse, so a session's click
// on an extension's view hands the component nothing.
func TestTuiSurfaceClickDoesNothingOutsideFullscreen(t *testing.T) {
	list := kitSpec{kind: frontend.ViewKindSelectList, id: "tracks", items: labels("it", 20), maxVisible: 5}
	for _, mouse := range []func() bool{nil, func() bool { return false }} {
		for _, c := range []mouseCase{
			{spec: marked("@@"), takes: true, path: []int{1, 0}, marker: "@@"},
			{spec: marked("@@"), takes: true, dock: true, path: []int{1, 0}, marker: "@@"},
			{spec: listView(list), takes: true, path: []int{1}, item: "vit15"},
		} {
			got, _ := surfaceClick(t, c, mouse)
			if len(got.events) != 0 || len(got.log) != 0 {
				t.Fatalf("regular mode handed %+v", got)
			}
		}
	}
}

// clickCounts are the clickCounts of the clicks in events.
func clickCounts(events []TuiMouseEvent) []int {
	var counts []int
	for _, e := range events {
		if e.Type == MouseClick {
			counts = append(counts, e.ClickCount)
		}
	}
	return counts
}

// Pi counts a terminal's clicks on the same cell within its double-click
// interval as the second and third of one gesture, and a session's clicks on
// an element count the same way. Tern reports a double click on a list item
// as a select per click and then an activate (Count 2): the activate repeats
// the second click, which already counted 2, so the component gets exactly
// the terminal's events, on a select list, a settings list and text that
// takes the press, in an overlay or the dock. Text that leaves the press
// starts a text selection, which reports a triple click's release as no
// click: the selection then anchors at the line's start.
func TestTuiSurfaceMultiClicksMatchTheTerminal(t *testing.T) {
	// A list shorter than its window keeps its rows where they are.
	list := kitSpec{kind: frontend.ViewKindSelectList, id: "tracks", items: labels("it", 4), maxVisible: 5}
	settings := kitSpec{kind: frontend.ViewKindSettingsList, id: "opts", items: labels("s", 3), maxVisible: 4}
	cases := []struct {
		name string
		c    mouseCase
		left bool
		// mid: the click is inside a word, past the anchor a double
		// click's word selection takes.
		mid bool
	}{
		{"select list", mouseCase{spec: listView(list), path: []int{1}, item: "vit02", column: 2, marker: "it02", listMarker: "@list", listRows: 1}, false, false},
		{"settings list", mouseCase{spec: listView(settings), path: []int{1}, item: "ids01", column: 3, marker: "s01", listMarker: "@list", listRows: 1}, false, false},
		{"text taken", mouseCase{spec: marked("@@"), takes: true, path: []int{1, 0}, marker: "@@"}, false, false},
		{"text left", mouseCase{spec: marked("@@"), path: []int{1, 0}, marker: "@@"}, true, false},
		{"text left mid-word", mouseCase{spec: marked("zq"), path: []int{1, 0}, marker: "zq", column: 1}, true, true},
		// "/" joins words (tui-alt-screen.ts TERMINAL_WORD_SELECTION_JOINERS):
		// the word's anchor is before the joiner, not the clicked part.
		{"text left past a joiner", mouseCase{spec: marked("zq/zq"), path: []int{1, 0}, marker: "zq/zq", column: 3}, true, true},
		{"dock text", mouseCase{spec: marked("@@"), takes: true, dock: true, path: []int{1, 0}, marker: "@@"}, false, false},
	}
	const ms = time.Millisecond
	sequences := []struct {
		name              string
		terminal          []time.Duration
		session           []sessionClick
		counts, left, mid []int
	}{
		{"double", []time.Duration{0, 150 * ms}, []sessionClick{{}, {pause: 150 * ms}, {count: 2}}, []int{1, 2}, []int{1, 2}, []int{1}},
		{"double without activate", []time.Duration{0, 150 * ms}, []sessionClick{{}, {pause: 150 * ms}}, []int{1, 2}, []int{1, 2}, []int{1}},
		{"triple", []time.Duration{0, 120 * ms, 120 * ms}, []sessionClick{{}, {pause: 120 * ms}, {count: 2}, {pause: 120 * ms}}, []int{1, 2, 3}, []int{1, 2}, []int{1}},
		{"four", []time.Duration{0, 100 * ms, 100 * ms, 100 * ms}, []sessionClick{{}, {pause: 100 * ms}, {count: 2}, {pause: 100 * ms}, {pause: 100 * ms}}, []int{1, 2, 3, 1}, []int{1, 2, 1}, []int{1, 1}},
		{"at the interval", []time.Duration{0, 500 * ms}, []sessionClick{{}, {pause: 500 * ms}}, []int{1, 2}, []int{1, 2}, []int{1}},
		{"past the interval", []time.Duration{0, 501 * ms}, []sessionClick{{}, {pause: 501 * ms}}, []int{1, 1}, []int{1, 1}, []int{1, 1}},
	}
	for _, tc := range cases {
		for _, seq := range sequences {
			t.Run(tc.name+" "+seq.name, func(t *testing.T) {
				terminal, _ := terminalClicks(t, tc.c, seq.terminal)
				surface, _ := surfaceClicks(t, tc.c, func() bool { return true }, seq.session)
				want := seq.counts
				switch {
				case tc.mid:
					want = seq.mid
				case tc.left:
					want = seq.left
				}
				if got := clickCounts(terminal.events); !slices.Equal(got, want) {
					t.Fatalf("terminal clickCounts = %v, want %v", got, want)
				}
				if !reflect.DeepEqual(surface, terminal) {
					t.Fatalf("surface clicks differ from the terminal's:\nsurface  %+v\nterminal %+v", surface, terminal)
				}
			})
		}
	}
}

// Tern's activate is a double click by the system's interval, which may be
// longer than Pi's: a second click Pi counted 1 is followed by the double
// click's gesture with clickCount 2. A click with Count 2 elsewhere than the
// last click, or long after it, is a double click there.
func TestTuiSurfaceActivateCompletesADoubleClick(t *testing.T) {
	list := kitSpec{kind: frontend.ViewKindSelectList, id: "tracks", items: labels("it", 4), maxVisible: 5}
	c := mouseCase{spec: listView(list), takes: true, path: []int{1}, item: "vit02", column: 2}
	const ms = time.Millisecond
	for _, seq := range []struct {
		name   string
		clicks []sessionClick
		counts []int
	}{
		{"slow double", []sessionClick{{}, {pause: 800 * ms}, {count: 2}}, []int{1, 1, 2}},
		{"slow double then a third", []sessionClick{{}, {pause: 800 * ms}, {count: 2}, {pause: 100 * ms}}, []int{1, 1, 2, 3}},
		{"another row", []sessionClick{{}, {pause: 100 * ms, item: "vit03"}, {item: "vit03", count: 2}}, []int{1, 1, 2}},
		{"activate after the interval", []sessionClick{{}, {pause: 100 * ms}, {pause: 600 * ms, count: 2}}, []int{1, 2, 2}},
		{"activate repeated", []sessionClick{{}, {pause: 100 * ms}, {count: 2}, {count: 2}}, []int{1, 2}},
	} {
		t.Run(seq.name, func(t *testing.T) {
			got, _ := surfaceClicks(t, c, func() bool { return true }, seq.clicks)
			if counts := clickCounts(got.events); !slices.Equal(counts, seq.counts) {
				t.Fatalf("clickCounts = %v, want %v (events %+v)", counts, seq.counts, got.events)
			}
			for _, e := range got.events {
				if e.Type == MousePress && e.ClickCount != 0 {
					t.Fatalf("a press carries clickCount %d", e.ClickCount)
				}
			}
		})
	}
}
