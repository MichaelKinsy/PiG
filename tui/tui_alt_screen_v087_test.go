package tui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports of upstream test/tui-alt-screen.test.ts cases added by v0.87.1. The
// harness plays the terminal: input goes to HandleViewportInput, then to a
// focused search box, then to the focused component (upstream TUI routing),
// and each upstream waitForRender is an explicit Render.

type altHarness struct {
	t    *testing.T
	out  *bytes.Buffer
	tui  *TuiAltScreen
	w, h int
}

func newAltHarness(t *testing.T, width, height int, options TuiAltScreenOptions) *altHarness {
	t.Helper()
	out := &bytes.Buffer{}
	tui := newAltScreenForTest(out, width, height, options)
	t.Cleanup(func() { tui.StopWithOptions(StopOptions{PreserveScreen: true}) })
	return &altHarness{t: t, out: out, tui: tui, w: width, h: height}
}

func (h *altHarness) start() { h.tui.Start() }

func (h *altHarness) render() { h.tui.Render() }

// send delivers input the way the TUI routes it and renders.
func (h *altHarness) send(inputs ...string) {
	for _, data := range inputs {
		if h.tui.HandleViewportInput(data) || h.tui.HandleFocusedSearchInput(data) {
			continue
		}
		if focused, ok := h.tui.FocusedComponent().(InputHandler); ok && ShouldDeliverKey(h.tui.FocusedComponent(), data) {
			focused.HandleInput(data)
		}
	}
	h.render()
}

func (h *altHarness) viewport() []string {
	rows := altScreenVisibleRows(h.t, h.out, h.w, h.h)
	for i := range rows {
		rows[i] = strings.TrimRight(rows[i], " ")
	}
	return rows
}

func (h *altHarness) viewportHas(needle string) bool {
	return slices.ContainsFunc(h.viewport(), func(row string) bool { return strings.Contains(row, needle) })
}

func (h *altHarness) wrote(needle string) bool { return strings.Contains(h.out.String(), needle) }

func numberedLines(count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	return strings.Join(lines, "\n")
}

func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

// columnOf returns the cell column of needle in a visible row.
func columnOf(row, needle string, last bool) int {
	index := strings.Index(row, needle)
	if last {
		index = strings.LastIndex(row, needle)
	}
	if index < 0 {
		return -1
	}
	return widthx.VisibleWidth(row[:index])
}

func transcriptOverDock(transcript Component, dock Component, dockBasis *int) *VStack {
	return NewVStack([]StackChild{
		{Component: transcript, StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1), MinSize: new(1)}},
		{Component: dock, StackEntryOptions: StackEntryOptions{Basis: dockBasis, Shrink: new(0), MinSize: new(1)}},
	}, StackOptions{})
}

// inputRecorder is a focusable component that records delivered keys.
type inputRecorder struct {
	invalidatable
	lines  []string
	inputs []string
}

func (r *inputRecorder) Render(int) []string     { return r.lines }
func (r *inputRecorder) HandleInput(data string) { r.inputs = append(r.inputs, data) }

func TestAltScreenJumpToEndIndicatorIsClickable(t *testing.T) {
	h := newAltHarness(t, 30, 6, TuiAltScreenOptions{ScrollToEndIndicator: func() string { return "\x1b[7m ↓ Jump to end \x1b[27m" }})
	transcript := NewScrollView(NewText(numberedLines(8)), ScrollViewOptions{Follow: "end", Primary: true})
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, NewText("editor\nfooter"), nil))
	h.start()
	if h.viewportHas("Jump to end") {
		t.Fatal("the indicator must not show while following")
	}
	h.send("\x1b[<64;1;1M")
	if transcript.IsFollowingEnd() {
		t.Fatal("wheel up must stop following")
	}
	rows := h.viewport()
	if rows[3] != "line 7  ↓ Jump to end" || rows[4] != "editor" {
		t.Fatalf("rows = %q", rows)
	}
	// Pressing next to the label starts a selection instead of jumping.
	h.send("\x1b[<0;2;4M", "\x1b[<0;2;4m")
	if transcript.IsFollowingEnd() {
		t.Fatal("a press beside the label must not jump")
	}
	h.send("\x1b[<0;15;4M", "\x1b[<0;15;4m")
	if !transcript.IsFollowingEnd() {
		t.Fatal("a press on the label jumps to the end")
	}
	if got, want := h.viewport(), []string{"line 5", "line 6", "line 7", "line 8", "editor", "footer"}; !slices.Equal(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

func TestAltScreenJumpToEndIndicatorStaysCenteredAsScrollbarHides(t *testing.T) {
	const label = " ↓ Jump to latest message · End "
	h := newAltHarness(t, 80, 6, TuiAltScreenOptions{ScrollToEndIndicator: func() string { return label }})
	zero := 0
	transcript := NewScrollView(NewText(numberedLines(20)), ScrollViewOptions{Follow: "end", Primary: true, Scrollbar: "auto", ScrollbarHideDelayMs: &zero})
	h.tui.SetLayoutRoot(transcript)
	h.start()
	labelColumn := func() int { return columnOf(altScreenVisibleRows(t, h.out, h.w, h.h)[5], label, false) }

	// Scrolling over the track keeps the scrollbar visible until the pointer leaves.
	h.send("\x1b[<64;80;1M")
	if !transcript.IsScrollbarVisible() || transcript.IsFollowingEnd() {
		t.Fatalf("visible=%v following=%v", transcript.IsScrollbarVisible(), transcript.IsFollowingEnd())
	}
	scrollTop := transcript.ScrollTop()
	visibleColumn := labelColumn()

	// Leaving the track lets the auto-hide timer expire without changing content.
	h.send("\x1b[<35;79;1M")
	time.Sleep(20 * time.Millisecond)
	h.render()
	if transcript.IsScrollbarVisible() || transcript.ScrollTop() != scrollTop {
		t.Fatalf("after leaving: visible=%v top=%d", transcript.IsScrollbarVisible(), transcript.ScrollTop())
	}
	hiddenColumn := labelColumn()

	h.send("\x1b[<35;80;1M")
	if !transcript.IsScrollbarVisible() || transcript.ScrollTop() != scrollTop {
		t.Fatalf("after re-entering: visible=%v top=%d", transcript.IsScrollbarVisible(), transcript.ScrollTop())
	}
	if got := []int{visibleColumn, hiddenColumn, labelColumn()}; !slices.Equal(got, []int{24, 24, 24}) {
		t.Fatalf("label columns = %v, want [24 24 24]", got)
	}
}

func TestAltScreenJumpToEndIndicatorSparesTheScrollbar(t *testing.T) {
	h := newAltHarness(t, 30, 6, TuiAltScreenOptions{ScrollToEndIndicator: func() string { return strings.Repeat("↓", 30) }})
	transcript := NewScrollView(NewText(numberedLines(12)), ScrollViewOptions{Follow: "end", Primary: true, Scrollbar: "always"})
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, NewText("editor\nfooter"), nil))
	h.start()
	h.send("\x1b[<64;1;1M")
	if transcript.IsFollowingEnd() {
		t.Fatal("wheel up must stop following")
	}
	if got := h.viewport()[3]; got != strings.Repeat("↓", 29)+"┃" {
		t.Fatalf("indicator row = %q", got)
	}
	// The indicator must not intercept a press on the scrollbar's column.
	h.send("\x1b[<0;30;4M", "\x1b[<0;30;4m")
	if transcript.IsFollowingEnd() {
		t.Fatal("a scrollbar press must not jump to the end")
	}
}

