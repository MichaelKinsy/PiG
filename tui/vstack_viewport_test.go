package tui

import "testing"

// .upstream/current/packages/tui/src/components/v-stack.ts:11-12: VStack lays out against an unbounded height, so a child's visible
// predicate receives { width: max(1, width), height: Number.MAX_SAFE_INTEGER } and the children render at that clamped width.
// numberMaxSafeInteger is JS Number.MAX_SAFE_INTEGER written out, so a change to tui's maxSafeInteger constant fails this test.
const numberMaxSafeInteger = 9007199254740991

func TestVStackGivesVisiblePredicatesAnUnboundedHeightAndTheClampedWidth(t *testing.T) {
	for _, tc := range []struct{ width, want int }{{20, 20}, {1, 1}, {0, 1}, {-5, 1}} {
		var seen []LayoutViewport
		child := &stubComponent{lines: []string{"x"}}
		v := NewVStack([]StackEntry{{Component: child, StackEntryOptions: StackEntryOptions{Visible: func(viewport LayoutViewport) bool {
			seen = append(seen, viewport)
			return true
		}}}}, StackOptions{})
		v.Render(tc.width)
		if len(seen) != 1 || seen[0] != (LayoutViewport{Width: tc.want, Height: numberMaxSafeInteger}) {
			t.Fatalf("Render(%d): visible saw %v, want one viewport %v", tc.width, seen, LayoutViewport{Width: tc.want, Height: numberMaxSafeInteger})
		}
	}
}
