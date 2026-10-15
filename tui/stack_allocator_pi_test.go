package tui

import (
	"slices"
	"testing"
)

// components/stack.ts:89-170 clampSize, distribute and allocateStackSizes. The expected sizes are what node computes by running those functions verbatim on the
// same entries (grow weights, caps, growers only, shrink weighted by size, minimum floors, shrink 0, gaps, auto basis, negative and contradictory sizes).
func TestAllocateStackSizesMatchesPiOnWeightedScenarios(t *testing.T) {
	type o = StackEntryOptions
	ptr := func(n int) *int { return &n }
	for _, tc := range []struct {
		name      string
		entries   []StackEntryOptions
		intrinsic []int
		available *int
		gap       int
		want      []int
	}{
		{"grow by weight", []o{{Basis: ptr(0), Grow: ptr(1)}, {Basis: ptr(0), Grow: ptr(3)}}, nil, ptr(40), 0, []int{13, 27}},
		{"grow respects a maximum and hands the rest on", []o{{Basis: ptr(0), Grow: ptr(1), MaxSize: ptr(10)}, {Basis: ptr(0), Grow: ptr(1)}}, nil, ptr(100), 0, []int{10, 90}},
		{"only entries with grow above zero grow", []o{{Basis: ptr(5)}, {Basis: ptr(5), Grow: ptr(1)}, {Basis: ptr(5), Grow: ptr(0)}}, nil, ptr(30), 0, []int{5, 20, 5}},
		{"an entry at its maximum is not a candidate", []o{{Basis: ptr(10), Grow: ptr(1), MaxSize: ptr(10)}, {Basis: ptr(0), Grow: ptr(1)}}, nil, ptr(50), 0, []int{10, 40}},
		{"every candidate proposes at least one cell", []o{{Basis: ptr(0), Grow: ptr(1)}, {Basis: ptr(0), Grow: ptr(1000)}}, nil, ptr(3), 0, []int{2, 1}},
		{"shrink is weighted by size", []o{{Basis: ptr(30)}, {Basis: ptr(10)}}, nil, ptr(20), 0, []int{12, 8}},
		{"shrink stops at the minimum", []o{{Basis: ptr(30), MinSize: ptr(25)}, {Basis: ptr(10)}}, nil, ptr(20), 0, []int{25, 0}},
		{"shrink 0 keeps its size", []o{{Basis: ptr(30), Shrink: ptr(0)}, {Basis: ptr(30)}}, nil, ptr(40), 0, []int{30, 10}},
		{"shrink factor weights the reduction", []o{{Basis: ptr(20), Shrink: ptr(3)}, {Basis: ptr(20), Shrink: ptr(1)}}, nil, ptr(20), 0, []int{3, 17}},
		{"gaps are taken from the available size", []o{{Basis: ptr(0), Grow: ptr(1)}, {Basis: ptr(0), Grow: ptr(1)}, {Basis: ptr(0), Grow: ptr(1)}}, nil, ptr(20), 2, []int{8, 5, 3}},
		{"gaps larger than the space leave nothing", []o{{Basis: ptr(3)}, {Basis: ptr(3)}}, nil, ptr(4), 10, []int{0, 0}},
		{"auto and missing basis use the intrinsic size", []o{{}, {}, {Basis: ptr(7)}}, []int{4, 5, 6}, nil, 0, []int{4, 5, 7}},
		{"negative basis is zero and a minimum above the maximum wins", []o{{Basis: ptr(-5)}, {Basis: ptr(3), MinSize: ptr(4), MaxSize: ptr(2)}}, nil, nil, 0, []int{0, 4}},
		{"a minimum above the maximum wins over the basis", []o{{Basis: ptr(9), MinSize: ptr(6), MaxSize: ptr(3)}}, nil, nil, 0, []int{6}},
		{"growing by exactly one cell", []o{{Basis: ptr(4), Grow: ptr(1)}}, nil, ptr(5), 0, []int{5}},
		{"shrinking by exactly one cell", []o{{Basis: ptr(6)}}, nil, ptr(5), 0, []int{5}},
		{"nothing available shrinks to zero", []o{{Basis: ptr(3)}, {Basis: ptr(3)}}, nil, ptr(0), 0, []int{0, 0}},
	} {
		got := allocateStackSizes(mkEntries(tc.entries...), tc.intrinsic, tc.available, tc.gap)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: allocateStackSizes = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// components/stack.ts Stack: constructor and addChild keep the options as normalized entries (negative sizes become 0, an omitted option stays unset), the align
// defaults to "stretch", removeChild removes the component and the first matching entry only, and clear empties the container and the entries.
func TestStackEntriesAreNormalizedAndRemovedWithTheirComponent(t *testing.T) {
	ptr := func(n int) *int { return &n }
	a, b := &scriptedChild{lines: []string{"a"}}, &scriptedChild{lines: []string{"b"}}
	stack := NewVStack(nil, StackOptions{})
	stack.AddChild(a, StackEntryOptions{Grow: ptr(-3), Shrink: ptr(-1), MinSize: ptr(-5), MaxSize: ptr(-4), Basis: ptr(-2)})
	stack.AddChild(b, StackEntryOptions{})
	stack.AddChild(a, StackEntryOptions{Grow: ptr(2)})
	node, ok := stack.LayoutNode().(StackLayoutNode)
	if !ok || node.Align != "stretch" || node.Gap != 0 || len(node.Entries) != 3 {
		t.Fatalf("layout node = %#v", stack.LayoutNode())
	}
	first := node.Entries[0]
	if *first.Grow != 0 || *first.Shrink != 0 || *first.MinSize != 0 || *first.MaxSize != 0 || *first.Basis != -2 {
		t.Errorf("negative options normalized to grow %d shrink %d min %d max %d basis %d, want 0 0 0 0 and the raw basis -2", *first.Grow, *first.Shrink, *first.MinSize, *first.MaxSize, *first.Basis)
	}
	second := node.Entries[1]
	if second.Grow != nil || second.Shrink != nil || second.MinSize != nil || second.MaxSize != nil || second.Basis != nil {
		t.Errorf("omitted options were stored: %+v", second)
	}
	if got := len(stack.childSnapshot()); got != 3 {
		t.Fatalf("the container holds %d children, want 3", got)
	}
	stack.RemoveChild(a)
	if got := len(stack.childSnapshot()); got != 2 {
		t.Fatalf("after RemoveChild the container holds %d children, want 2", got)
	}
	node = stack.LayoutNode().(StackLayoutNode)
	if len(node.Entries) != 2 || node.Entries[0].Component != b || node.Entries[1].Component != a {
		t.Fatalf("after removing the first a: %d entries, want [b a]", len(node.Entries))
	}
	if got := stack.Render(3); !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("render after RemoveChild = %q, want [b a]", got)
	}
	stack.Clear()
	if got := len(stack.childSnapshot()); got != 0 {
		t.Fatalf("after Clear the container holds %d children, want 0", got)
	}
	if node = stack.LayoutNode().(StackLayoutNode); len(node.Entries) != 0 || len(stack.Render(3)) != 0 {
		t.Fatalf("Clear left %d entries and render %q", len(node.Entries), stack.Render(3))
	}
	gapped := NewHStack(nil, StackOptions{Gap: ptr(-2), Align: "center"})
	if node := gapped.LayoutNode().(StackLayoutNode); node.Gap != 0 || node.Align != "center" || node.Type != "hstack" {
		t.Errorf("hstack layout node = %#v, want gap 0 (negative gap normalized), align center, type hstack", node)
	}
}

// components/h-stack.ts and v-stack.ts render: a stack is laid out at least one cell wide, an empty or fully hidden horizontal stack renders nothing, a child allocated
// no width is not rendered at that width, shorter children are placed by the alignment (center: floor((height - lines) / 2)), and a vertical entry is cut or
// padded to its allocated height.
func TestStacksRenderLikePiAtTheEdges(t *testing.T) {
	ptr := func(n int) *int { return &n }
	if got := NewHStack(nil, StackOptions{}).Render(10); len(got) != 0 {
		t.Errorf("an empty HStack rendered %q", got)
	}
	if _, ok := Component(NewVStack(nil, StackOptions{})).(*VStack); !ok {
		t.Error("NewVStack is not a *VStack component")
	}
	if _, ok := Component(NewHStack(nil, StackOptions{})).(*HStack); !ok {
		t.Error("NewHStack is not a *HStack component")
	}
	probe := &scriptedChild{lines: []string{"p"}}
	NewHStack([]StackEntry{{Component: probe}}, StackOptions{}).Render(0)
	NewVStack([]StackEntry{{Component: probe}}, StackOptions{}).Render(0)
	if !slices.Equal(probe.widths, []int{1, 1, 1}) {
		t.Errorf("children rendered at widths %v for stacks of width 0, want [1 1 1] (the horizontal stack measures, then renders)", probe.widths)
	}
	hidden := &scriptedChild{lines: []string{"h"}}
	shown := &scriptedChild{lines: []string{"s"}}
	NewHStack([]StackEntry{
		{Component: hidden, StackEntryOptions: StackEntryOptions{Basis: ptr(0)}},
		{Component: shown},
	}, StackOptions{}).Render(5)
	if !slices.Equal(hidden.widths, []int{5}) {
		t.Errorf("a child allocated no width was rendered at widths %v, want only the intrinsic measurement [5]", hidden.widths)
	}
	tall := &scriptedChild{lines: []string{"1", "2", "3"}}
	short := &scriptedChild{lines: []string{"x"}}
	centered := NewHStack([]StackEntry{{Component: tall}, {Component: short}}, StackOptions{Align: "center"}).Render(2)
	if len(centered) != 3 || stripANSI(centered[1]) != "2x" || stripANSI(centered[0]) != "1 " || stripANSI(centered[2]) != "3 " {
		t.Errorf("center alignment rows = %q, want the short child on the middle row", centered)
	}
	long := &scriptedChild{lines: []string{"a", "b", "c"}}
	cut := NewVStack([]StackEntry{{Component: long, StackEntryOptions: StackEntryOptions{MaxSize: ptr(1)}}}, StackOptions{}).Render(3)
	if !slices.Equal(cut, []string{"a"}) {
		t.Errorf("an entry with maximum height 1 rendered %q, want [a]", cut)
	}
}
