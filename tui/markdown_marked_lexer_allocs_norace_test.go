//go:build !race

package tui

import (
	"strings"
	"testing"
)

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

// Bound: the block lexer of a state that has lexed a text before allocates for a document of lists, fenced code, tables and quotes by the chunks it copies strings to, not by each string and token list: a streamed message lexes again at every frame.
func TestMarkedBlockLexerAllocatesPerDocumentNotPerString(t *testing.T) {
	for _, doc := range []struct {
		name  string
		text  string
		bound float64
	}{
		{"streaming body", streamingBody(8000), 8},
		{"long answer", longAnswer(20000), 1800},
		{"list of 600 items", "- Release detail\n" + strings.Repeat("- Release detail\n", 599), 16},
	} {
		state := &markedInlineState{}
		lex := func() {
			state.blockLexer().lexRunes(state.blockRunes(doc.text), doc.text)
			state.startDocument()
		}
		lex()
		if allocs := testing.AllocsPerRun(20, lex); allocs > doc.bound {
			t.Errorf("%s: lexing allocated %.0f times, want at most %.0f", doc.name, allocs, doc.bound)
		} else {
			t.Logf("%s: %.0f allocations", doc.name, allocs)
		}
	}
}
