package tui

// pi: packages/coding-agent/src/modes/interactive/components/keybinding-hints.ts
// pi: packages/coding-agent/src/modes/interactive/components/extension-selector.ts

// pi: packages/coding-agent/src/modes/interactive/components/countdown-timer.ts

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestExtensionSelectorMultilineTitleRendersOneLinePerSegment(t *testing.T) {
	selector := NewExtensionSelectorComponent("first\nsecond", []string{"Continue", "Cancel"}, nil, nil)
	got := strings.Join(selector.Render(80), "\n")
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("multiline title missing segments:\n%s", got)
	}
	for _, line := range selector.Render(80) {
		if strings.Contains(line, "first") && strings.Contains(line, "second") {
			t.Fatalf("multiline title rendered both segments on one line: %q", line)
		}
	}
}

func TestExtensionSelectorMultilineTitleUsesScopedBoldAndDimKeys(t *testing.T) {
	// Pi 0.87.1 extension-selector.ts:48 uses theme.bold (chalk), which closes/reopens at newlines; keybinding-hints.ts:42-47 uses dim keys and muted descriptions.
	theme := ActiveTheme()
	title := "first\n\nsecond"
	selector := NewExtensionSelectorComponent(title, []string{"Trust"}, nil, nil)
	wantTitle := NewPaddedText(theme.Fg("accent", "\x1b[1mfirst\x1b[22m\n\x1b[1m\x1b[22m\n\x1b[1msecond\x1b[22m"), 1, 0, nil).Render(80)
	got := selector.Render(80)
	if !slices.Equal(got[2:2+len(wantTitle)], wantTitle) {
		t.Fatalf("title rows = %q, want %q", got[2:2+len(wantTitle)], wantTitle)
	}
	wantHint := NewPaddedText(theme.Fg("dim", "↑↓")+theme.Fg("muted", " navigate")+"  "+theme.Fg("dim", "enter")+theme.Fg("muted", " select")+"  "+theme.Fg("dim", "escape/ctrl+c")+theme.Fg("muted", " cancel"), 1, 0, nil).Render(80)
	if !slices.Equal(got[len(got)-3:len(got)-2], wantHint) {
		t.Fatalf("hint = %q, want %q", got[len(got)-3:len(got)-2], wantHint)
	}
}

func TestExtensionSelectorHandleInput_TogglesToolsExpandedShortcut(t *testing.T) {
	// Pi's coding-agent installs its merged app/tui manager before opening dialogs.
	previous := GetKeybindings()
	SetKeybindings(NewTUIKeybindingsManager(map[string][]string{"app.tools.expand": {"ctrl+o"}}))
	t.Cleanup(func() { SetKeybindings(previous) })
	called := 0
	sel := NewExtensionSelectorComponent("Pick one", []string{"a", "b"}, nil, nil, ExtensionSelectorOptions{OnToggleToolsExpanded: func() { called++ }})

	sel.HandleInput("\x0f") // default ctrl+o → app.tools.expand

	if called != 1 {
		t.Fatalf("toggle callback calls = %d, want 1", called)
	}
	if sel.Done() {
		t.Fatal("selector should remain open after tools-expand toggle")
	}
	if got := sel.SelectedIndex(); got != 0 {
		t.Fatalf("selected index = %d, want 0", got)
	}
}

func TestExtensionSelectorHandleInput_CancelStillWins(t *testing.T) {
	sel := NewExtensionSelectorComponent("Pick one", []string{"a", "b"}, nil, nil)

	sel.HandleInput("\x1b")

	if !sel.Done() {
		t.Fatal("selector should be done after cancel")
	}
	if !sel.Cancelled() {
		t.Fatal("selector should be cancelled after escape")
	}
}

// Upstream extension-selector.ts and extension-editor.ts render an optional
// description between the title and the body, after a blank spacer, wrapped
// with one cell of padding.
func TestExtensionDialogsRenderDescriptionUnderTitle(t *testing.T) {
	description := "This report stays on your machine and is never uploaded anywhere at all."
	selector := NewExtensionSelectorComponent("Bug report", []string{"Export as Zip", "Cancel"}, nil, nil)
	selector.SetDescription(description)
	for name, lines := range map[string][]string{"selector": selector.Render(40)} {
		plain := make([]string, len(lines))
		for i, line := range lines {
			plain[i] = strings.TrimRight(stripANSI(line), " ")
		}
		title := slices.IndexFunc(plain, func(line string) bool {
			return strings.Contains(line, "Bug report") || strings.Contains(line, "Report a bug")
		})
		if title < 0 || plain[title+1] != "" || plain[title+2] != " This report stays on your machine and" {
			t.Fatalf("%s lines = %q", name, plain)
		}
		for _, line := range lines[title+2 : title+4] {
			if w := widthx.VisibleWidth(line); w > 40 {
				t.Fatalf("%s description line exceeds width: %d", name, w)
			}
		}
	}
	plainSelector := NewExtensionSelectorComponent("Bug report", []string{"Export as Zip"}, nil, nil)
	if got, want := len(selector.Render(40))-len(plainSelector.Render(40)), 1+len(NewPaddedText(description, 1, 0, nil).Render(40))+1; got != want {
		t.Fatalf("description added %d lines, want %d", got, want)
	}
}

