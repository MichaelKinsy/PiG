package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TreeSelect must not render a line wider than the width it is given, down
// to a single cell, in every state: rows, no rows, and label editing.
func TestTreeSelectRowsStayWithinWidth(t *testing.T) {
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "a", label: "a fairly long first entry", kids: []TreeNode{
			&fakeNode{id: "a1", label: "child one with text"},
			&fakeNode{id: "a2", label: "child two 漢字 wide"},
		}},
	}}
	states := map[string]func() *TreeSelect{
		"rows":  func() *TreeSelect { return NewTreeSelect("", root) },
		"empty": func() *TreeSelect { return NewTreeSelect("", &fakeNode{id: "r"}) },
		"label": func() *TreeSelect {
			ts := NewTreeSelect("", root)
			ts.editingLabel = true
			ts.labelBuf = "a long label being typed"
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
