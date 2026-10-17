package tui

import (
	"strings"
	"testing"
)

// marked's newline rule, /^(?:[ \t]*(?:\n|$))+/, runs before the indented code rule, so a line of only spaces and tabs is a space token even when it starts with four columns of indentation or a tab.
// Pi source: marked 18 Lexer.blockTokens (space before code) and Tokenizer.space.
// Left out because it differs from Pi independently of this rule: a quoted line holding only whitespace after the quote marker that is followed by an unquoted line (`> a`, `>    `, `b`), which Pi keeps in one quoted paragraph.
func TestMarkdownWhitespaceOnlyLineIsNotCodeMatchesPi(t *testing.T) {
	var docs []string
	prefixes := []string{"", "a\n\n", "a\n", "# h\n", "- a\n\n", "- a\n  ", "- a\n\n  ", "> a\n\n>", ">", "> >", ">>", "> a\n>", "1. a\n\n   ", "```\nc\n```\n"}
	widths := []string{"\t", "\t\t", "\t\t\t", " \t", "  \t", "   \t", "    ", "     ", "\t ", "\t  ", "\t\t "}
	for _, p := range prefixes {
		quoted := strings.HasSuffix(p, ">")
		for _, w := range widths {
			docs = append(docs, p+w, p+w+"\n> b", p+w+"\n\nb")
			if !quoted {
				docs = append(docs, p+w+"\nb", p+w+"\n    c")
			}
		}
	}
	compareMarkdownWithPi(t, docs)
}