func TestAltScreenJumpToEndIndicatorNeedsFollowEnd(t *testing.T) {
	h := newAltHarness(t, 30, 3, TuiAltScreenOptions{ScrollToEndIndicator: func() string { return " ↓ Jump to end " }})
	transcript := NewScrollView(NewText("one\ntwo\nthree\nfour\nfive"), ScrollViewOptions{Primary: true})
	h.tui.SetLayoutRoot(transcript)
	h.start()
	if transcript.IsFollowingEnd() || h.viewportHas("Jump to end") {
		t.Fatal("a primary view without follow-end never shows the indicator")
	}
}

func TestAltScreenAltWheelScrollsFaster(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText(numberedLines(12)))
	h.start()
	if h.tui.ViewportTop() != 8 {
		t.Fatalf("viewport top = %d, want 8", h.tui.ViewportTop())
	}
	// Alt sets bit 8 on the wheel button (72 = 64 + 8).
	h.send("\x1b[<72;1;1M")
	if h.tui.ViewportTop() != 3 {
		t.Fatalf("viewport top after alt wheel = %d, want 3", h.tui.ViewportTop())
	}
}

func TestAltScreenDoesNotRedispatchMissesThroughHorizontalLayouts(t *testing.T) {
	h := newAltHarness(t, 20, 2, TuiAltScreenOptions{})
	list := &mouseProbe{lines: []string{"A", "B"}, result: &TuiMouseEventResult{Handled: true}}
	h.tui.SetLayoutRoot(NewHStack([]StackChild{
		{Component: list, StackEntryOptions: StackEntryOptions{Basis: new(10)}},
		{Component: NewText("plain"), StackEntryOptions: StackEntryOptions{Basis: new(10)}},
	}, StackOptions{}))
	h.start()
	h.send("\x1b[<0;15;1M", "\x1b[<0;15;1m")
	if len(list.events) != 0 {
		t.Fatalf("a press on the right column reached the left list: %+v", list.events)
	}
}

func unsetEnvForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAltScreenUsesButtonMotionTrackingInMultiplexers(t *testing.T) {
	keys := []string{"TMUX", "ZELLIJ", "STY", "TERM"}
	unsetEnvForTest(t, keys...)
	t.Setenv("TERM", "xterm-256color")
	direct := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	direct.start()
	if !direct.wrote("\x1b[?1003h") {
		t.Fatal("a direct terminal gets all-motion tracking")
	}
	for name, environment := range map[string]map[string]string{
		"tmux environment":   {"TMUX": "/tmp/tmux/default,1,0"},
		"tmux TERM":          {"TERM": "tmux-256color"},
		"Zellij environment": {"ZELLIJ": "0"},
		"Screen environment": {"STY": "123.session"},
		"Screen TERM":        {"TERM": "screen-256color"},
	} {
		unsetEnvForTest(t, keys...)
		for key, value := range environment {
			t.Setenv(key, value)
		}
		h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
		h.start()
		if !h.wrote("\x1b[?1002h") || h.wrote("\x1b[?1003h") || !h.wrote("\x1b[?1006h") {
			t.Fatalf("%s should enable button-motion SGR tracking only: %q", name, h.out.String())
		}
		h.tui.StopWithOptions(StopOptions{PreserveScreen: true})
	}
}

