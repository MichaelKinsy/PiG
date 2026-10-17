//go:build !race

package tui

import "testing"

// Each frame of a streamed answer renders its last blocks again, and the block rules ask parseMarkdownListItem about every
// line of them. Bound: parsing list item and non-item lines of the long answer allocates nothing.
func TestParseMarkdownListItemAllocatesNothing(t *testing.T) {
	lines := []string{
		"- plain text, with `inline code` and a long line",
		"- nested lists:",
		"  1. first, with a number 1",
		"  2. second, with ~~strikethrough~~",
		"The `parse()` function reads **one token at a time**",
		"| column | value | note |",
	}
	allocs := testing.AllocsPerRun(100, func() {
		for _, line := range lines {
			parseMarkdownListItem(line)
		}
	})
	if allocs > 0 {
		t.Fatalf("parsing %d lines for a list item allocated %.1f times, want 0", len(lines), allocs)
	}
}