// Upstream extension-selector.ts renders the key hints as Text(hint, 1, 0), so
// at 40 cells the hint wraps inside one cell of padding on each side instead of
// producing a 48-cell row.
func TestExtensionSelectorHintWrapsAtNarrowWidth(t *testing.T) {
	selector := NewExtensionSelectorComponent("Pick", []string{"a", "b"}, nil, nil)
	var hint []string
	for _, line := range selector.Render(40) {
		plain := stripANSI(line)
		if w := widthx.VisibleWidth(line); w > 40 {
			t.Fatalf("row is %d cells wide in a 40-cell render: %q", w, plain)
		}
		if strings.Contains(plain, "navigate") || strings.Contains(plain, "cancel") {
			hint = append(hint, strings.TrimRight(plain, " "))
		}
	}
	want := []string{" ↑↓ navigate  enter select", " escape/ctrl+c cancel"}
	if !slices.Equal(hint, want) {
		t.Fatalf("hint rows = %q, want %q", hint, want)
	}
}

// ExtensionSelectorComponent extends Container (extension-selector.ts:20): border, spacer, title, spacer, list container,
// spacer, hint, spacer, border; a description adds a spacer and a Text after the title, and moving the selection rebuilds
// the list container's rows (updateList).
func TestExtensionSelectorComponentChildrenFollowUpstream(t *testing.T) {
	e := NewExtensionSelectorComponent("Pick", []string{"a", "b", "c"}, nil, nil)
	if got := len(e.Children()); got != 9 {
		t.Fatalf("children = %d, want 9", got)
	}
	if got := len(e.listContainer.Children()); got != 3 {
		t.Fatalf("list rows = %d, want 3", got)
	}
	e.SetDescription("why")
	if got := len(e.Children()); got != 11 {
		t.Fatalf("children with description = %d, want 11", got)
	}
	before := strings.Join(e.Render(40), "\n")
	e.HandleInput("j")
	if strings.Join(e.Render(40), "\n") == before {
		t.Fatal("moving the selection did not change the render")
	}
	e.SetDescription("")
	if got := len(e.Children()); got != 9 {
		t.Fatalf("children after clearing the description = %d, want 9", got)
	}
}

// previewMarks are the preview lines the tests below draw: 8 cells a line, so the right column is 8 cells plus the
// gap that separates it from the option rows.
var previewMarks = []string{"pigA top", "pigA bot", "pigB top", "pigB bot"}

func testPreview(selected int) []string {
	return []string{previewMarks[selected*2], previewMarks[selected*2+1]}
}

// lineIndexOf returns the index of the rendered line carrying text.
func lineIndexOf(t *testing.T, lines []string, text string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(stripANSI(line), text) {
			return i
		}
	}
	t.Fatalf("no line carries %q in %q", text, lines)
	return -1
}

