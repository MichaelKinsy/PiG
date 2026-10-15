package tui

import "testing"

// A line without '>' after a quoted line is a lazy line of the quote's paragraph unless the paragraph rule inside a blockquote (marked's Ae with Me: rule, heading, blockquote, fence, list item, HTML block, blank line; no table, no lheading) interrupts it. Exact rule shapes, spacing and the characters that are not whitespace in a heading or list marker decide it.
func TestMarkdownQuoteLazyLinesMatchPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"> q\n- - -", "> q\n* * *", "> q\n_ _ _", "> q\n---", "> q\n-----", "> q\n- - - -", "> q\n***", "> q\n  ---", "> q\n    ---", "> q\n___x", "> q\n--- x",
		"> q\n#", "> q\n#x", "> q\n# x", "> q\n#\tx", "> q\n####### x", "> q\n  # x", "> q\n    # x", "> q\n#\u00a0x", "> q\n#\u0085x",
		"> q\n~~~\nc\n~~~", "> q\n~~~", "> q\n```x", "> q\n``` `", "> q\n  ```", "> q\n    ```",
		"> q\n<div>", "> q\n<div>x</div>", "> q\n<b>x", "> q\n<!-- c -->", "> q\n</div>", "> q\n<span>",
		"> q\n1. a", "> q\n2. a", "> q\n1) a", "> q\n+ a", "> q\n* a", "> q\n-", "> q\n- a", "> q\n10. a", "> q\n1234567890. a", "> q\n-\ta", "> q\n  - a",
		"> q\n=", "> q\n===", "> q\n    code", "> q\n\tcode", "> q\n[x]: /u", "> q\n> r\ns",
		"> q\n \t", "> q\n  ", "> q\n \t\n", "> q\n \t\nx", "> q\n> \t", "> q\n>  ", "> a\n \t\n> b", "> q\n  \nx", "> q\n\t\nx", "> q\n- \nx", "> q\n1.", "> q\n1.x", "> q\n10) x", "> q\n12345678901. x", "> q\n  1. x", "> q\n   - x", "> q\n    - x", "> q\n</p>", "> q\n<script>", "> q\n<a href>", "> q\n<1>", "> q\n___", "> q\n- - -x",
	})
}

// Marked's blockquote tokenizer lexes the lazy lines after a quoted line on their own: an indented run is indented code that is joined to the quoted paragraph with four leading spaces removed and its newline kept, so the next lazy or quoted line follows an empty row; indented lines after a non-indented lazy line stay in their paragraph as written.
// Pi source: marked 18 Tokenizer.blockquote and Lexer.blockTokens (code joined to a paragraph), packages/tui/src/components/markdown.ts case "blockquote".
// Left out because they differ from Pi independently of this rule: the setext underline `===` after a lazy line, a list item (`- l`) with an indented line, and an indented fence line (`    ```)` before a quoted line (see Update 166 in handoffs/lg-imla-5.md).
func TestMarkdownQuoteLazyIndentedRunMatchesPi(t *testing.T) {
	lines := []string{"x", "    x", "  x", "      x", "after", "> r", ">     r", "", "# h", "    <div>", "<div>", "\tx", "---", "```"}
	var docs []string
	for _, a := range lines {
		for _, b := range lines {
			for _, c := range []string{"", "\nx", "\n    y", "\ny", "\n> z"} {
				docs = append(docs, "> q\n"+a+"\n"+b+c, "> q\n>\n"+a+"\n"+b+c)
			}
		}
	}
	// A setext underline after an indented run joins without the empty row.
	docs = append(docs, "> q\n    x\n===", "> q\n    x\n===\n    y", "> q\n    x\n===\nz", "> q\n    x\n    y\n===")
	compareMarkdownWithPi(t, docs)
}

// A setext underline (= or -- , `-` alone is a bullet and --- a rule) on a lazy line after another lazy line would close a heading in the lazy run, so marked's blockquote tokenizer joins it to the quoted paragraph indented by four spaces whatever its own indent; directly after a quoted line it stays as written, and after an indented run the code tokenizer removes the four spaces again.
// Pi source: marked 18 Tokenizer.blockquote (lazy continuation text) and Lexer.blockTokens.

func TestMarkdownQuoteLazySetextUnderlineIsIndentedMatchesPi(t *testing.T) {
	var docs []string
	for _, pre := range []string{"> q\n", "> q\n> r\n"} {
		for _, lazy := range []string{"x", "  x", "x y", "    x", "x\ny", "x\n    y", "\n"} {
			for _, underline := range []string{"=", "===", "  ===", "   ===", "===  ", "--", " -- ", "====", "= =", "===x", "    ==="} {
				for _, tail := range []string{"", "\ny", "\n===", "\n> z", "\n\nw", "\n    c"} {
					docs = append(docs, pre+lazy+"\n"+underline+tail)
				}
			}
		}
	}
	compareMarkdownWithPi(t, append(docs, "> q\n===", "> q\n  ===", "> q\n--", "> q\n> r\n===\nx"))
}

// An indented lazy line after a quoted line that is not text (a heading, rule, fence or list item) is not joined to a paragraph: it is a code block of its own, so marked's code tokenizer does not remove its four spaces into text.
// Pi source: marked 18 Tokenizer.blockquote and Lexer.blockTokens (code joined only to a text or paragraph token).
// Left out because they differ from Pi independently of this rule: a quoted HTML block, definition, table or list item before the indented line, which marked lexes apart from the lazy line.
func TestMarkdownQuoteIndentedLazyLineAfterABlockMatchesPi(t *testing.T) {
	var docs []string
	// A quoted "===" closes a setext heading only after quoted text ("> q\n> ===") and is text on its own ("> ===").
	for _, pre := range []string{"> # h\n", "> ---\n", "> q\n> # h\n", "> q\n===\n", "> q\n", "> q\n> ===\n", "> ===\n", "> q\n> --\n"} {
		for _, lazy := range []string{"    x", "    x\n    y", "      x", "    x\n> r", "    x\ny", "    # h", "    ```"} {
			for _, tail := range []string{"", "\nz", "\n===", "\n> z", "\n\nw", "\n    c"} {
				docs = append(docs, pre+lazy+tail)
			}
		}
	}
	compareMarkdownWithPi(t, docs)
}

// marked lexes a lazy line apart from the quoted lines before it, so a table delimiter row on a lazy line makes no table with a quoted line above it, however many quoted lines precede it, and it interrupts the paragraph only as the delimiter of a table whose header is lazy too and has as many cells (`a|b`, not `x`).
// Pi source: marked 18 Tokenizer.blockquote and the gfm paragraph rule.
// Left out because it differs from Pi independently of this rule: a quoted list item (`> - a`) followed by a quoted line, the lazy delimiter row and another quoted line.
func TestMarkdownQuoteLazyDelimiterRowAfterSeveralQuotedLinesMatchesPi(t *testing.T) {
	var docs []string
	for _, pre := range []string{"> q\n", "> q\n> r\n", "> q\n> r\n> s\n", "> # h\n> q\n", "> q\n\n> r\n"} {
		for _, d := range []string{"|-|-|", "|---|---|", "-|-", "|-|", " |-|-|", ":-|-:", "a|b\n|-|-|", "|-|-|\n|1|2|", "x\n|-|-|", "|-|-|\nx"} {
			for _, tail := range []string{"", "\ny", "\n> z"} {
				docs = append(docs, pre+d+tail)
			}
		}
	}
	compareMarkdownWithPi(t, docs)
}
