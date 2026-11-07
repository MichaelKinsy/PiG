package tui

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// scriptedChild renders the lines it holds and records the widths it was asked for.
type scriptedChild struct {
	lines  []string
	widths []int
}

func (c *scriptedChild) Render(width int) []string {
	c.widths = append(c.widths, width)
	return c.lines
}
func (c *scriptedChild) Invalidate() {}

// components/box.ts: addChild, removeChild and clear drop the render cache (invalidateCache); Pig's Box also marks itself dirty so the surface that draws it
// redraws it (D91, invalidatable). Upstream does not invalidate the children there, so only the dirty mark is asserted.
func TestBoxChangesMarkItDirty(t *testing.T) {
	child := &scriptedChild{lines: []string{"x"}}
	var component Component = NewBox()
	box, ok := component.(*Box)
	if !ok {
		t.Fatalf("NewBox returned %T, want a *Box", component)
	}
	for _, step := range []struct {
		name string
		do   func()
	}{
		{"AddChild", func() { box.AddChild(child) }},
		{"RemoveChild", func() { box.RemoveChild(child) }},
		{"Clear", func() { box.Clear() }},
		{"Invalidate", func() { box.Invalidate() }},
	} {
		box.NeedsRedraw()
		if step.name == "RemoveChild" {
			box.AddChild(child)
			box.NeedsRedraw()
		}
		step.do()
		if !box.IsDirty() {
			t.Errorf("%s left the box clean", step.name)
		}
	}
}

// box.ts:35-40 removeChild: indexOf finds the first occurrence and splice removes that one only; a component that is not a child changes nothing.
func TestBoxRemoveChildRemovesOnlyTheFirstOccurrence(t *testing.T) {
	a, b := &scriptedChild{lines: []string{"a"}}, &scriptedChild{lines: []string{"b"}}
	box := NewPaddedBox(0, 0, nil)
	for _, child := range []Component{a, b, a} {
		box.AddChild(child)
	}
	box.RemoveChild(a)
	if got := box.Render(4); !slices.Equal(got, []string{"b   ", "a   "}) {
		t.Fatalf("after removing one of two occurrences: %q, want [b a] padded to the width", got)
	}
}

// box.ts render: the cache is used only while the child lines are identical, including their number: a child that now returns a prefix of its earlier lines
// renders the shorter result.
func TestBoxRenderCacheMissesWhenAChildReturnsFewerLines(t *testing.T) {
	child := &scriptedChild{lines: []string{"one", "two"}}
	box := NewPaddedBox(0, 0, nil)
	box.AddChild(child)
	if got := box.Render(5); len(got) != 2 {
		t.Fatalf("first render = %q", got)
	}
	child.lines = []string{"one"}
	if got := box.Render(5); !slices.Equal(got, []string{"one  "}) {
		t.Fatalf("render after the child shrank = %q", got)
	}
}

// box.ts render: `contentWidth = Math.max(1, width - paddingX * 2)` is the width the children are rendered at, and every row (padding rows included) is padded
// to the full width.
func TestBoxRendersChildrenAtLeastOneCellWideAndPadsEveryRowToTheWidth(t *testing.T) {
	child := &scriptedChild{lines: []string{"ab"}}
	box := NewPaddedBox(1, 1, nil)
	box.AddChild(child)
	rows := box.Render(0)
	if len(child.widths) != 1 || child.widths[0] != 1 {
		t.Fatalf("child rendered at widths %v for width 0 and padding 1, want [1]", child.widths)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %q, want padding row, content, padding row", rows)
	}
	wide := NewPaddedBox(1, 1, nil)
	wide.AddChild(&scriptedChild{lines: []string{"ab"}})
	for i, row := range wide.Render(8) {
		if widthx.VisibleWidth(row) != 8 {
			t.Errorf("row %d = %q is %d cells wide, want 8", i, row, widthx.VisibleWidth(row))
		}
	}
}

// invalidationCountingChild counts the Invalidate calls it receives.
type invalidationCountingChild struct {
	scriptedChild
	invalidations int
}

func (c *invalidationCountingChild) Invalidate() { c.invalidations++ }

// box.ts addChild, removeChild and clear call only invalidateCache, which drops the box's own cache; the children are invalidated only by Box.invalidate.
func TestBoxStructureChangesDoNotInvalidateTheChildren(t *testing.T) {
	kept, moved := &invalidationCountingChild{scriptedChild: scriptedChild{lines: []string{"k"}}}, &invalidationCountingChild{scriptedChild: scriptedChild{lines: []string{"m"}}}
	box := NewPaddedBox(0, 0, nil)
	box.AddChild(kept)
	box.AddChild(moved)
	box.RemoveChild(moved)
	box.AddChild(moved)
	box.Clear()
	if kept.invalidations != 0 || moved.invalidations != 0 {
		t.Fatalf("children invalidated %d and %d times by addChild/removeChild/clear, want 0", kept.invalidations, moved.invalidations)
	}
	box.AddChild(kept)
	box.Invalidate()
	if kept.invalidations != 1 {
		t.Fatalf("Box.Invalidate invalidated the child %d times, want 1", kept.invalidations)
	}
}
