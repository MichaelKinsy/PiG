package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestTreeSelect_EmptyStateMatchesUpstreamTextShape(t *testing.T) {
	ts := NewTreeSelectorComponent("", &fakeNode{id: "root"})
	lines := ts.Render(100)
	joined := strings.Join(lines, "\n")

	for _, want := range []string{
		"Session Tree",
		"Type to search:",
		"No entries found",
		"(0/0)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("empty tree render missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "(empty tree)") {
		t.Fatalf("legacy empty-tree marker leaked:\n%s", joined)
	}
}

// pi main 2cfab7c26 test/tree-selector.test.ts "search cursor" (#9157).
func TestTreeSelectSearchCursor(t *testing.T) {
	cursorLines := func(lines []string) (found []string, index int) {
		index = -1
		for i, line := range lines {
			if strings.Contains(line, widthx.CursorMarker) {
				found = append(found, line)
				if index < 0 {
					index = i
				}
			}
		}
		return found, index
	}
	t.Run("renders the cursor only while the selector is focused", func(t *testing.T) {
		ts := NewTreeSelectorComponent("", &fakeNode{id: "root"})
		if lines, _ := cursorLines(ts.Render(80)); len(lines) != 0 {
			t.Fatalf("unfocused selector draws the cursor: %q", lines)
		}
		ts.SetFocused(true)
		lines, _ := cursorLines(ts.Render(80))
		if len(lines) != 1 {
			t.Fatalf("focused selector draws %d cursor lines, want 1", len(lines))
		}
		if got := widthx.StripAnsi(strings.Replace(lines[0], widthx.CursorMarker, "", 1)); got != "  Type to search:  " {
			t.Fatalf("cursor line = %q", got)
		}
		if !strings.Contains(lines[0], widthx.CursorMarker+"\x1b[7m \x1b[27m") {
			t.Fatalf("cursor cell missing: %q", lines[0])
		}
		ts.SetFocused(false)
		if lines, _ := cursorLines(ts.Render(80)); len(lines) != 0 {
			t.Fatalf("selector that lost focus still draws the cursor: %q", lines)
		}
	})
	t.Run("moves focus to the label input and restores it after cancellation", func(t *testing.T) {
		useTreeKeybindings(t, nil)
		ts := NewTreeSelectorComponent("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "a", label: "hello"}}})
		ts.OnLabelEdit = func(string, string) {}
		ts.SetFocused(true)
		ts.HandleInput("L") // app.tree.editLabel
		rendered := ts.Render(80)
		lines, index := cursorLines(rendered)
		if len(lines) != 1 || index <= 0 || !strings.Contains(widthx.StripAnsi(rendered[index-1]), "Label (empty to remove):") {
			t.Fatalf("label input cursor: %d lines at %d:\n%s", len(lines), index, strings.Join(rendered, "\n"))
		}
		ts.HandleInput("\x1b")
		rendered = ts.Render(80)
		lines, index = cursorLines(rendered)
		if len(lines) != 1 || !strings.Contains(widthx.StripAnsi(rendered[index]), "Type to search:") {
			t.Fatalf("search cursor after cancel: %d lines:\n%s", len(lines), strings.Join(rendered, "\n"))
		}
	})
	t.Run("keeps the cursor within narrow terminal widths", func(t *testing.T) {
		ts := NewTreeSelectorComponent("", &fakeNode{id: "root"})
		ts.SetFocused(true)
		ts.HandleInput("a long search query")
		lines, _ := cursorLines(ts.Render(10))
		if len(lines) != 1 || widthx.VisibleWidth(lines[0]) > 10 {
			t.Fatalf("narrow cursor line %q (width %d)", lines, widthx.VisibleWidth(lines[0]))
		}
	})
}