// A preview draws right after the option rows: the rows keep the cells left of it, the preview line facing each row
// takes the rightmost cells, and the highlighted option chooses which preview the column shows.
func TestExtensionSelectorPreviewDrawsAfterTheOptionRows(t *testing.T) {
	selector := NewExtensionSelectorComponent("Pick one", []string{"first option", "second option", "third option"}, nil, nil)
	selector.SetPreview(testPreview)
	const width, cells = 60, 8
	lines := selector.Render(width)
	for i, line := range lines {
		if got := widthx.VisibleWidth(line); got > width {
			t.Fatalf("line %d is %d cells wide: %q", i, got, stripANSI(line))
		}
	}
	first, second, third := lineIndexOf(t, lines, "first option"), lineIndexOf(t, lines, "second option"), lineIndexOf(t, lines, "third option")
	if second != first+1 || third != second+1 {
		t.Fatalf("option rows at %d, %d, %d: the rows must stay one line each at width %d", first, second, third, width)
	}
	// The preview is top-aligned with the first row.
	if got := lines[first]; !strings.Contains(got, "pigA top") {
		t.Fatalf("first row = %q, want the preview's first line beside it", got)
	}
	if got := lines[second]; !strings.Contains(got, "pigA bot") {
		t.Fatalf("second row = %q, want the preview's second line beside it", got)
	}
	if got := lines[third]; strings.Contains(got, "pigA") {
		t.Fatalf("third row = %q, want the cells right of it blank", got)
	}
	prefix, _, ok := strings.Cut(lines[first], "pigA top")
	if !ok {
		t.Fatalf("first row = %q", lines[first])
	}
	// The longest row is "  second option": the left padding cell, the two-cell cursor column and 13 letters.
	if want := 1 + 2 + len("second option") + 1 + previewGapCells; widthx.VisibleWidth(prefix) != want {
		t.Fatalf("preview starts at column %d, want %d (the longest row plus %d cells)", widthx.VisibleWidth(prefix), want, previewGapCells)
	}
	if got := widthx.VisibleWidth(lines[first]); got != width {
		t.Fatalf("preview row is %d cells wide, want %d", got, width)
	}
	_ = cells

	// The column follows the highlighted option.
	selector.HandleInput("j")
	lines = selector.Render(width)
	if got := lines[lineIndexOf(t, lines, "first option")]; !strings.Contains(got, "pigB top") {
		t.Fatalf("after moving down the first row = %q, want the second option's preview", got)
	}
}

// The preview column never squeezes the option list into a column of words: below the floor width the selector draws
// alone, as upstream's does.
func TestExtensionSelectorPreviewDropsWhenTheBodyWouldSqueeze(t *testing.T) {
	selector := NewExtensionSelectorComponent("Pick", []string{"first option", "second option"}, nil, nil)
	selector.SetPreview(func(int) []string { return []string{"pighead!"} })
	const cells = 8
	floor := previewFloorCells + cells + previewGapCells + previewRightCells
	for _, tc := range []struct {
		width int
		want  bool
	}{{floor - 1, false}, {floor, true}, {80, true}} {
		got := strings.Contains(strings.Join(selector.Render(tc.width), "\n"), "pighead!")
		if got != tc.want {
			t.Errorf("width %d: preview drawn = %v, want %v", tc.width, got, tc.want)
		}
	}
}

// A selector without a preview keeps upstream's render: every option row wraps within the full width.
func TestExtensionSelectorWithoutPreviewKeepsFullWidthRows(t *testing.T) {
	withOption := []string{"an option label that is long enough to wrap at a narrow render width"}
	plain := NewExtensionSelectorComponent("Pick", withOption, nil, nil)
	nulled := NewExtensionSelectorComponent("Pick", withOption, nil, nil)
	nulled.SetPreview(nil)
	for _, width := range []int{20, 40, 80} {
		if !slices.Equal(plain.Render(width), nulled.Render(width)) {
			t.Fatalf("SetPreview(nil) changed the render at width %d", width)
		}
		for i, line := range plain.Render(width) {
			if got := widthx.VisibleWidth(line); got > width {
				t.Fatalf("width %d: line %d is %d cells wide: %q", width, i, got, stripANSI(line))
			}
		}
	}
}

// A preview that draws nothing for the highlighted row falls back to upstream's full-width rows for that row: the
// column is a property of what the row has to show, not of the dialog.
func TestExtensionSelectorEmptyPreviewKeepsFullWidthRows(t *testing.T) {
	options := []string{"first option", "an option label that is long enough to wrap at a narrow render width", "Create your own..."}
	plain := NewExtensionSelectorComponent("Pick", options, nil, nil)
	withPreview := NewExtensionSelectorComponent("Pick", options, nil, nil)
	withPreview.SetPreview(func(selected int) []string {
		if selected == len(options)-1 {
			return nil
		}
		return []string{"pigcell!"}
	})
	const width = 80
	previewed := false
	for i, line := range withPreview.Render(width) {
		before, _, ok := strings.Cut(line, "pigcell!")
		if !ok {
			continue
		}
		previewed = true
		// The long row would push the preview past the dialog, so the list wraps at the room left of it.
		want := width - 8 - previewRightCells
		if got := widthx.VisibleWidth(before); got != want {
			t.Fatalf("line %d draws the preview at column %d, want %d", i, got, want)
		}
		break
	}
	if !previewed {
		t.Fatalf("the highlighted row draws no preview at width %d", width)
	}
	for range len(options) - 1 {
		withPreview.HandleInput("j")
		plain.HandleInput("j")
	}
	got := withPreview.Render(width)
	if strings.Contains(strings.Join(got, "\n"), "pigcell!") {
		t.Fatalf("the row without a preview keeps the column:\n%q", got)
	}
	if !slices.Equal(plain.Render(width), got) {
		t.Fatalf("rows without a preview = %q, want the plain selector's %q", got, plain.Render(width))
	}
}

