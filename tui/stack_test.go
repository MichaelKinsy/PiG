package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func mkEntries(opts ...StackEntryOptions) []StackLayoutEntry {
	entries := make([]StackLayoutEntry, len(opts))
	for i, o := range opts {
		e := StackLayoutEntry{Visible: o.Visible}
		if o.Basis != nil {
			e.Basis = o.Basis
		}
		if o.Grow != nil {
			g := normalizeSize(o.Grow, 0)
			e.Grow = &g
		}
		if o.Shrink != nil {
			s := normalizeSize(o.Shrink, 1)
			e.Shrink = &s
		}
		if o.MinSize != nil {
			m := normalizeSize(o.MinSize, 0)
			e.MinSize = &m
		}
		if o.MaxSize != nil {
			m := normalizeSize(o.MaxSize, maxSafeInteger)
			e.MaxSize = &m
		}
		entries[i] = e
	}
	return entries
}

// Expected values are computed by hand-executing the upstream flexbox algorithm
// (the independent oracle), not read back from this implementation.
func TestAllocateStackSizes(t *testing.T) {
	tests := []struct {
		name      string
		entries   []StackLayoutEntry
		intrinsic []int
		available *int
		gap       int
		want      []int
	}{
		{
			name:      "no available returns intrinsic clamped",
			entries:   mkEntries(StackEntryOptions{}, StackEntryOptions{}),
			intrinsic: []int{3, 7},
			available: nil,
			want:      []int{3, 7},
		},
		{
			name:      "grow distributes surplus evenly by weight",
			entries:   mkEntries(StackEntryOptions{Grow: new(1)}, StackEntryOptions{Grow: new(1)}),
			intrinsic: []int{3, 3},
			available: new(10),
			want:      []int{6, 4},
		},
		{
			name:      "shrink reclaims weighted by current size",
			entries:   mkEntries(StackEntryOptions{Shrink: new(1)}, StackEntryOptions{Shrink: new(1)}),
			intrinsic: []int{8, 8},
			available: new(10),
			want:      []int{4, 6},
		},
		{
			name:      "maxSize clamps intrinsic",
			entries:   mkEntries(StackEntryOptions{MaxSize: new(3)}),
			intrinsic: []int{5},
			available: nil,
			want:      []int{3},
		},
		{
			name:      "gap reduces content size before grow",
			entries:   mkEntries(StackEntryOptions{Grow: new(1)}, StackEntryOptions{Grow: new(1)}),
			intrinsic: []int{3, 3},
			available: new(10),
			gap:       2,
			want:      []int{4, 4},
		},
		{
			name:      "explicit basis overrides intrinsic",
			entries:   mkEntries(StackEntryOptions{Basis: new(5)}, StackEntryOptions{}),
			intrinsic: []int{1, 2},
			available: nil,
			want:      []int{5, 2},
		},
		{
			name:      "no grow flag means surplus is left unfilled",
			entries:   mkEntries(StackEntryOptions{}, StackEntryOptions{}),
			intrinsic: []int{3, 3},
			available: new(10),
			want:      []int{3, 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allocateStackSizes(tt.entries, tt.intrinsic, tt.available, tt.gap)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("allocateStackSizes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClampSizeMinWinsOverMax(t *testing.T) {
	// min 5, max 3 -> max is raised to min, result 5.
	e := StackLayoutEntry{MinSize: new(5), MaxSize: new(3)}
	if got := clampSize(10, e); got != 5 {
		t.Fatalf("clampSize = %d, want 5", got)
	}
	if got := clampSize(-4, StackLayoutEntry{}); got != 0 {
		t.Fatalf("clampSize(negative) = %d, want 0", got)
	}
}

func TestVisibleStackEntriesFilters(t *testing.T) {
	shown := StackLayoutEntry{Visible: func(LayoutViewport) bool { return true }}
	hidden := StackLayoutEntry{Visible: func(LayoutViewport) bool { return false }}
	unset := StackLayoutEntry{}
	got := visibleStackEntries([]StackLayoutEntry{shown, hidden, unset}, LayoutViewport{Width: 10, Height: 5})
	if len(got) != 2 {
		t.Fatalf("visible entries = %d, want 2 (shown + unset)", len(got))
	}
}

func TestVStackRenderTruncatesAndPadsWithGap(t *testing.T) {
	a := &stubComponent{lines: []string{"a"}}
	b := &stubComponent{lines: []string{"b", "c"}}
	v := NewVStack([]StackChild{{Component: a}, {Component: b}}, StackOptions{Gap: new(1)})
	got := v.Render(20)
	want := []string{"a", "", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("VStack.Render = %q, want %q", got, want)
	}
}

func TestVStackRenderPadsToAllocatedHeight(t *testing.T) {
	// maxSize forces a taller allocation than the child's intrinsic height,
	// but with no available size the allocator returns the intrinsic size, so
	// no padding. A larger basis, however, pads with blank lines.
	a := &stubComponent{lines: []string{"x"}}
	v := NewVStack([]StackChild{{Component: a, StackEntryOptions: StackEntryOptions{Basis: new(3)}}}, StackOptions{})
	got := v.Render(20)
	want := []string{"x", "", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("VStack.Render = %q, want %q", got, want)
	}
}

func TestHStackRenderCompositesSideBySide(t *testing.T) {
	a := &stubComponent{lines: []string{"ab"}}
	b := &stubComponent{lines: []string{"cd"}}
	h := NewHStack([]StackChild{{Component: a}, {Component: b}}, StackOptions{})
	got := h.Render(4)
	if len(got) != 1 {
		t.Fatalf("HStack.Render lines = %d, want 1", len(got))
	}
	// Both children get width 2 (intrinsic), placed at columns 0 and 2.
	if w := widthx.VisibleWidth(got[0]); w != 4 {
		t.Fatalf("composited width = %d, want 4", w)
	}
	stripped := stripANSI(got[0])
	if stripped != "abcd" {
		t.Fatalf("HStack row = %q, want %q", stripped, "abcd")
	}
}

func TestHStackRenderAlignEndOffsetsShorterChild(t *testing.T) {
	tall := &stubComponent{lines: []string{"1", "2", "3"}}
	short := &stubComponent{lines: []string{"x"}}
	end := "end"
	h := NewHStack([]StackChild{{Component: tall}, {Component: short}}, StackOptions{Align: end})
	got := h.Render(2)
	if len(got) != 3 {
		t.Fatalf("HStack height = %d, want 3", len(got))
	}
	// The tall child fills column 0 top-to-bottom ("1","2","3"); align=end drops
	// the single-line short child onto the bottom row at column 1, so the bottom
	// row is "3x" and the top row is just the tall child's "1".
	if s := stripANSI(got[2]); s != "3x" {
		t.Fatalf("bottom row = %q, want %q", s, "3x")
	}
	if s := strings.TrimRight(stripANSI(got[0]), " "); s != "1" {
		t.Fatalf("top row = %q, want the tall child \"1\" only", s)
	}
}

func TestStackLayoutNodeReflectsEntries(t *testing.T) {
	a := &stubComponent{lines: []string{"a"}}
	v := NewVStack([]StackChild{{Component: a, StackEntryOptions: StackEntryOptions{Grow: new(2)}}}, StackOptions{Gap: new(3), Align: "center"})
	node, ok := v.LayoutNode().(StackLayoutNode)
	if !ok {
		t.Fatalf("LayoutNode is %T, want StackLayoutNode", v.LayoutNode())
	}
	if node.Type != "vstack" || node.Gap != 3 || node.Align != "center" {
		t.Fatalf("node = %+v", node)
	}
	if len(node.Entries) != 1 || node.Entries[0].Grow == nil || *node.Entries[0].Grow != 2 {
		t.Fatalf("entries = %+v", node.Entries)
	}
}
