package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// screenSession is a recording session that shows fullscreen overlays.
type screenSession struct {
	*recordingSession
	screen frontend.Screen
	ok     bool
}

func (s *screenSession) Screen() (frontend.Screen, bool) { return s.screen, s.ok }

// clickLines draws fixed lines, reports area as its Click area, and records
// the clicks it is handed with the screen ScreenLines draws meanwhile.
type clickLines struct {
	lines   []string
	area    *frontend.Area
	surface *TuiSurface
	clicks  []TuiMouseEvent
	screens [][]string
}

func (c *clickLines) Render(int) []string { return slices.Clone(c.lines) }
func (c *clickLines) Invalidate()         {}
func (c *clickLines) ClickArea() (frontend.Area, bool) {
	if c.area == nil {
		return frontend.Area{}, false
	}
	return *c.area, true
}

func (c *clickLines) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	c.clicks = append(c.clicks, event)
	if c.surface != nil {
		c.screens = append(c.screens, c.surface.ScreenLines())
	}
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true}}
}

func numbered(prefix string, n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = prefix + strconv.Itoa(i)
	}
	return lines
}

// screenSurface starts a 60x20 surface on a session that shows a screen of
// the terminal's size less two rows, main from row 1 and column 3, and the
// dock from column 2.
func screenSurface(t *testing.T, document, dock Component) (*TuiSurface, *screenSession) {
	t.Helper()
	session := &screenSession{recordingSession: newRecordingSession(), ok: true,
		screen: frontend.Screen{Rows: -2, MainRow: 1, MainColumn: 3, DockColumn: 2}}
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	surface.SetLayout(document, dock)
	surface.Start()
	return surface, session
}

// mainLinesWithClick returns the id and node of the main Lines node with a
// Click area.
func mainLinesWithClick(t *testing.T, session *recordingSession) (string, frontend.Lines) {
	t.Helper()
	for _, entry := range session.tree[frontend.RegionMain] {
		if lines, ok := entry.node.(frontend.Lines); ok && lines.Click != nil {
			return entry.id, lines
		}
	}
	t.Fatal("no main Lines node with a Click area")
	return "", frontend.Lines{}
}

// A component's Click area travels with its Lines node, and a change of the
// area alone updates the node.
func TestTuiSurfaceReportsAClickAreaOnLines(t *testing.T) {
	logo := &clickLines{lines: []string{"ab", "cd"}, area: &frontend.Area{Row: 0, Column: 1, Rows: 2, Columns: 1}}
	surface, session := startedSurface(t, NewContainer(logo), nil)
	_, node := mainLinesWithClick(t, session)
	if *node.Click != (frontend.Area{Column: 1, Rows: 2, Columns: 1}) || !slices.Equal(node.Lines, []string{"ab", "cd"}) {
		t.Fatalf("node = %#v", node)
	}
	frames := len(session.frames)
	logo.area = &frontend.Area{Row: 1, Column: 0, Rows: 1, Columns: 2}
	surface.Render()
	if len(session.frames) != frames+1 {
		t.Fatalf("a new area sent %d frames", len(session.frames)-frames)
	}
	if _, node = mainLinesWithClick(t, session); *node.Click != (frontend.Area{Row: 1, Rows: 1, Columns: 2}) {
		t.Fatalf("updated area = %#v", node.Click)
	}
	logo.area = nil
	surface.Render()
	for _, entry := range session.tree[frontend.RegionMain] {
		if lines, ok := entry.node.(frontend.Lines); ok && lines.Click != nil {
			t.Fatalf("an area that went stays: %#v", lines.Click)
		}
	}
	assertReplayIsFresh(t, surface, session)
}

func TestNodesEqualComparesClickAndFullscreen(t *testing.T) {
	a, b := &frontend.Area{Rows: 1, Columns: 2}, &frontend.Area{Rows: 1, Columns: 2}
	if !nodesEqual(frontend.Lines{Lines: []string{"x"}, Click: a}, frontend.Lines{Lines: []string{"x"}, Click: b}) {
		t.Fatal("equal areas differ")
	}
	for _, other := range []*frontend.Area{nil, {Rows: 1, Columns: 3}} {
		if nodesEqual(frontend.Lines{Lines: []string{"x"}, Click: a}, frontend.Lines{Lines: []string{"x"}, Click: other}) {
			t.Fatalf("area %v equals %v", a, other)
		}
		if nodesEqual(frontend.Overlay{Click: a}, frontend.Overlay{Click: other}) {
			t.Fatalf("overlay area %v equals %v", a, other)
		}
	}
	if nodesEqual(frontend.Overlay{Fullscreen: true}, frontend.Overlay{}) {
		t.Fatal("a fullscreen overlay equals one that is not")
	}
}

