package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TreeSelectorComponent must not render a line wider than the width it is given, down
// to a single cell, in every state: rows, no rows, and label editing.
func TestTreeSelectRowsStayWithinWidth(t *testing.T) {
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "a", label: "a fairly long first entry", kids: []TreeNode{
			&fakeNode{id: "a1", label: "child one with text"},
			&fakeNode{id: "a2", label: "child two 漢字 wide"},
		}},
	}}
	states := map[string]func() *TreeSelectorComponent{
		"rows":  func() *TreeSelectorComponent { return NewTreeSelectorComponent("", root) },
		"empty": func() *TreeSelectorComponent { return NewTreeSelectorComponent("", &fakeNode{id: "r"}) },
		"label": func() *TreeSelectorComponent {
			ts := NewTreeSelectorComponent("", root)
			ts.labelInput = NewInput(InputOptions{})
			ts.labelInput.SetValue("a long label being typed")
			return ts
		},
	}
	for name, build := range states {
		for width := 1; width <= 12; width++ {
			for i, line := range build().Render(width) {
				if got := widthx.VisibleWidth(line); got > width {
					t.Errorf("%s width %d: line %d is %d cells: %q", name, width, i, got, line)
				}
			}
		}
	}
}

// tree-selector.ts:1379-1393: TreeSelectorComponent extends Container with spacer, border, title, help, search line, border, spacer, tree container, label container, spacer, border.
func TestTreeSelectorIsAContainerOfPisConstructorChildren(t *testing.T) {
	ts := NewTreeSelectorComponent("", &fakeNode{id: "r", kids: []TreeNode{&fakeNode{id: "a", label: "entry"}}})
	if got := len(ts.Children()); got != 11 {
		t.Fatalf("children = %d, want 11", got)
	}
	if len(ts.treeContainer.Children()) != 1 || len(ts.labelInputContainer.Children()) != 1 {
		t.Fatal("tree and label containers each hold one child")
	}
	if got := strings.Join(ts.Render(60), "\n"); strings.Contains(got, "Label (empty to remove)") || !strings.Contains(got, "entry") {
		t.Fatalf("tree mode render wrong: %s", got)
	}
	ts.labelInput = NewInput(InputOptions{})
	if got := strings.Join(ts.Render(60), "\n"); !strings.Contains(got, "Label (empty to remove)") || strings.Contains(got, "(1/1)") {
		t.Fatalf("label mode render wrong: %s", got)
	}
}
