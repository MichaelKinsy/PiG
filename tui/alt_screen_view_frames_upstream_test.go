package tui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/tui/test/tui-alt-screen.test.ts of Pi b7bb7ca905b8, 'scrolls without re-rendering components until content changes'
// (#9549: re-rendering a long transcript on every wheel event saturated a core). The component counts its Render calls, as Pi's
// does; a wheel event and ScrollBy move the viewport only, so no component renders until something requests a render for a change.

type countedComponent struct {
	text    *Text
	renders *int
}

func (c countedComponent) Render(width int) []string {
	*c.renders++
	return c.text.Render(width)
}
func (c countedComponent) Invalidate() { c.text.Invalidate() }

func TestAltScreenScrollsWithoutRenderingComponentsUntilContentChanges(t *testing.T) {
	rows := make([]string, 10)
	for i := range rows {
		rows[i] = fmt.Sprintf("line %d", i+1)
	}
	text := NewPaddedText(strings.Join(rows, "\n"), 0, 0, nil)
	renders := 0
	ui := newAltScreenForTest(nil, 20, 4, TuiAltScreenOptions{})
	ui.out = io.Discard
	ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	scroll := NewScrollView(countedComponent{text, &renders}, ScrollViewOptions{Follow: "end", Primary: true})
	t.Cleanup(scroll.Dispose)
	ui.SetLayoutRoot(scroll)
	ui.Start()
	ui.doRender()
	initial := renders

	ui.HandleViewportInput("\x1b[<64;1;1M")
	ui.doRender()
	ui.ScrollBy(-1)
	ui.doRender()
	if renders != initial {
		t.Fatalf("scrolling rendered the component %d more times", renders-initial)
	}
	want := []string{"line 5", "line 6", "line 7", "line 8"}
	if got := trimmedRows(stripRows(shownRows(ui.GetScreenLines(), 4))); !slices.Equal(got, want) {
		t.Fatalf("viewport = %q, want %q", got, want)
	}

	text.SetText("changed")
	ui.RequestRender()
	ui.doRender()
	if renders <= initial {
		t.Fatalf("a changed component was not rendered again")
	}
	if got := strings.TrimRight(widthx.StripAnsi(shownRows(ui.GetScreenLines(), 4)[0]), " "); got != "changed" {
		t.Fatalf("first row = %q, want %q", got, "changed")
	}
}

// Ports 'keeps search matches current across view-only scroll frames and content changes' of the same file: a frame that reuses earlier output must
// still let the search see content that changed in between. Frames are drawn with doRender, as the scheduled frame does; Render is a direct frame
// that never reuses output.
func TestAltScreenSearchStaysCurrentAcrossViewOnlyFramesAndContentChanges(t *testing.T) {
	useAltScreenBindings(t, nil)
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	lines[4] = "line 5 needle one"
	text := NewPaddedText(strings.Join(lines, "\n"), 0, 0, nil)
	ui := newAltScreenForTest(nil, 60, 8, TuiAltScreenOptions{})
	ui.out = io.Discard
	ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	transcript := NewScrollView(text, ScrollViewOptions{Follow: "end", Primary: true})
	t.Cleanup(transcript.Dispose)
	ui.SetLayoutRoot(transcript)
	ui.Start()
	ui.doRender()
	send := func(inputs ...string) {
		for _, data := range inputs {
			if !ui.HandleViewportInput(data) {
				ui.HandleFocusedSearchInput(data)
			}
		}
		ui.doRender()
	}
	shows := func(want string) bool {
		return slices.ContainsFunc(shownRows(ui.GetScreenLines(), 8), func(row string) bool { return strings.Contains(widthx.StripAnsi(row), want) })
	}

	send("\x1b[102;6u", "needle")
	if !shows("1/1") {
		t.Fatalf("one match expected: %q", shownRows(ui.GetScreenLines(), 8))
	}
	for range 3 {
		ui.HandleViewportInput("\x1b[<65;1;4M")
	}
	ui.doRender()
	if transcript.ScrollTop() <= 0 {
		t.Fatalf("the transcript did not scroll")
	}

	lines[10] = "line 11 needle two"
	text.SetText(strings.Join(lines, "\n"))
	ui.RequestRender()
	ui.doRender()
	if !shows("1/2") {
		t.Fatalf("two matches expected after the change: %q", shownRows(ui.GetScreenLines(), 8))
	}
	send("\x1b[103;6u")
	if !shows("2/2") || !shows("line 11 needle two") {
		t.Fatalf("the second match is not shown: %q", shownRows(ui.GetScreenLines(), 8))
	}
}