func TestAltScreenRightClickPasteOnlyOnWindowsOutsideVSCode(t *testing.T) {
	previous := altScreenPlatform
	t.Cleanup(func() { altScreenPlatform = previous })
	pastes := 0
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{OnRightClickPaste: func() { pastes++ }})
	altScreenPlatform = "windows"
	unsetEnvForTest(t, "TERM_PROGRAM")
	h.start()
	h.send("\x1b[<2;1;1M", "\x1b[<2;1;1m")
	if pastes != 1 {
		t.Fatalf("pastes = %d, want 1", pastes)
	}
	t.Setenv("TERM_PROGRAM", "vscode")
	h.send("\x1b[<2;1;1M")
	if pastes != 1 {
		t.Fatal("VS Code handles its own right-click paste")
	}
	altScreenPlatform = "linux"
	unsetEnvForTest(t, "TERM_PROGRAM")
	h.send("\x1b[<2;1;1M")
	if pastes != 1 {
		t.Fatal("right-click paste is Windows only")
	}
}

func TestAltScreenRevealsHiddenAutoScrollbarOnHover(t *testing.T) {
	h := newAltHarness(t, 10, 5, TuiAltScreenOptions{})
	delay := 20
	scrollView := NewScrollView(NewText(numberedLines(20)), ScrollViewOptions{Primary: true, Scrollbar: "auto", ScrollbarHideDelayMs: &delay})
	h.tui.SetLayoutRoot(scrollView)
	h.start()
	if scrollView.IsScrollbarVisible() {
		t.Fatal("an auto scrollbar starts hidden")
	}
	h.send("\x1b[<35;10;3M")
	if !scrollView.IsScrollbarVisible() || !scrollView.IsScrollbarActive() {
		t.Fatal("hovering the hidden track reveals and activates the scrollbar")
	}
	if !slices.ContainsFunc(h.viewport(), func(row string) bool { return strings.ContainsAny(row, "│█") }) {
		t.Fatalf("scrollbar glyphs missing: %q", h.viewport())
	}
	h.send("\x1b[<35;9;3M")
	time.Sleep(40 * time.Millisecond)
	h.render()
	if scrollView.IsScrollbarVisible() {
		t.Fatal("leaving the track lets the scrollbar hide")
	}
}

func TestAltScreenScrollbarTrackPressJumpsThenDrags(t *testing.T) {
	h := newAltHarness(t, 10, 10, TuiAltScreenOptions{})
	scrollView := NewScrollView(NewText(numberedLines(50)), ScrollViewOptions{Primary: true, Scrollbar: "always"})
	h.tui.SetLayoutRoot(scrollView)
	h.start()
	h.send("\x1b[<0;10;6M")
	if scrollView.ScrollTop() != 20 {
		t.Fatalf("track press scrollTop = %d, want 20", scrollView.ScrollTop())
	}
	h.send("\x1b[<32;10;10M")
	if scrollView.ScrollTop() != 40 {
		t.Fatalf("drag scrollTop = %d, want 40", scrollView.ScrollTop())
	}
	h.send("\x1b[<0;10;10m")
	if h.wrote("\x1b]52;c;") {
		t.Fatal("a scrollbar gesture must not copy")
	}
}

func TestAltScreenHalfPageAndLineActions(t *testing.T) {
	h := newAltHarness(t, 20, 10, TuiAltScreenOptions{})
	h.tui.Add(NewText(numberedLines(30)))
	h.start()
	for _, step := range []struct {
		action TUIKeybinding
		want   int
	}{
		{KBAltScreenHalfPageUp, 15}, {KBAltScreenHalfPageDown, 20},
		{KBAltScreenLineUp, 19}, {KBAltScreenLineDown, 20},
	} {
		h.tui.performViewportAction(step.action)
		h.render()
		if got := h.tui.ViewportTop(); got != step.want {
			t.Fatalf("%s: viewport top = %d, want %d", step.action, got, step.want)
		}
	}
}

// TestAltScreenHalfPageCustomBindings ports upstream "scrolls the transcript by
// half a page with custom bindings".
func TestAltScreenHalfPageCustomBindings(t *testing.T) {
	useAltScreenBindings(t, map[string][]string{KBAltScreenHalfPageUp: {"ctrl+u"}, KBAltScreenHalfPageDown: {"ctrl+d"}})
	h := newAltHarness(t, 20, 10, TuiAltScreenOptions{})
	h.tui.Add(NewText(numberedLines(30)))
	h.start()
	h.send("\x15")
	if got := h.tui.ViewportTop(); got != 15 {
		t.Fatalf("ctrl+u viewport top = %d, want 15", got)
	}
	h.send("\x04")
	if got := h.tui.ViewportTop(); got != 20 {
		t.Fatalf("ctrl+d viewport top = %d, want 20", got)
	}
}

// TestAltScreenLineCustomBindings ports upstream "scrolls the transcript by one
// line with custom bindings".
func TestAltScreenLineCustomBindings(t *testing.T) {
	useAltScreenBindings(t, map[string][]string{KBAltScreenLineUp: {"ctrl+y"}, KBAltScreenLineDown: {"ctrl+e"}})
	h := newAltHarness(t, 20, 10, TuiAltScreenOptions{})
	h.tui.Add(NewText(numberedLines(30)))
	h.start()
	h.send("\x19")
	if got := h.tui.ViewportTop(); got != 19 {
		t.Fatalf("ctrl+y viewport top = %d, want 19", got)
	}
	h.send("\x05")
	if got := h.tui.ViewportTop(); got != 20 {
		t.Fatalf("ctrl+e viewport top = %d, want 20", got)
	}
}