// packages/coding-agent/src/modes/interactive/components/extension-selector.ts:102-117 handleInput: confirm (and a bare "\n") call
// onSelect with the highlighted option, cancel calls onCancel, navigation calls neither; an empty option list selects nothing.
func TestExtensionSelectorComponentCallsSelectAndCancelCallbacks(t *testing.T) {
	var selected []string
	cancels := 0
	onSelect := func(option string) { selected = append(selected, option) }
	e := NewExtensionSelectorComponent("Pick", []string{"a", "b", "c"}, onSelect, func() { cancels++ })
	e.HandleInput("j")
	e.HandleInput("\x1b[B")
	e.HandleInput("\x1b[A")
	if len(selected) != 0 || cancels != 0 {
		t.Fatalf("navigation called a callback: %v %d", selected, cancels)
	}
	e.HandleInput("\r")
	if !slices.Equal(selected, []string{"b"}) || cancels != 0 || !e.Done() || e.Cancelled() {
		t.Fatalf("confirm: selected %v cancels %d done %v cancelled %v", selected, cancels, e.Done(), e.Cancelled())
	}

	c := NewExtensionSelectorComponent("Pick", []string{"a"}, onSelect, func() { cancels++ })
	c.HandleInput("\x1b")
	if cancels != 1 || len(selected) != 1 || !c.Cancelled() {
		t.Fatalf("cancel: selected %v cancels %d cancelled %v", selected, cancels, c.Cancelled())
	}

	n := NewExtensionSelectorComponent("Pick", []string{"a"}, onSelect, nil)
	n.HandleInput("\n")
	if !slices.Equal(selected, []string{"b", "a"}) {
		t.Fatalf("a bare newline selects the highlighted option: %v", selected)
	}

	empty := NewExtensionSelectorComponent("Pick", nil, onSelect, func() { cancels++ })
	empty.HandleInput("\r")
	if len(selected) != 2 || empty.Done() {
		t.Fatalf("an empty selector selected %v done %v", selected, empty.Done())
	}
}

// extension-selector.ts:30-57: opts.description is a text row under the title (opts.onToggleToolsExpanded is covered by
// TestExtensionSelectorHandleInput_TogglesToolsExpandedShortcut), and a positive opts.timeout with opts.tui starts a CountdownTimer whose first tick titles the selector at once and
// requests no render; expiry calls onCancel; dispose stops the timer. No timeout or no tui means no timer.
func TestExtensionSelectorComponentOptions(t *testing.T) {
	d := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil, ExtensionSelectorOptions{Description: "why"})
	if plain := stripANSI(strings.Join(d.Render(60), "\n")); !strings.Contains(plain, "why") {
		t.Fatalf("description missing:\n%s", plain)
	}

	r := &renderCounter{renders: make(chan struct{}, 4)}
	loop := make(chan func(), 4)
	cancels := 0
	e := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, func() { cancels++ }, ExtensionSelectorOptions{
		TUI: r, Timeout: time.Second, Dispatch: func(f func()) { loop <- f },
	})
	defer e.Dispose()
	if !strings.Contains(stripANSI(strings.Join(e.Render(60), "\n")), "Pick (1s)") {
		t.Fatalf("first tick missing:\n%s", strings.Join(e.Render(60), "\n"))
	}
	select {
	case <-r.renders:
		t.Fatal("the constructor's first tick requested a render")
	default:
	}
	select {
	case tick := <-loop:
		tick()
	case <-time.After(10 * time.Second):
		t.Fatal("countdown did not tick")
	}
	if cancels != 1 || !e.Done() || !e.Cancelled() {
		t.Fatalf("expiry: onCancel %d done %v cancelled %v", cancels, e.Done(), e.Cancelled())
	}
	if len(r.renders) == 0 {
		t.Fatal("expiry tick requested no render")
	}

	for name, opts := range map[string]ExtensionSelectorOptions{
		"no tui":     {Timeout: time.Second},
		"no timeout": {TUI: r},
		"negative":   {TUI: r, Timeout: -time.Second},
	} {
		if NewExtensionSelectorComponent("Pick", nil, nil, nil, opts).countdown != nil {
			t.Fatalf("%s started a countdown", name)
		}
	}
	h := NewExtensionSelectorComponent("Pick", nil, nil, nil, ExtensionSelectorOptions{TUI: r, Timeout: time.Hour})
	timer := h.countdown
	h.Dispose()
	if h.countdown != nil || timer == nil || !timer.stopped {
		t.Fatal("Dispose left the countdown running")
	}
}
