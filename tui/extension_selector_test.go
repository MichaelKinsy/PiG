package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestExtensionSelectorMultilineTitleRendersOneLinePerSegment(t *testing.T) {
	selector := NewExtensionSelector("first\nsecond", []string{"Continue", "Cancel"})
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
	selector := NewExtensionSelector(title, []string{"Trust"})
	wantTitle := NewPaddedText(theme.FgText("accent", "\x1b[1mfirst\x1b[22m\n\x1b[1m\x1b[22m\n\x1b[1msecond\x1b[22m"), 1, 0, nil).Render(80)
	got := selector.Render(80)
	if !slices.Equal(got[2:2+len(wantTitle)], wantTitle) {
		t.Fatalf("title rows = %q, want %q", got[2:2+len(wantTitle)], wantTitle)
	}
	wantHint := NewPaddedText(theme.FgText("dim", "↑↓")+theme.FgText("muted", " navigate")+"  "+theme.FgText("dim", "enter")+theme.FgText("muted", " select")+"  "+theme.FgText("dim", "escape/ctrl+c")+theme.FgText("muted", " cancel"), 1, 0, nil).Render(80)
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
	sel := NewExtensionSelector("Pick one", []string{"a", "b"}, func() { called++ })

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
	sel := NewExtensionSelector("Pick one", []string{"a", "b"})

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
	selector := NewExtensionSelector("Bug report", []string{"Export as Zip", "Cancel"})
	selector.SetDescription(description)
	editor := NewExtensionEditorComponent("Report a bug", "")
	editor.SetDescription(description)
	for name, lines := range map[string][]string{"selector": selector.Render(40), "editor": editor.Render(40)} {
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
	plainSelector := NewExtensionSelector("Bug report", []string{"Export as Zip"})
	if got, want := len(selector.Render(40))-len(plainSelector.Render(40)), 1+len(NewPaddedText(description, 1, 0, nil).Render(40))+1; got != want {
		t.Fatalf("description added %d lines, want %d", got, want)
	}
}

// Upstream extension-selector.ts renders the key hints as Text(hint, 1, 0), so
// at 40 cells the hint wraps inside one cell of padding on each side instead of
// producing a 48-cell row.
func TestExtensionSelectorHintWrapsAtNarrowWidth(t *testing.T) {
	selector := NewExtensionSelector("Pick", []string{"a", "b"})
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
	selector := NewExtensionSelector("Pick one", []string{"first option", "second option", "third option"})
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
	selector := NewExtensionSelector("Pick", []string{"first option", "second option"})
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
	plain := NewExtensionSelector("Pick", withOption)
	nulled := NewExtensionSelector("Pick", withOption)
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
	plain := NewExtensionSelector("Pick", options)
	withPreview := NewExtensionSelector("Pick", options)
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
