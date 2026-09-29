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