// TestAltScreenModifiedNavigationReachesFocusedComponent ports upstream "routes
// Ctrl-modified viewport navigation to the focused component".
func TestAltScreenModifiedNavigationReachesFocusedComponent(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 20, 6, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(12)), ScrollViewOptions{Follow: "end", Primary: true})
	editor := &inputRecorder{lines: []string{"editor"}}
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, editor, new(1)))
	h.tui.SetFocus(editor)
	h.start()
	h.send("\x1bOH")
	if transcript.ScrollTop() != 0 || len(editor.inputs) != 0 {
		t.Fatalf("home: top=%d editor=%q", transcript.ScrollTop(), editor.inputs)
	}
	modified := []string{"\x1b[1;5H", "\x1b[1;5F", "\x1b[5;5~", "\x1b[6;5~", "\x1b[57423;5u"}
	h.send(append(slices.Clone(modified), "\x1b[57423;5:3u")...)
	if transcript.ScrollTop() != 0 || !slices.Equal(editor.inputs, modified) {
		t.Fatalf("modified keys: top=%d editor=%q", transcript.ScrollTop(), editor.inputs)
	}
	h.send("\x1b[6~")
	if transcript.ScrollTop() != 1 || !slices.Equal(editor.inputs, modified) {
		t.Fatalf("page down: top=%d editor=%q", transcript.ScrollTop(), editor.inputs)
	}
}

// TestAltScreenPromptJumpsWithKittyAndCtrlArrows ports upstream "jumps between
// OSC 133 semantic prompt markers" with the exact Kitty keypad-arrow press and
// release reports followed by the legacy Ctrl+Shift arrow reports.
func TestAltScreenPromptJumpsWithKittyAndCtrlArrows(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 20, 3, TuiAltScreenOptions{})
	var lines []string
	for message := 1; message <= 4; message++ {
		lines = append(lines, fmt.Sprintf("\x1b]133;A\x07message %d", message), "detail")
	}
	h.tui.Add(NewText(strings.Join(lines, "\n")))
	h.start()
	if h.tui.ViewportTop() != 5 {
		t.Fatalf("viewport top = %d, want 5", h.tui.ViewportTop())
	}
	for _, step := range []struct {
		inputs  []string
		top     int
		row     int
		message string
	}{
		{[]string{"\x1b[57419;6u", "\x1b[57419;6:3u"}, 4, 0, "message 3"},
		{[]string{"\x1b[1;6A"}, 2, 0, "message 2"},
		{[]string{"\x1b[57420;6u", "\x1b[57420;6:3u"}, 4, 0, "message 3"},
		{[]string{"\x1b[1;6B"}, 5, 1, "message 4"},
	} {
		h.send(step.inputs...)
		if got := h.tui.ViewportTop(); got != step.top || h.viewport()[step.row] != step.message {
			t.Fatalf("%q: top=%d rows=%q, want top %d with %q", step.inputs, got, h.viewport(), step.top, step.message)
		}
	}
	if !h.tui.IsFollowingOutput() {
		t.Fatal("jumping to the last prompt at the end resumes following")
	}
}

func TestAltScreenSearchButtonsNavigateAndHover(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 120, 6, TuiAltScreenOptions{
		SearchNavigationButtonStyle: func(text string, hovered bool) string {
			if hovered {
				return "\x1b[45m" + text + "\x1b[49m"
			}
			return "\x1b[44m" + text + "\x1b[49m"
		},
	})
	h.tui.Add(NewText("needle one\nmiddle\nneedle two\nend"))
	h.start()
	h.send("\x1b[102;6u", "needle")
	if !h.viewportHas("1/2") || !h.viewportHas("↑ Shift+Enter · ↓ Enter") {
		t.Fatalf("search box = %q", h.viewport())
	}
	arrowRow := slices.IndexFunc(h.viewport(), func(row string) bool { return strings.Contains(row, "↑") && strings.Contains(row, "↓") })
	arrowColumn := columnOf(h.viewport()[arrowRow], "Enter", true)
	h.out.Reset()
	h.send(fmt.Sprintf("\x1b[<35;%d;%dM", arrowColumn+1, arrowRow+1))
	if !h.wrote("\x1b[45m↓ Enter\x1b[49m") {
		t.Fatal("hovering the next button restyles it")
	}
	h.send(fmt.Sprintf("\x1b[<0;%d;%dM", arrowColumn+1, arrowRow+1))
	if !h.viewportHas("2/2") {
		t.Fatalf("next button: %q", h.viewport())
	}
	previousColumn := columnOf(h.viewport()[arrowRow], "Shift+Enter", false) + 3
	h.send(fmt.Sprintf("\x1b[<0;%d;%dM", previousColumn+1, arrowRow+1))
	if !h.viewportHas("1/2") {
		t.Fatalf("previous button: %q", h.viewport())
	}
	h.send("\x1b[102;6u")
	if h.viewportHas("↑ Shift+Enter · ↓ Enter") {
		t.Fatal("the shortcut toggles search closed")
	}
}

func TestAltScreenSearchIgnoresTranscriptBoxDrawing(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 80, 10, TuiAltScreenOptions{})
	h.tui.Add(NewText(strings.Join([]string{
		"needle one", "middle", "needle two", "filler",
		"┌" + strings.Repeat("─", 40) + "┐", "│ box" + strings.Repeat(" ", 36) + "│", "└" + strings.Repeat("─", 40) + "┘", "end",
	}, "\n")))
	h.start()
	h.send("\x1b[102;6u", "needle")
	if !h.viewportHas("1/2") || h.viewportHas("2/2") {
		t.Fatalf("search = %q", h.viewport())
	}
	boxBottom := slices.IndexFunc(h.viewport(), func(row string) bool { return strings.HasPrefix(row, "└") })
	h.send(fmt.Sprintf("\x1b[<0;24;%dM", boxBottom+1))
	if !h.viewportHas("1/2") || h.viewportHas("2/2") {
		t.Fatalf("a press on transcript box drawing navigated: %q", h.viewport())
	}
}