// Screen counts a size of zero or less from the terminal's, and reports
// nothing for a session that shows no screen.
func TestTuiSurfaceScreenCountsFromTheTerminal(t *testing.T) {
	surface, session := screenSurface(t, NewContainer(), nil)
	screen, ok := surface.Screen()
	if !ok || screen.Columns != 60 || screen.Rows != 18 || screen.MainColumn != 3 {
		t.Fatalf("screen = %#v, %v", screen, ok)
	}
	session.screen.Columns, session.screen.Rows = 40, 10
	if screen, _ = surface.Screen(); screen.Columns != 40 || screen.Rows != 10 {
		t.Fatalf("explicit size = %#v", screen)
	}
	session.ok = false
	if _, ok := surface.Screen(); ok {
		t.Fatal("a session that cannot show a screen reported one")
	}
	plain, _ := startedSurface(t, NewContainer(), nil)
	if _, ok := plain.Screen(); ok {
		t.Fatal("a session without Screen reported one")
	}
}

// ScreenLines draws the document's end above the dock, main from MainRow and
// MainColumn and the dock at the bottom from DockColumn; while a click is
// handed to a component, the document from that component when the end
// leaves it out of view.
func TestTuiSurfaceScreenLinesFollowTheEndOrTheClickedComponent(t *testing.T) {
	logo := &clickLines{lines: []string{"logo0", "logo1"}, area: &frontend.Area{Rows: 2, Columns: 5}}
	body := &clickLines{lines: numbered("body", 30)}
	dock := &clickLines{lines: []string{"dock0", "dock1"}}
	surface, _ := screenSurface(t, NewContainer(NewContainer(logo), body), dock)
	logo.surface = surface
	lines := surface.ScreenLines()
	// 18 rows: row 0 blank (MainRow 1), rows 1 to 15 the document's last 15 lines, rows 16 and 17 the dock.
	want := make([]string, 18)
	for i := range 15 {
		want[1+i] = "   body" + strconv.Itoa(15+i)
	}
	want[16], want[17] = "  dock0", "  dock1"
	if !slices.Equal(lines, want) {
		t.Fatalf("following the end:\n%q\nwant\n%q", lines, want)
	}

	id, _ := mainLinesWithClick(t, surface.session.(*screenSession).recordingSession)
	surface.Click(frontend.Click{Node: id, Row: 1, Column: 2})
	if len(logo.clicks) != 1 {
		t.Fatalf("clicks = %d", len(logo.clicks))
	}
	event := logo.clicks[0]
	if event.Type != MouseClick || event.Button != MouseButtonLeft || event.X != 2 || event.Y != 1 || event.ScreenX != 5 || event.ScreenY != 2 {
		t.Fatalf("click event = %#v", event)
	}
	anchored := logo.screens[0]
	if anchored[1] != "   logo0" || anchored[2] != "   logo1" || anchored[3] != "   body0" || anchored[15] != "   body12" || anchored[17] != "  dock1" {
		t.Fatalf("screen from the clicked component:\n%q", anchored)
	}
	if got := surface.ScreenLines(); !slices.Equal(got, want) {
		t.Fatalf("after the click the screen follows the end again:\n%q", got)
	}
}

// A click reaches the component only inside its Click area and only for a
// node that shows.
func TestTuiSurfaceClickNeedsTheAreaAndTheNode(t *testing.T) {
	logo := &clickLines{lines: []string{"ab", "cd"}, area: &frontend.Area{Row: 1, Column: 1, Rows: 1, Columns: 1}}
	surface, session := startedSurface(t, NewContainer(logo), nil)
	id, _ := mainLinesWithClick(t, session)
	for _, click := range []frontend.Click{
		{Node: id, Row: 0, Column: 1}, {Node: id, Row: 1, Column: 0}, {Node: id, Row: 1, Column: 2}, {Node: id, Row: 2, Column: 1},
		{Node: "nope", Row: 1, Column: 1}, {Node: "overlay.7", Row: 1, Column: 1}, {Node: "overlay.x"},
	} {
		surface.Click(click)
		if len(logo.clicks) != 0 {
			t.Fatalf("click %#v reached the component", click)
		}
	}
	surface.Click(frontend.Click{Node: id, Row: 1, Column: 1})
	if len(logo.clicks) != 1 {
		t.Fatal("a click inside the area did not reach the component")
	}
	// Without a screen, the click's screen cell is the node's cell.
	if event := logo.clicks[0]; event.ScreenX != 1 || event.ScreenY != 1 {
		t.Fatalf("event = %#v", event)
	}
	surface.Stop()
	surface.Click(frontend.Click{Node: id, Row: 1, Column: 1})
	if len(logo.clicks) != 1 {
		t.Fatal("a click after Stop reached the component")
	}
}

