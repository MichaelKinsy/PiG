package tui

import (
	"strings"
	"testing"
)

// A table cell is cut into words for the column minimum width on JavaScript whitespace (/\s+/): U+FEFF, U+2028, U+2029 and U+00A0 separate words, U+0085 and U+200B do not.
func TestMarkdownTableCellWordsMatchPi(t *testing.T) {
	var docs []string
	for _, sep := range []string{" ", "\u0085", "\ufeff", "\u00a0", "\u2028", "\u2029", "\u3000", "\u200b", "\u180e", "\u000b", "\u001c", "\u2003"} {
		long := strings.Repeat("a", 18) + sep + strings.Repeat("b", 6)
		docs = append(docs,
			"| "+long+" | c | d |\n|---|---|---|\n| x | y | z |",
			"| h1 | h2 |\n|---|---|\n| "+long+" | "+strings.Repeat("w", 25)+sep+strings.Repeat("v", 25)+" |",
			"| h |\n|---|\n|"+sep+long+sep+"|",
		)
	}
	compareMarkdownWithPi(t, docs)
}

// A table too narrow to render shows its raw markdown (renderTable), and marked's lexer folds a one-newline space token after the table into that raw: the wrapped raw then ends in an empty row, which stays at the end of the text and comes before the empty row the fallback adds when a block follows.
func TestMarkdownNarrowTableFallbackRawMatchesPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"| h |\n|---|\nrow\n", "| h |\n|---|\n| a\u2028b |\nx", "| h |\n|---|\n\nx", "| h |\n|---|\n\n\nx", "| h |\n|---|\n| r |\n", "| h |\n|---|\n| r |\n\n", "| h |\n|---|\n| r |\n\n\n", "| h |\n|---|\n# head", "| h |\n|---|\n| r |\n# head", "| h |\n|---|\n| r |\n---\ntext", "| h |\n|---|\n| r |\n> q", "| h |\n|---|\n| r |\n\n```\nc\n```", "| h |\n|---|\n| r |\n- l", "| h |\n|---|\n| r |", "| h |\n|---|", "| h |\n|---|\n", "| h |\n|---|\n\n", "| h |\n|---|\n   \n",
		"x\n\n| h |\n|---|\n| r |\n\n", "| h |\n|---|\n| r |  \n", "| h |\n|---|\n| r |\n  \n\nx",
		"| h |\n|---|\n| a\u2028b |\nx"})
}

// A table never interrupts a quote's paragraph (Ae's paragraph is Me, which drops the table alternative) and a lazy line joins that paragraph, so a pipe line after a quoted line continues the quote and a lazy delimiter row starts no table.
// Left out: a lazy table header that begins with U+0085, where marked keeps the lines in the quoted paragraph and PiG renders a table.
func TestMarkdownTableLinesAfterQuoteMatchPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"> q\n| a |\n|---|\n| b |", "> q\n| a |\ntext", "> q\n|---|", "> q\n|a|\n|-|", "> q\n\u00a0| a |", "> q\n| a |\u00a0", "> q\n| a |\u0085", "> q\n| a |\ufeff", "> q\n|\u0085|",
		"> q\n|--|\n", "> q\n| - |\ntext", "> q\n|:-:|\ntext", "> q\n|---|---|\ntext", "> q\n| a |\n| b |", "> q\n> |---|", "> a\n> |---|\n> text", "> | a |\n|---|", "> | a |\n> |---|\n> | b |", "> | a |\n|---|\n| b |",
		"para\n| a |\n|---|\n| b |", "para\n|---|", "para\n|a|\n|-|\nx", "para\n| a |\u0085\n|---|", "q\n|---|", "- q\n|---|", "- q\n  |---|"})
}