func TestAltScreenSearchMatchStyles(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 60, 4, TuiAltScreenOptions{
		SearchMatchStyle:        func(text string) string { return "\x1b[41m" + text + "\x1b[49m" },
		SearchCurrentMatchStyle: func(text string) string { return "\x1b[42m" + text + "\x1b[49m" },
	})
	h.tui.Add(NewText("needle first\nmiddle\nneedle second\nend"))
	h.start()
	h.send("\x1b[102;6u", "needle")
	if !h.wrote("\x1b[42mneedle\x1b[49m") || !h.wrote("\x1b[41mneedle\x1b[49m") {
		t.Fatal("current and other matches use their configured styles")
	}
}

// TestAltScreenSearchRestoresEditorFocus ports upstream "searches the
// transcript with Ctrl+Shift+F and restores editor focus on close".
func TestAltScreenSearchRestoresEditorFocus(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 60, 8, TuiAltScreenOptions{})
	lines := strings.Split(numberedLines(12), "\n")
	lines[4] = "line 5 needle one"
	lines[9] = "line 10 needle two"
	transcript := NewScrollView(NewText(strings.Join(lines, "\n")), ScrollViewOptions{Follow: "end", Primary: true})
	editor := &inputRecorder{lines: []string{"editor"}}
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, editor, new(1)))
	h.tui.SetFocus(editor)
	h.start()

	h.send("\x1b[102;6u", "needle")
	h.render()
	if transcript.IsFollowingEnd() || !h.viewportHas("2/2") || !h.viewportHas("↑ Shift+Enter · ↓ Enter") ||
		!h.viewportHas("line 10 needle two") || len(editor.inputs) != 0 {
		t.Fatalf("search: following=%v rows=%q editor=%q", transcript.IsFollowingEnd(), h.viewport(), editor.inputs)
	}
	if !h.wrote("\x1b[1;7mneedle\x1b[22;27m") {
		t.Fatal("the current match uses the default current style")
	}
	for range 6 {
		h.send("\x1b[<64;1;4M")
	}
	if transcript.ScrollTop() != 0 || !slices.ContainsFunc(h.viewport(), func(row string) bool {
		return strings.Contains(row, "needle") && strings.Contains(row, "2/2")
	}) {
		t.Fatalf("wheel while searching: top=%d rows=%q", transcript.ScrollTop(), h.viewport())
	}
	h.send("\x07")
	if !h.viewportHas("1/2") || !h.viewportHas("line 5 needle one") {
		t.Fatalf("ctrl+g: %q", h.viewport())
	}
	h.send("\x1b[103;6u")
	h.render()
	if !h.viewportHas("2/2") || !h.viewportHas("line 10 needle two") {
		t.Fatalf("ctrl+shift+g: %q", h.viewport())
	}
	h.send("\x1b", "x")
	if h.viewportHas("↑ Shift+Enter · ↓ Enter") || !slices.Equal(editor.inputs, []string{"x"}) {
		t.Fatalf("escape closes search and restores the editor: rows=%q editor=%q", h.viewport(), editor.inputs)
	}
}

// TestAltScreenSearchKeepsViewportScrolling ports upstream "keeps viewport
// scrolling while transcript search is focused".
func TestAltScreenSearchKeepsViewportScrolling(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 20, 6, TuiAltScreenOptions{})
	h.tui.Add(NewText(numberedLines(12)))
	h.start()
	topBefore := h.tui.ViewportTop()
	h.send("\x1b[102;6u")
	if !h.viewportHas("↑ ↓") {
		t.Fatalf("compact search controls missing: %q", h.viewport())
	}
	h.send("\x1b[5~", "\x1b[<64;1;4M")
	if h.tui.ViewportTop() >= topBefore || !h.viewportHas("↑ ↓") {
		t.Fatalf("scrolling with search open: top %d -> %d", topBefore, h.tui.ViewportTop())
	}
}

// TestAltScreenSearchActionsDriveDirectly drives the search actions without key
// bindings: toggle, query, next, previous, and close.
func TestAltScreenSearchActionsDriveDirectly(t *testing.T) {
	h := newAltHarness(t, 60, 6, TuiAltScreenOptions{})
	h.tui.Add(NewText("needle a\nmiddle\nneedle b\nneedle c"))
	h.start()
	h.tui.performViewportAction(KBAltScreenSearch)
	if !h.tui.IsSearchFocused() {
		t.Fatal("the search action opens a focused search box")
	}
	h.tui.HandleFocusedSearchInput("needle")
	h.render()
	for _, step := range []struct {
		action TUIKeybinding
		want   string
	}{{KBAltScreenSearchNext, "2/3"}, {KBAltScreenSearchNext, "3/3"}, {KBAltScreenSearchNext, "1/3"}, {KBAltScreenSearchPrevious, "3/3"}} {
		h.tui.performViewportAction(step.action)
		h.render()
		if !h.viewportHas(step.want) {
			t.Fatalf("%s: rows=%q, want %s", step.action, h.viewport(), step.want)
		}
	}
	h.tui.performViewportAction(KBAltScreenSearchClose)
	h.render()
	if h.tui.IsSearchFocused() || h.tui.HandleFocusedSearchInput("x") {
		t.Fatal("the close action removes the search box")
	}
}

