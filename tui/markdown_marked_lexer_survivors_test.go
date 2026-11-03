package tui

import (
	"testing"
	"unicode/utf8"
)

// markedLexerSurvivorDocs are documents that separate a mutation of markdown_marked_lexer.go from the lexer: each lexes to the tree of marked, and a mutant of the listed site lexes it to another one.
var markedLexerSurvivorDocs = []string{
	// The inline queue holds the cells of a table: the task trim of the second pass of Tokenizer.list takes the last entry whose source is a task, which is a cell here and not the first item's own entry (inlineCell appends it).
	"- [X] c\n- [X] c\n|-|", "- [ ] \U0001F642\n- [ ] \U0001F642\n|-|", "- [x] a\n- | [x] b |\n  |-|", "- [x] a\n  | [x] b |\n  |-|\n- c",
	// A quote whose first line is a list item and whose lazy lines continue it: the quote's text loses the lazy line's length (markedWithoutTail).
	">>2)\n>  ===\ntwo words\ta  ", ">>2)\n>  ===\nx", "> - a\n>  ===\nb  ", ">- a\n> ---\nb",
}

// TestMarkedBlockLexerSurvivorDocsMatchMarked requires PiG's block tokens to equal marked's on markedLexerSurvivorDocs.
func TestMarkedBlockLexerSurvivorDocsMatchMarked(t *testing.T) {
	requireMarkedLexerMatchesPi(t, "survivor documents", markedLexerSurvivorDocs, 1)
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// A token's offset is where it starts in the source that was lexed (the resume points of a streamed render are recorded from it): the source has the first character of the token's raw there, and the tokens of a source follow each other.
func TestMarkedBlockTokenOffsetsLocateTheirTokens(t *testing.T) {
	sets := markedLexerCommittedSources(t)
	var docs []string
	for _, set := range sets {
		docs = append(docs, set...)
	}
	docs = append(docs, markedLexerSurvivorDocs...)
	state := &markedInlineState{}
	checked := 0
	for _, doc := range docs {
		normalized := normalizeSource(doc)
		runes := []rune(normalized)
		state.startDocument()
		tokens := state.blockLexer().lexRunes(state.blockRunes(normalized), normalized)
		previous := -1
		for _, token := range tokens {
			if token.offset <= previous {
				t.Fatalf("%q: a %s token at offset %d follows one at %d", doc, token.kind, token.offset, previous)
			}
			previous = token.offset
			// A quote's and a clipped paragraph's raw are spliced from pieces of the source, so only their first character is the source's at the offset.
			if token.raw != "" && (token.offset >= len(runes) || runes[token.offset] != firstRune(token.raw)) {
				t.Fatalf("%q: the %s token with raw %q does not start at offset %d", doc, token.kind, token.raw, token.offset)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no tokens")
	}
}
