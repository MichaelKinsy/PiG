package ai

import (
	"slices"
	"testing"
)

// Ending a stream sorts its open blocks by content index; the sort must not allocate (sort.Slice boxes the slice and builds a reflective swapper on every call), and must keep Pi's end order: text/thinking/tool blocks by their position in content.
func TestSortEndingBlocksOrdersByContentIndexWithoutAllocating(t *testing.T) {
	want := []endingBlock{{contentIndex: 0, kind: "thinking"}, {contentIndex: 1, kind: "text"}, {contentIndex: 3, kind: "tool", toolIndex: 1}, {contentIndex: 7, kind: "tool"}}
	for _, order := range [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}, {1, 3, 0, 2}} {
		got := make([]endingBlock, len(order))
		for i, index := range order {
			got[i] = want[index]
		}
		sortEndingBlocks(got)
		if !slices.Equal(got, want) {
			t.Fatalf("order %v sorted to %v, want %v", order, got, want)
		}
	}
	blocks := slices.Clone(want)
	slices.Reverse(blocks)
	if allocs := testing.AllocsPerRun(100, func() {
		slices.Reverse(blocks)
		sortEndingBlocks(blocks)
	}); allocs != 0 {
		t.Fatalf("sortEndingBlocks allocated %v times per call", allocs)
	}
}