// A fullscreen overlay covers the session's screen and takes a click
// anywhere; without a screen it is an ordinary overlay.
func TestTuiSurfaceReportsAFullscreenOverlay(t *testing.T) {
	options := OverlaySpec{
		Anchor:    "top-left",
		Width:     &OverlayValue{Value: 100, Percent: true},
		MaxHeight: &OverlayValue{Value: 100, Percent: true},
	}.Options()
	options.Fullscreen = true
	overlayNode := func(session *recordingSession) (string, frontend.Overlay) {
		t.Helper()
		overlays := session.tree[frontend.RegionOverlay]
		if len(overlays) != 1 {
			t.Fatalf("overlays = %#v", overlays)
		}
		return overlays[0].id, overlays[0].node.(frontend.Overlay)
	}

	surface, session := screenSurface(t, NewContainer(), nil)
	animation := &clickLines{lines: numbered("frame", 30)}
	surface.ShowOverlay(animation, options)
	surface.Render()
	id, node := overlayNode(session.recordingSession)
	if !node.Fullscreen || node.Width != 60 || len(node.Lines) != 18 || node.Click == nil || *node.Click != (frontend.Area{Rows: 18, Columns: 60}) {
		t.Fatalf("fullscreen overlay = %d lines %#v", len(node.Lines), node)
	}
	surface.Click(frontend.Click{Node: id})
	if len(animation.clicks) != 1 || animation.clicks[0].Type != MouseClick {
		t.Fatalf("overlay clicks = %#v", animation.clicks)
	}

	plain, plainSession := startedSurface(t, NewContainer(), nil)
	other := &clickLines{lines: numbered("frame", 30)}
	plain.ShowOverlay(other, options)
	plain.Render()
	id, node = overlayNode(plainSession)
	if node.Fullscreen || node.Click != nil || len(node.Lines) != 20 {
		t.Fatalf("overlay without a screen = %d lines %#v", len(node.Lines), node)
	}
	plain.Click(frontend.Click{Node: id})
	if len(other.clicks) != 0 {
		t.Fatal("an overlay that is not fullscreen took a click")
	}
	if !strings.HasPrefix(id, "overlay.") {
		t.Fatalf("overlay id %q", id)
	}
}

// A session that shows the dock itself gets the screen without the dock's
// lines: the document runs to the screen's last row.
func TestTuiSurfaceScreenLinesLeaveOutADockTheSessionShows(t *testing.T) {
	body := &clickLines{lines: numbered("body", 30)}
	dock := &clickLines{lines: []string{"dock0", "dock1"}}
	surface, session := screenSurface(t, NewContainer(body), dock)
	session.screen.ShowsDock = true
	lines := surface.ScreenLines()
	// 18 rows: row 0 blank (MainRow 1), rows 1 to 17 the document's last 17 lines.
	if len(lines) != 18 || lines[0] != "" || lines[1] != "   body13" || lines[17] != "   body29" {
		t.Fatalf("screen:\n%q", lines)
	}
	for _, line := range lines {
		if strings.Contains(line, "dock") {
			t.Fatalf("the dock's lines show: %q", line)
		}
	}
}

// Under a fullscreen overlay, a session that shows the dock itself keeps the
// editor node (not sendable, since the overlay takes the keys); any other
// session gets the dock as lines, as under any overlay that takes the keys.
func TestTuiSurfaceKeepsTheDockUnderAFullscreenOverlayWhenTheSessionShowsIt(t *testing.T) {
	options := OverlaySpec{
		Anchor:    "top-left",
		Width:     &OverlayValue{Value: 100, Percent: true},
		MaxHeight: &OverlayValue{Value: 100, Percent: true},
	}.Options()
	options.Fullscreen = true
	for _, showsDock := range []bool{true, false} {
		editor := NewEditor()
		session := &screenSession{recordingSession: newRecordingSession(), ok: true, screen: frontend.Screen{ShowsDock: showsDock}}
		surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
		surface.SetLayout(NewContainer(), NewContainer(NewContainer(editor), NewText("footer")))
		surface.SetHooks(SurfaceHooks{Editor: func() *Editor { return editor }, EditorSendable: func() bool { return surface.GetFocusedComponent() == Component(editor) }})
		surface.SetFocus(editor)
		surface.Start()
		surface.Render()
		surface.ShowOverlay(&clickLines{lines: numbered("frame", 20)}, options)
		surface.Render()
		ids := dockIDs(session.recordingSession)
		hasEditor := slices.Contains(ids, "editor")
		if hasEditor != showsDock {
			t.Fatalf("ShowsDock %v: dock = %v", showsDock, ids)
		}
		if showsDock {
			for _, entry := range session.tree[frontend.RegionDock] {
				if node, ok := entry.node.(frontend.Editor); ok && node.Sendable {
					t.Fatal("the editor is sendable while the overlay takes the keys")
				}
			}
		}
		surface.Stop()
	}
}
