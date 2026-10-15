package tui

import (
	"strings"
	"testing"
)

// marked lexes a list item's text with top false: no paragraph rule runs, so every line starts a fresh try at the block rules and an HTML block starts at any line, indented up to three spaces. A line indented four or more spaces past the item's content is indented code, which marked's blockTokens joins to a text token before it (four spaces removed, so the line is text whatever it now looks like) and which keeps a newline that makes an empty row before the text that follows; after a heading, rule or other block it is a code block of its own.
// Pi source: marked 18 Lexer.blockTokens (top false, code joined to a text or paragraph token) and Tokenizer.list; packages/tui/src/components/markdown.ts renderListItem.
// Left out because they differ from Pi independently of this rule: a quoted line followed by an indented quoted line (`> q` then `> q` four spaces on), a definition followed by an indented `---`, a task item (`- [ ] t`) or nested list whose continuation line is indented code, an ordered item whose next line is a sibling `1. o` indented two spaces, and a setext underline run (`---` then `===`) followed by an HTML block.
func TestMarkdownListItemContinuationLinesMatchPi(t *testing.T) {
	prefixes := []struct {
		first string
		width int
	}{{"- l", 2}, {"1. l", 3}}
	lines := []string{"<div>x", "<custom>", "<!-- c -->", "<pre>x</pre>", "code", "# h", "> q", "---", "===", "```", "c", "|a|b|", "|-|-|", "[a]: /u", "<span>x"}
	rels := []int{0, 1, 3, 4, 5}
	var docs []string
	for _, p := range prefixes {
		for i, first := range lines {
			for _, r1 := range rels {
				docs = append(docs, p.first+"\n"+strings.Repeat(" ", p.width+r1)+first)
				for j, second := range lines {
					if first == "> q" && second == "> q" || first == "[a]: /u" && second == "---" || (i+j+r1)%3 != 0 {
						continue
					}
					for _, r2 := range rels {
						docs = append(docs, p.first+"\n"+strings.Repeat(" ", p.width+r1)+first+"\n"+strings.Repeat(" ", p.width+r2)+second)
					}
				}
			}
		}
	}
	docs = append(docs,
		"- l\n      a\n      b", "- l\n      a\n\n      b", "- l\n    <div>x\n\n    y", "- l\n      c\n  # h", "- l\n      c\n  - n", "- l\n  # h\n      c", "- l\n  ---\n      c",
		"- l\n      c\n  d\n  e", "- l\n      c\n   d", "- l\n\n      c", "- l\n\n     ===\n       c", "- l\n   <div>x\n   y", "- l\n   <div>x\n\n   y\n  z", "- a\n   <div>x\n- b\n      c",
		// A bullet four spaces past the content is code joined to the text, a bullet at three is a nested list.
		"- l\n      - n", "- l\n        - n", "- l\n      1. n", "1. l\n       - n", "- l\n     - n", "- l\n      a\n\n      # h", "- l\n      a\n\n      - n",
		// The top level has a paragraph rule: a custom tag does not interrupt it.
		"l\n<custom>", "l\n  <custom>\nx", "l\n<custom>\n\ny",
	)
	compareMarkdownWithPi(t, docs)
}
