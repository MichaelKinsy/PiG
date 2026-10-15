package tui

import (
	"strings"
	"testing"
)

// marked splits a task item's marker into a checkbox token and lexes the rest of its first line as a text token, so a heading, quote, rule, bullet, fence, table row, definition or HTML tag after `[ ] ` is plain text; the lines after it are lexed as usual. A setext heading over the first line takes the marker into its text and the item has no checkbox.
// Pi source: marked 18 Tokenizer.list task handling and Lexer.blockTokens; packages/tui/src/components/markdown.ts renderListItem.
func TestMarkdownTaskItemFirstLineIsTextMatchesPi(t *testing.T) {
	var docs []string
	for _, mark := range []string{"[ ]", "[x]", "[X]"} {
		for _, rest := range []string{"t", "# h", "```\n  c\n  ```", "> q", "- a", "---", "<div>", "|a|b|\n  |-|-|", "t\n  ===", "t\n  ---", "t\n===", "t\n  ```\n  c\n  ```", "    code", "[r]: /u", "[r]: /u\n  x", "<!-- c -->", "t\n  <div>x", "t\n\n  u", "", "t\n  - n", "t\n  # h", "1. a", "***", "$$", "# h\n  ===", "t u", "**b**"} {
			docs = append(docs, "- "+mark+" "+rest, "* "+mark+" "+rest, "- "+mark+"  "+rest, "1. "+mark+" "+rest)
			if rest != "" {
				docs = append(docs, "- a\n- "+mark+" "+rest+"\n- b")
			}
		}
	}
	// Only the first block is text: what follows a nested list is lexed as usual.
	docs = append(docs, "- [ ] t\n  - n\n  # h", "- [ ] t\n  - n\n\n  # h", "- [x] t\n  - n\n  > q",
		// A NUL in the source is rendered as written, in a task item and in any other list item.
		"- a\x00b", "- [ ] a\x00b", "- [ ] \x00# h", "- [x] a\n  b\x00c", "- [ ] a\n  - n\x00")
	compareMarkdownWithPi(t, docs)
}

// marked's listIsTask is /^\[[ xX]\] +\S/: the spaces (not tabs) must be followed by a character JavaScript's \s does not match, so an item whose text is "[ ] " (or has the next line after it) is plain text.
// Pi source: marked 18 Tokenizer.list (a.task = listIsTask.test(c)).
func TestMarkdownTaskItemNeedsATextAfterTheSpacesMatchesPi(t *testing.T) {
	var docs []string
	for _, mark := range []string{"[ ]", "[x]", "[X]"} {
		for _, rest := range []string{"", " ", "  ", "\t", " \t", "\u00a0", " \u00a0", "\u3000", " \u3000", "\ufeff", " \ufeff", " \u200bx", " \u0085x", "\tx", "\u00a0x", "x", " x", "\n  c", " \n  c", "  \n- b", " \n\n  c", " \n  - n", " \n  # h"} {
			docs = append(docs, "- "+mark+rest, "- "+mark+rest+"\n- b", "- a\n- "+mark+rest+"\n- b", "1. "+mark+rest)
			// A quoted list item with a lazy line after it (`> - [ ] `, `  c`) differs from Pi independently of this rule.
			if !strings.Contains(rest, "\n  c") {
				docs = append(docs, "> - "+mark+rest)
			}
		}
	}
	compareMarkdownWithPi(t, docs)
}
