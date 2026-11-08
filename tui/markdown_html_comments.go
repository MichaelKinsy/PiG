package tui

// Ports packages/tui/src/components/markdown.ts at Pi main 54280c4ff (stripHtmlComments, removeHtmlCommentBlocks and the
// comment skip of renderInlineTokens), which hides HTML comments. the pinned v1.5.0 renders a comment as
// text.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// stripHTMLComments is html with every comment removed (HTML_COMMENT_REGEX, /<!--[\s\S]*?(?:-->|$)/g). An unclosed comment
// runs to the end of the text, as in CommonMark, so streamed comments stay hidden. The comment's closing "-->" is looked for
// after the opening "<!--", so "<!-->" does not close itself.
func stripHTMLComments(html string) string {
	start := strings.Index(html, "<!--")
	if start < 0 {
		return html
	}
	var out strings.Builder
	out.Grow(len(html))
	for start >= 0 {
		out.WriteString(html[:start])
		rest := html[start+len("<!--"):]
		_, after, ok := strings.Cut(rest, "-->")
		if !ok {
			return out.String()
		}
		html = after
		start = strings.Index(html, "<!--")
	}
	out.WriteString(html)
	return out.String()
}

// removeHTMLCommentBlocks removes the HTML tokens that hold only comments, so they render no row, in the tokens and in those of
// every list item and quote (markdown.ts removeHtmlCommentBlocks). A removed comment between two blank lines, or at the start
// or end, also drops one blank line, so it leaves no extra gap. It returns the tokens that remain.
func removeHTMLCommentBlocks(tokens []*markedBlock) []*markedBlock {
	for i := len(tokens) - 1; i >= 0; i-- {
		token := tokens[i]
		switch {
		case token.kind == "list":
			for _, item := range token.items {
				item.tokens = removeHTMLCommentBlocks(item.tokens)
			}
		case token.kind == "blockquote":
			token.tokens = removeHTMLCommentBlocks(token.tokens)
		case token.kind == "html" && widthx.JSTrim(stripHTMLComments(token.raw)) == "":
			tokens = append(tokens[:i], tokens[i+1:]...)
			switch {
			case i < len(tokens) && tokens[i].kind == "space" && (i == 0 || tokens[i-1].kind == "space"):
				tokens = append(tokens[:i], tokens[i+1:]...)
			case i == len(tokens) && i > 0 && tokens[i-1].kind == "space":
				// markdown.ts splices tokens[--i], which also steps the loop past the removed space.
				i--
				tokens = append(tokens[:i], tokens[i+1:]...)
			}
		}
	}
	return tokens
}

// skipsHTMLComment reports whether the inline HTML token at i is a comment, which renders nothing, and whether the text token
// after it then loses its leading blanks: only when the token before it ended with one (or there is none), so "a <!-- c --> b"
// renders as "a b" (markdown.ts renderInlineTokens).
func skipsHTMLComment(tokens []markedToken, i int) (comment, dropLeadingSpace bool) {
	if !strings.HasPrefix(tokens[i].raw, "<!--") {
		return false, false
	}
	if i == 0 {
		return true, true
	}
	raw := tokens[i-1].raw
	return true, raw != "" && (raw[len(raw)-1] == ' ' || raw[len(raw)-1] == '\t')
}

// dropsLeadingSpace is the text token after a skipped comment with its leading spaces and tabs removed when dropLeadingSpace
// is set and it starts with one.
func dropsLeadingSpace(token markedToken, dropLeadingSpace bool) markedToken {
	if dropLeadingSpace && token.kind == markedText && token.text != "" && (token.text[0] == ' ' || token.text[0] == '\t') {
		token.text = strings.TrimLeft(token.text, " \t")
	}
	return token
}
