package tui

import "testing"

// marked's blank line is /^[ \t]*$/, so a line holding only U+00A0, U+3000, U+2003, U+2028, a form feed or another Unicode space is text, while JavaScript's trim() (the "no text at all" test and the trimmed text of a message) also strips U+FEFF and keeps U+0085.
// Pi source: packages/tui/src/components/markdown.ts (render: text.trim() === ""), marked 18 rules.other.blankLine and Lexer.space.
// Left out because it differs from Pi independently of this rule: a latex block inside a list item whose text is only whitespace (a blank line inside `$$` loosens the list here, Pi lexes the block as one token) and a Unicode-space paragraph between two lists (`- a`, blank, space, blank, `- b`).
func TestMarkdownUnicodeSpaceLinesMatchPi(t *testing.T) {
	spaces := []string{"\ufeff", "\u0085", "\u180e", "\u2028", "\u2029", "\u00a0", "\u3000", "\v", "\f", "\u200b", "\u2003", "\u1680", "\u202f"}
	templates := []string{"%", "a\n%\nb", "%a", "a%", "> %", "> a\n> %\n> b", "```\n%\n```", "a\n\n%\n\nb", "a\n%", "%\na", "|%|%|\n|-|-|\n|%|%|", "a  %\nb", "a\n---%", "---%", "[r]:%/u", "> a\n%", "    %\n", "    %x", "- a\n\n  %\n\n  b", "- a\n  %\n  b", "# a\n%\nb", "---\n%\nb", "1. a\n   %\n   b", "```\nc\n```\n%", "```\nc\n```\n%\nb", "    c\n%\nb", "    c\n%", "|a|b|\n|-|-|\n|1|2|\n%\nb", "a\n===\n%\nb", "a\n===\n%", "a\n%\n\nb", "---\n%", "- a\n%\nb", "$$\nx^2\n$$\n%\nb", "\\[x\\]\n%\nb", "a\n%\n===",
		// Tokenizer.list: a line of only Unicode spaces continues the item, the list raw is trimmed with JavaScript trimEnd and the lexer reads on from there, an item whose first line is one space has no text.
		"- %", "1. %", "* %\n* b", "> - %", "> - %\n> - b", "- a%", "- a\n%", "- a\n\n%", "- a\n%\n- b", "- a\n%\n%\n- b", "- a\n%\n%\n%\n- b", "- a\n  %\n- b", "- a\n%\n  b", "- a\n%\n  b\n- c", "- a\n\n%\n  b", "- a\n%\n\nb", "- a\n\n%\n- b", "- a\n\n%\n\n%\n- b", "- a\n  - b%", "- a\n  - b\n%\n- c", "- a\n  - %\n  - c", "- a%\nb", "- a%\n\n- b", "> - a\n> %\n> - b", "- [ ] %", "- %\n  a", "- a\n\t%\n  b", "1. a\n%\n2. b",
		// An item whose first line is blank by JavaScript's trim starts its content one column after the marker however many spaces follow the marker (`g = t[1].length + 1`).
		"-   %\n  b", "-   %\n    b", "-    %\n  b", "-     %\n  b", "1.  %\n    b", "1.  %\n   b",
		// A line holding U+2028 or U+2029 is not a heading (`(.*)` stops there), and a table whose raw ends in a wide space wraps to an empty row at width 1.
		"# %", "# a%", "## a %b", "|a|b|\n|-|-|\n|1|2|\n%", "|a|b|\n|-|-|\n%\n|1|2|",
		// markdown.ts tokenizeBlockLatex: `dollarMatch?.[1]` is a non-empty string, the text is its JavaScript trim.
		"$$\n%\n$$", "$$\n%\n$$\nb", "\\[\n%\n\\]", "$$%$$", "a\n\n$$\n%\n$$\n", "> $$\n> %\n> $$"}
	var docs []string
	for _, space := range spaces {
		for _, template := range templates {
			doc := ""
			for _, r := range template {
				if r == '%' {
					doc += space
				} else {
					doc += string(r)
				}
			}
			docs = append(docs, doc)
		}
	}
	compareMarkdownWithPi(t, docs)
}

// markdown.ts tokenizeBlockLatex takes `dollarMatch?.[1]` (a non-empty string, not a trimmed one) as the text, so a block of blank lines and spaces is a latex block with empty text, which renders as one empty row.
// Left out because it differs from Pi independently of this rule: `$$ $$` on one line (inline latex with empty text; the row is empty here and padded to the width in Pi at width 1).
func TestMarkdownWhitespaceOnlyLatexBlockMatchesPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{"$$\n \n$$", "$$\n\n$$", "$$\n$$", "$$\t$$\nb", "\\[ \\]", "\\[\n\n\\]\nb", "$$\n\n$$\n\nb", "a\n$$\n  \n$$\nb", "$$\n \n$$\n$$\nx\n$$", "> $$\n>\n> $$", "$$\n\t\n$$\n"})
}