func TestAltScreenCopiesAfterGenericRelease(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText("\x1b[1mal\x1b[0mpha\nbeta\ngamma\ndelta"))
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<32;4;2M", "\x1b[<3;4;2m")
	if !h.wrote(osc52("alpha\nbeta")) || !h.wrote("\x1b[7m") {
		t.Fatalf("generic release must copy the selection: %q", h.out.String())
	}
	if !h.wrote("al\x1b[0m\x1b[7mpha") {
		t.Fatal("selection inverse must be reapplied after a reset inside the selection")
	}
	if !h.viewportHas("Copied!") {
		t.Fatal("a copy flashes Copied!")
	}
}

func TestAltScreenCopySelectionErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"generic", errors.New(""), "Copy failed"},
		{"specific", errors.New("Clipboard unavailable: install wl-clipboard"), "Clipboard unavailable: install wl-clipboard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyOnSelect := false
			h := newAltHarness(t, 80, 4, TuiAltScreenOptions{CopyOnSelect: &copyOnSelect, CopySelection: func(string) error { return tc.err }})
			var flashDuration int
			h.tui.flash = func(message string, durationMs int) {
				flashDuration = durationMs
				h.tui.Flash(message, durationMs)
			}
			h.tui.Add(NewText("alpha\nbeta\ngamma\ndelta"))
			h.start()
			h.send("\x1b[<0;1;1M", "\x1b[<32;4;2M", "\x1b[<0;4;2m")
			if h.tui.CopyActiveSelectionToClipboard() {
				t.Fatal("a failing clipboard reports failure")
			}
			h.render()
			if !h.viewportHas(tc.want) || (tc.want != "Copy failed" && h.viewportHas("Copy failed")) {
				t.Fatalf("flash rows = %q, want %q", h.viewport(), tc.want)
			}
			if flashDuration != altCopyErrorFlashDurationMS || h.wrote("\x1b]52;c;") {
				t.Fatalf("error flash duration = %d, OSC 52 written = %v", flashDuration, h.wrote("\x1b]52;c;"))
			}
		})
	}
}

func TestAltScreenSelectionUsesJavaScriptTrailingWhitespace(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.previousScreen = []string{"text\ufeff ", "end"}
	h.tui.selectionAnchor = &selectionPoint{row: 0, col: 0}
	h.tui.selectionFocus = &selectionPoint{row: 1, col: 3, boundary: true}
	got, ok := h.tui.activeSelectionTextLocked()
	if !ok || got != "text\nend" {
		t.Fatalf("selection = %q, %v; want JavaScript trimEnd semantics", got, ok)
	}
}

func TestAltScreenDoubleClickWordHasNoTrailingWhitespace(t *testing.T) {
	h := newAltHarness(t, 20, 1, TuiAltScreenOptions{})
	h.tui.Add(NewText("foo  bar"))
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<0;1;1m", "\x1b[<0;3;1M")
	if !h.wrote("foo\x1b[27m") {
		t.Fatal("a double-clicked word highlight ends at the word")
	}
}

func TestAltScreenDoubleClickJoinsSlashAndHyphenSegments(t *testing.T) {
	for _, tc := range []struct{ line, needle string }{
		{"extensions/starline/fixed-editor/compositor.ts", "starline"},
		{"earendil-works/pi-tui", "works"},
	} {
		var copied []string
		h := newAltHarness(t, 80, 1, TuiAltScreenOptions{CopySelection: func(text string) error { copied = append(copied, text); return nil }})
		h.tui.Add(NewText(tc.line))
		h.start()
		column := strings.Index(tc.line, tc.needle) + 1
		press := fmt.Sprintf("\x1b[<0;%d;1M", column)
		release := fmt.Sprintf("\x1b[<0;%d;1m", column)
		h.send(press, release, press, release)
		if !slices.Equal(copied, []string{tc.line}) {
			t.Fatalf("copied = %q, want %q", copied, tc.line)
		}
		h.tui.StopWithOptions(StopOptions{PreserveScreen: true})
	}
}

func TestAltScreenWordDragHighlightsWhitespaceSegment(t *testing.T) {
	h := newAltHarness(t, 20, 1, TuiAltScreenOptions{})
	h.tui.Add(NewText("foo  bar"))
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<0;1;1m", "\x1b[<0;2;1M", "\x1b[<32;4;1M")
	if !h.wrote("foo  \x1b[27m") {
		t.Fatal("a word drag over whitespace highlights the whole segment")
	}
}

func TestAltScreenMultiClickSelectsWordsAndLines(t *testing.T) {
	h := newAltHarness(t, 20, 2, TuiAltScreenOptions{})
	h.tui.Add(NewText("zero alpha beta\ngamma delta"))
	h.start()
	// The second click lands on a different character in alpha.
	h.send("\x1b[<0;6;1M", "\x1b[<0;6;1m", "\x1b[<0;10;1M", "\x1b[<0;10;1m")
	if !h.wrote(osc52("alpha")) {
		t.Fatal("a double click copies the word")
	}
	// A double-click drag includes each word touched.
	h.send("\x1b[<0;12;1M", "\x1b[<0;12;1m", "\x1b[<0;14;1M", "\x1b[<32;3;2M", "\x1b[<0;3;2m")
	if !h.wrote(osc52("beta\ngamma")) {
		t.Fatal("a double-click drag extends by whole words")
	}
	h.send("\x1b[<0;7;2M", "\x1b[<0;7;2m", "\x1b[<0;9;2M", "\x1b[<0;9;2m", "\x1b[<0;11;2M", "\x1b[<0;11;2m")
	if !h.wrote(osc52("gamma delta")) {
		t.Fatal("a triple click copies the line")
	}
}

func TestAltScreenFocusLossDoesNotRepaintIdleSelections(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText("alpha\nbeta\ngamma\ndelta"))
	h.start()
	renderScheduled(h.tui)
	h.tui.HandleViewportInput("\x1b[O")
	h.tui.HandleViewportInput("\x1b[I")
	if renderScheduled(h.tui) {
		t.Fatal("focus changes without a selection must not render")
	}
	// A completed click leaves a zero-width anchor; orphaned drag/release
	// events must not extend it.
	h.send("\x1b[<0;1;1M", "\x1b[<0;1;1m", "\x1b[<32;4;2M", "\x1b[<0;4;2m")
	if h.wrote("\x1b]52;c;") {
		t.Fatal("orphan events must not copy")
	}
	// Losing focus after a press without a drag cancels it without repainting.
	h.send("\x1b[<0;1;3M")
	renderScheduled(h.tui)
	h.tui.HandleViewportInput("\x1b[O")
	h.tui.HandleViewportInput("\x1b[I")
	if renderScheduled(h.tui) {
		t.Fatal("cancelling a zero-width press must not render")
	}
	h.send("\x1b[<32;4;2M", "\x1b[<0;4;2m")
	if h.wrote("\x1b]52;c;") || !h.wrote("\x1b[?1004h") {
		t.Fatal("a cancelled press must not copy; focus reporting is enabled")
	}
}

func TestAltScreenFocusLossClearsActiveSelection(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText("alpha\nbeta\ngamma\ndelta"))
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<32;4;2M")
	h.out.Reset()
	h.send("\x1b[O", "\x1b[I")
	if !h.wrote("alpha") || !h.wrote("beta") || h.wrote("\x1b[7m") {
		t.Fatalf("focus loss must repaint without the selection: %q", h.out.String())
	}
	h.send("\x1b[<32;4;2M", "\x1b[<0;4;2m")
	if h.wrote("\x1b]52;c;") {
		t.Fatal("orphan drag and release must not copy")
	}
}

func TestAltScreenRetainsCompletedSelectionAcrossFocusChanges(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText("alpha\nbeta\ngamma\ndelta"))
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<32;4;2M", "\x1b[<0;4;2m")
	renderScheduled(h.tui)
	h.tui.HandleViewportInput("\x1b[O")
	h.tui.HandleViewportInput("\x1b[I")
	if renderScheduled(h.tui) {
		t.Fatal("focus changes keep a completed selection without repainting")
	}
	h.out.Reset()
	h.tui.RepaintAll()
	if !h.wrote("alpha") || !h.wrote("beta") || !h.wrote("\x1b[7m") {
		t.Fatal("a full redraw still shows the completed selection")
	}
}

func TestAltScreenIgnoresHorizontalWheel(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText(numberedLines(8)))
	h.start()
	h.send("\x1b[<66;1;1M", "\x1b[<67;1;1M")
	if h.tui.ViewportTop() != 4 || !slices.Equal(h.viewport(), []string{"line 5", "line 6", "line 7", "line 8"}) {
		t.Fatalf("horizontal wheel scrolled: top=%d rows=%q", h.tui.ViewportTop(), h.viewport())
	}
}

