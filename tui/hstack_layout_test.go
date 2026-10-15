package tui

import (
	"reflect"
	"strings"
	"testing"
)

func hstackRows(stack *HStack, width int) []string {
	rows := stack.Render(width)
	for i := range rows {
		rows[i] = strings.TrimRight(stripANSI(rows[i]), " ")
	}
	return rows
}

// packages/tui/src/components/h-stack.ts:12-13: HStack lays out against { width: max(1, width), height: Number.MAX_SAFE_INTEGER }; each child's
// visible predicate receives that viewport.
// mutation-checked: a height other than maxSafeInteger at tui/stack.go HStack.Render fails it.
func TestHStackGivesAVisibleEntrysPredicateTheUnboundedViewport(t *testing.T) {
	for _, tc := range []struct{ width, want int }{{20, 20}, {1, 1}, {0, 1}, {-5, 1}} {
		var seen []LayoutViewport
		child := &stubComponent{lines: []string{"x"}}
		stack := NewHStack([]StackEntry{{Component: child, StackEntryOptions: StackEntryOptions{Visible: func(viewport LayoutViewport) bool {
			seen = append(seen, viewport)
			return true
		}}}}, StackOptions{})
		stack.Render(tc.width)
		want := LayoutViewport{Width: tc.want, Height: maxSafeInteger}
		if len(seen) != 1 || seen[0] != want {
			t.Fatalf("Render(%d): visible saw %v, want one viewport %v", tc.width, seen, want)
		}
	}
}

// h-stack.ts:16-19: a child's intrinsic width is the widest line in terminal cells (visibleWidth), so wide characters count two and ANSI sequences count zero.
// mutation-checked: measuring bytes instead of cells at tui/stack.go HStack.Render fails it.
func TestHStackMeasuresChildWidthInTerminalCells(t *testing.T) {
	wide := &stubComponent{lines: []string{"日本"}}
	colored := &stubComponent{lines: []string{"\x1b[31mab\x1b[0m"}}
	tail := &stubComponent{lines: []string{"x"}}
	stack := NewHStack([]StackEntry{{Component: wide}, {Component: colored}, {Component: tail}}, StackOptions{})
	if got, want := hstackRows(stack, 20), []string{"日本abx"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

// h-stack.ts:35-36: center alignment offsets a shorter child by floor((height - lines) / 2); the odd remainder falls below.
// mutation-checked: rounding that quotient up at tui/stack.go HStack.Render fails it.
func TestHStackCenterAlignFloorsTheOffset(t *testing.T) {
	tall := &stubComponent{lines: []string{"a", "b", "c"}}
	short := &stubComponent{lines: []string{"X", "Y"}}
	stack := NewHStack([]StackEntry{{Component: tall}, {Component: short}}, StackOptions{Align: "center"})
	if got, want := hstackRows(stack, 20), []string{"aX", "bY", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

// h-stack.ts:39-40 and tui.ts:415-445 compositeTuiLine: a child's line is composited at its own allocated width, so a child that renders wider than
// the width it was given is clipped to that width and does not spill past it.
// mutation-checked: compositing at the total width instead of the child's width at tui/stack.go HStack.Render fails it.
func TestHStackClipsAChildToItsAllocatedWidth(t *testing.T) {
	lead := &stubComponent{lines: []string{"z"}}
	overlong := &stubComponent{lines: []string{"ABCDEF"}}
	stack := NewHStack([]StackEntry{
		{Component: lead},
		{Component: overlong, StackEntryOptions: StackEntryOptions{Basis: new(3), Shrink: new(0)}},
	}, StackOptions{})
	if got, want := hstackRows(stack, 20), []string{"zABC"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	if got := NewHStack(nil, StackOptions{}).Render(10); got == nil || len(got) != 0 {
		t.Fatalf("an empty HStack rendered %#v, want an empty non-nil slice (h-stack.ts:15 returns [])", got)
	}
}
