package tui

import "testing"

// .upstream/current/packages/tui/src/components/h-stack.ts:15-17: HStack lays out against an unbounded height, so a child's visible predicate receives
// { width: max(1, width), height: Number.MAX_SAFE_INTEGER }, as VStack's does (v-stack.ts:11-12). h-stack.ts:18 renders `[]` when no entry is visible.
// mutation-checked: the mutant "HStack viewport height 0" fails it.
// mutation-checked: the mutant "HStack renders nil when no entry is visible" fails it.
func TestHStackGivesVisiblePredicatesAnUnboundedHeightAndTheClampedWidth(t *testing.T) {
	for _, tc := range []struct{ width, want int }{{20, 20}, {1, 1}, {0, 1}, {-5, 1}} {
		var seen []LayoutViewport
		child := &stubComponent{lines: []string{"x"}}
		h := NewHStack([]StackEntry{{Component: child, StackEntryOptions: StackEntryOptions{Visible: func(viewport LayoutViewport) bool {
			seen = append(seen, viewport)
			return false
		}}}}, StackOptions{})
		lines := h.Render(tc.width)
		if len(seen) != 1 || seen[0] != (LayoutViewport{Width: tc.want, Height: numberMaxSafeInteger}) {
			t.Fatalf("Render(%d): visible saw %v, want one viewport %v", tc.width, seen, LayoutViewport{Width: tc.want, Height: numberMaxSafeInteger})
		}
		if lines == nil || len(lines) != 0 {
			t.Fatalf("Render(%d) with no visible entry = %#v, want the empty list", tc.width, lines)
		}
	}
}