// liveText is a component with no dirty state: it draws a copy of whatever its rows hold when it is asked.
type liveText struct{ rows []string }

func (l *liveText) Render(int) []string { return slices.Clone(l.rows) }
func (*liveText) Invalidate()           {}

// viewFrameRig is a 20x4 alternate screen over a primary scroll view of ten rows "line 1" to "line 10", scrolled to its end.
func viewFrameRig(t *testing.T) (*TuiAltScreen, *liveText) {
	t.Helper()
	live := &liveText{rows: make([]string, 10)}
	for i := range live.rows {
		live.rows[i] = fmt.Sprintf("line %d", i+1)
	}
	ui := newAltScreenForTest(nil, 20, 4, TuiAltScreenOptions{})
	ui.out = io.Discard
	ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	scroll := NewScrollView(live, ScrollViewOptions{Follow: "end", Primary: true})
	t.Cleanup(scroll.Dispose)
	ui.SetLayoutRoot(scroll)
	ui.Start()
	ui.doRender()
	return ui, live
}

// Pi's stated limit of b7bb7ca905b8 (#11968): a component that changes state without requesting a render stays stale while the view scrolls, instead
// of being refreshed by the scroll. It was already stale without scrolling. An in-process Go component that is not dirty-tracked shows it.
func TestAltScreenScrollKeepsAComponentThatChangedWithoutARenderRequestStale(t *testing.T) {
	ui, live := viewFrameRig(t)
	live.rows[8] = "changed 9"
	ui.ScrollBy(-1)
	ui.doRender()
	want := []string{"line 6", "line 7", "line 8", "line 9"}
	if got := trimmedRows(stripRows(shownRows(ui.GetScreenLines(), 4))); !slices.Equal(got, want) {
		t.Fatalf("a scroll frame shows %q, want the stale %q", got, want)
	}
	ui.RequestRender()
	ui.doRender()
	want[3] = "changed 9"
	if got := trimmedRows(stripRows(shownRows(ui.GetScreenLines(), 4))); !slices.Equal(got, want) {
		t.Fatalf("after RequestRender the frame shows %q, want %q", got, want)
	}
}

// A render request is not undone by a scroll request before the frame: the frame renders everything. Focus changes render everything as well.
func TestAltScreenFramesAfterARenderRequestFocusChangeOrDirectRenderShowChangedComponents(t *testing.T) {
	changed := []string{"line 6", "line 7", "line 8", "changed 9"}
	cases := map[string]func(ui *TuiAltScreen, live *liveText){
		"render request then scroll": func(ui *TuiAltScreen, _ *liveText) { ui.RequestRender(); ui.ScrollBy(-1) },
		"focus change":               func(ui *TuiAltScreen, live *liveText) { ui.ScrollBy(-1); ui.SetFocus(live) },
		"immediate render request":   func(ui *TuiAltScreen, _ *liveText) { ui.RequestImmediateRender(); ui.ScrollBy(-1) },
	}
	for name, act := range cases {
		t.Run(name, func(t *testing.T) {
			ui, live := viewFrameRig(t)
			live.rows[8] = "changed 9"
			act(ui, live)
			ui.doRender()
			if got := trimmedRows(stripRows(shownRows(ui.GetScreenLines(), 4))); !slices.Equal(got, changed) {
				t.Fatalf("the frame shows %q, want %q", got, changed)
			}
		})
	}
}

// stripRows is rows without their escape sequences: a frame row ends with the reset a row of the screen carries.
func stripRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = widthx.StripAnsi(row)
	}
	return out
}
