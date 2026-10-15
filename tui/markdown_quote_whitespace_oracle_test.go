package tui

import (
	"strings"
	"testing"
)

// marked lexes a blockquote's lazy lines apart from its quoted lines. A quoted line of spaces or tabs that ends the quoted text has no newline after it, so it stays in the quoted paragraph and the lazy lines join that paragraph
// (`> q`, `>  `, `x` is one paragraph "q\n \nx"); a quoted line with nothing after the marker is blank and ends the paragraph.
// Pi source: marked 18 Tokenizer.blockquote and Tokenizer.paragraph; packages/tui/src/components/markdown.ts renderToken "blockquote".
func TestMarkdownQuotedWhitespaceLineBeforeALazyLineMatchesPi(t *testing.T) {
	var docs []string
	for _, space := range []string{" ", "  ", "\t", " \t", "\u00a0", "\u3000"} {
		for _, template := range []string{
			"> q\n>%s\nx", "> q\n>%s\n  x", "> q\n>%s\nx\ny", "> q\n>%s\n# h", "> q\n>%s\n- x", "> q\n>%s\n1. x", "> q\n>%s\n---", "> q\n>%s\n```\nc\n```",
			"> q\n>%s\n<div>x</div>", "> q\n>%s\n    code", "> q\n>%s\n| a |\n|---|", "> q\n>%s\n> x", "> q\n>%s\n>%s\nx", "> q\n>%s\nx\n\ny", "> q\n>%s\n[a]: b",
			"> a\n> b\n>%s\nx", "> q\n>%s\n\nx", "> q\n>%s\n%s\nx", "> q\n>%s", "> q\n>%s\n", "> a\n>%s\n>\n> b", "> # h\n>%s\nx", "> ```\n> c\n>%s\nx", "> [a]: b\n>%s\nx",
		} {
			docs = append(docs, strings.ReplaceAll(template, "%s", space))
		}
	}
	compareMarkdownWithPi(t, docs)
}

// A line after a quote that is blank only to JavaScript's trim (a no-break or ideographic space) is a paragraph, not a space token, so it gets the blank row a block after a quote gets.
// Pi source: marked 18 Tokenizer.space (/^(?:[ \t]*(?:\n|$))+/) and packages/tui/src/components/markdown.ts renderToken "blockquote".
func TestMarkdownLineOfUnicodeSpaceAfterAQuoteIsAParagraphMatchesPi(t *testing.T) {
	var docs []string
	for _, space := range []string{"\u00a0", "\u3000", "\u2028", "\ufeff", "\u0085", "\u200b", " \u00a0", "\u00a0 "} {
		for _, template := range []string{"> q\n>\n%s", "> q\n>\n%s\nz", "> q\n\n%s", "> q\n%s", "> q\n> %s\nx", "> q\n>%s\nx", "> q\n> %s\n> x", "> q\n>\n%s\n>x", "> - a\n>\n%s", "> # h\n%s", "> # h\n>\n%s", "> ```\n> c\n> ```\n>\n%s"} {
			docs = append(docs, strings.ReplaceAll(template, "%s", space))
		}
	}
	compareMarkdownWithPi(t, docs)
}

// A last line of spaces after an indented code block, an HTML block or a table is read into that block's raw (code `(?:\n[ \t]*(?:\n|$))*`, HTML `|$`, table rows), and a lone space left after a block is one character that Lexer.lex folds into the block's raw; none is a space token, so the text ends without the empty row a space token adds.
// A fence after an HTML comment gets no spacing row because markdown.ts case "html" adds none. A quoted line of spaces with no paragraph before it leaves an indented lazy line a code token of its own.
// Pi source: marked 18 Tokenizer.code/html/table and Lexer.lex; packages/tui/src/components/markdown.ts renderToken "html" and "code".
func TestMarkdownTrailingSpaceLineAndHTMLBeforeFenceMatchPi(t *testing.T) {
	var docs []string
	for _, base := range []string{"    code", "<div>", "<div>x", "<!-- c -->", "<pre>\nx", "```\nc\n```", "> q", "- a", "# h", "---", "[a]: b", "| a |\n|---|\n| b |", "x"} {
		for _, tail := range []string{"\n", "\n\n", "\n\n\n", "\n ", "\n  ", "\n\t", "\n \n", "\n\n ", "\n \n ", "\n \n\n", "\n\n\n "} {
			docs = append(docs, base+tail)
		}
	}
	for _, html := range []string{"<!-- c -->", "<div>x</div>", "<pre>\nx\n</pre>"} {
		docs = append(docs, html+"\n```", html+"\n```\nx\n```", html+"\n\n```", html+"\n```\nx\n```\ny")
	}
	for _, space := range []string{" ", "  ", "\t"} {
		docs = append(docs, ">"+space+"\n    code", ">"+space+"\n    code\n    more", "> a\n>"+space+"\n    code", ">\n>"+space+"\n    code", "> q\n>"+space+"\n    code")
	}
	compareMarkdownWithPi(t, docs)
}

// marked's GFM table rule reads a line and a delimiter row after it as a table header, so a paragraph ends before an indented line that a `---` row follows; the code token that line then starts cannot interrupt the paragraph, so Lexer.blockTokens joins it to the paragraph's text, indent removed.
// Pi source: marked 18 Lexer.blockTokens (code after a paragraph) and Tokenizer.paragraph (table lookahead).
func TestMarkdownIndentedLineBeforeDelimiterRowJoinsTheParagraphMatchesPi(t *testing.T) {
	var docs []string
	for _, tail := range []string{"---", "===", "|---|", "---\nb", "\n---", "", "\n\nb", "|---|---|", ":-:", "-"} {
		for _, code := range []string{"    code", "    code\n    more", "\tcode", "    code\n  \n    more", "    a | b"} {
			docs = append(docs, "a\n"+code+"\n"+tail, "x **y**\nz\n"+code+"\n"+tail, "- a\n"+code+"\n"+tail)
		}
	}
	compareMarkdownWithPi(t, docs)
}
