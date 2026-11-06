//go:build !race

package tui

import "testing"

// Every line of a list item that the block lexer reads is asked whether it opens a block that ends the item. Bound: asking about item and non-item lines of a long answer allocates nothing.
func TestMarkedListItemRulesAllocateNothing(t *testing.T) {
	lines := []string{
		"- plain text, with `inline code` and a long line",
		"- nested lists:",
		"  1. first, with a number 1",
		"  2. second, with ~~strikethrough~~",
		"The `parse()` function reads **one token at a time**",
		"| column | value | note |",
		"    ```go",
		"> quoted",
		"# heading",
		"<div>",
		"* * *",
	}
	matches := 0
	allocs := testing.AllocsPerRun(100, func() {
		matches = 0
		for _, line := range lines {
			for _, rule := range []func(string, int) bool{markedItemBegin, markedHrBegin, markedFencesBegin, markedHeadingBegin, markedHTMLBeginAt, markedBlockquoteBegin} {
				if rule(line, 3) {
					matches++
				}
			}
		}
	})
	if matches == 0 {
		t.Fatal("no line matched a rule")
	}
	if allocs > 0 {
		t.Fatalf("asking %d lines about the rules that end a list item allocated %.1f times, want 0", len(lines), allocs)
	}
}