func TestAltScreenMouseRegionClicksKeepDragSelection(t *testing.T) {
	h := newAltHarness(t, 20, 2, TuiAltScreenOptions{})
	clicks := 0
	h.tui.Add(NewMouseRegion(NewText("clickable\nselectable"), func(event TuiMouseEvent) *TuiMouseEventResult {
		if event.Type != MouseClick {
			return nil
		}
		clicks++
		return &TuiMouseEventResult{Handled: true}
	}))
	h.start()
	h.send("\x1b[<0;2;1M", "\x1b[<0;2;1m")
	if clicks != 1 {
		t.Fatalf("clicks = %d, want 1", clicks)
	}
	h.send("\x1b[<0;1;1M", "\x1b[<32;4;2M", "\x1b[<0;4;2m")
	if clicks != 1 || !h.wrote("\x1b]52;c;") {
		t.Fatalf("a drag selects and copies without clicking: clicks=%d", clicks)
	}
}

func TestAltScreenFocusesAndCapturesMouseAwareComponents(t *testing.T) {
	h := newAltHarness(t, 20, 2, TuiAltScreenOptions{})
	var events []TuiMouseEventType
	component := &funcMouseComponent{lines: []string{"control"}, handle: func(event TuiMouseEvent) *TuiMouseEventResult {
		events = append(events, event.Type)
		if event.Type == MousePress {
			return &TuiMouseEventResult{Handled: true, Capture: true, Focus: true}
		}
		return &TuiMouseEventResult{Handled: true}
	}}
	h.tui.Add(component)
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<32;5;2M", "\x1b[<0;5;2m")
	if !slices.Equal(events, []TuiMouseEventType{MousePress, MouseDrag, MouseRelease}) {
		t.Fatalf("events = %v", events)
	}
	if h.tui.FocusedComponent() != component {
		t.Fatal("a focus result focuses the component")
	}
}

func TestAltScreenReportsComponentClickCounts(t *testing.T) {
	h := newAltHarness(t, 20, 1, TuiAltScreenOptions{})
	var counts []int
	h.tui.Add(&funcMouseComponent{lines: []string{"control"}, handle: func(event TuiMouseEvent) *TuiMouseEventResult {
		switch event.Type {
		case MousePress:
			return &TuiMouseEventResult{Handled: true}
		case MouseClick:
			counts = append(counts, event.ClickCount)
			return &TuiMouseEventResult{Handled: true}
		}
		return nil
	}})
	h.start()
	for range 3 {
		h.send("\x1b[<0;1;1M", "\x1b[<0;1;1m")
	}
	if !slices.Equal(counts, []int{1, 2, 3}) {
		t.Fatalf("click counts = %v", counts)
	}
}

func TestAltScreenHandledMotionDoesNotRender(t *testing.T) {
	h := newAltHarness(t, 20, 2, TuiAltScreenOptions{})
	h.tui.Add(&funcMouseComponent{lines: []string{"hover target"}, handle: func(event TuiMouseEvent) *TuiMouseEventResult {
		if event.Type == MouseMove {
			return &TuiMouseEventResult{Handled: true}
		}
		return nil
	}})
	h.start()
	renderScheduled(h.tui)
	h.tui.HandleViewportInput("\x1b[<35;1;1M")
	if renderScheduled(h.tui) {
		t.Fatal("handled no-op motion must not render")
	}
}

func TestAltScreenComponentsConsumeWheelFirst(t *testing.T) {
	h := newAltHarness(t, 20, 3, TuiAltScreenOptions{})
	wheels := 0
	h.tui.Add(NewMouseRegion(NewText(numberedLines(8)), func(event TuiMouseEvent) *TuiMouseEventResult {
		if event.Type != MouseWheel {
			return nil
		}
		wheels++
		return &TuiMouseEventResult{Handled: true}
	}))
	h.start()
	top := h.tui.ViewportTop()
	h.send("\x1b[<64;1;1M")
	if wheels != 1 || h.tui.ViewportTop() != top {
		t.Fatalf("wheels=%d top %d -> %d", wheels, top, h.tui.ViewportTop())
	}
}

// TestAltScreenFocusedOverlayOwnsWheelAndKeys ports upstream "gives wheel and
// viewport keys to a focused overlay": the viewport declines them so the
// driver delivers them to the overlay.
func TestAltScreenFocusedOverlayOwnsWheelAndKeys(t *testing.T) {
	h := newAltHarness(t, 20, 6, TuiAltScreenOptions{})
	h.tui.Add(NewText(numberedLines(12)))
	h.start()
	topBefore := h.tui.ViewportTop()
	overlay := &inputRecorder{lines: []string{"overlay"}}
	handle := h.tui.OpenOverlay(overlay, OverlayOptions{})
	h.render()
	keys := []string{"\x1b[5~", "\x1b[6~", "\x1bOH", "\x1bOF", "\x1b[<64;10;3M"}
	h.send(keys...)
	if !slices.Equal(overlay.inputs, keys) || h.tui.ViewportTop() != topBefore {
		t.Fatalf("overlay inputs=%q top %d -> %d", overlay.inputs, topBefore, h.tui.ViewportTop())
	}
	handle.Close()
	h.send("\x1b[5~")
	if h.tui.ViewportTop() >= topBefore {
		t.Fatal("closing the overlay returns keys to the viewport")
	}
}

// TestAltScreenUnfocusedOverlaysKeepViewportScrolling ports upstream "keeps
// viewport scrolling when an overlay is not focused".
func TestAltScreenUnfocusedOverlaysKeepViewportScrolling(t *testing.T) {
	h := newAltHarness(t, 20, 6, TuiAltScreenOptions{})
	editor := &inputRecorder{}
	h.tui.Add(NewText(numberedLines(12)))
	h.tui.SetFocus(editor)
	h.start()
	topBefore := h.tui.ViewportTop()
	hidden := h.tui.OpenOverlay(&inputRecorder{lines: []string{"hidden"}}, OverlayOptions{})
	hidden.setHidden(true)
	nonCapturing := &inputRecorder{lines: []string{"non-capturing"}}
	h.tui.OpenOverlay(nonCapturing, OverlayOptions{nonCapturing: true})
	unfocused := &inputRecorder{lines: []string{"unfocused"}}
	h.tui.OpenOverlay(unfocused, OverlayOptions{}).unfocus()
	h.render()
	h.send("\x1b[5~", "\x1b[<64;10;3M")
	if h.tui.ViewportTop() >= topBefore || len(nonCapturing.inputs) != 0 || len(unfocused.inputs) != 0 {
		t.Fatalf("top %d -> %d, overlays got %q %q", topBefore, h.tui.ViewportTop(), nonCapturing.inputs, unfocused.inputs)
	}
}

// TestAltScreenDelegatingOverlayKeepsFocusOnNestedInputClick ports upstream
// mouse-components.test.ts "keeps a delegating overlay focused when its nested
// input is clicked".
func TestAltScreenDelegatingOverlayKeepsFocusOnNestedInputClick(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	input := NewInput(InputOptions{})
	input.SetText("hi")
	overlay := &delegatingOverlay{Container: NewContainer(input), input: input}
	h.start()
	h.tui.OpenOverlay(overlay, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(20)})
	h.render()
	h.send("\x1b[<0;5;1M", "\x1b[<0;5;1m", "!")
	if input.Text() != "hi!" || h.tui.FocusedComponent() != overlay {
		t.Fatalf("value=%q focused=%v", input.Text(), h.tui.FocusedComponent())
	}
}

// delegatingOverlay forwards keys to its nested input, like upstream's
// InputOverlay test component.
type delegatingOverlay struct {
	*Container
	input *TextInput
}

func (o *delegatingOverlay) HandleInput(data string) { o.input.HandleInput(data) }

// funcMouseComponent is a leaf component with a closure mouse handler.
type funcMouseComponent struct {
	invalidatable
	lines  []string
	handle func(event TuiMouseEvent) *TuiMouseEventResult
}

func (c *funcMouseComponent) Render(int) []string { return c.lines }

func (c *funcMouseComponent) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	result := c.handle(event)
	if result == nil {
		return nil
	}
	return &TuiMouseDispatchResult{TuiMouseEventResult: *result}
}
