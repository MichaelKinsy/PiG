package tui

// Ports packages/tui/src/components/markdown.ts (render, renderToken, renderList, renderTable and trimPartialClosingFences).

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// renderBlocks renders the blocks of content, a text whose line endings and tabs are normalized. A top-level render passes rec to record the points a later render of a longer text resumes from, and from to start at one: content is then the text from that point's line (its line number is from.line), and the output so far is the point's.
// It returns the lines of every block (loopOut and final are the same: markdown.ts adds no spacing after the last token) and how many leading lines of final are loopOut's.
func (m *Markdown) renderBlocks(content string, width int, from *resumePoint, rec *resumeRecorder) (loopOut, final []string, finalBase int) {
	// pig divergence (D220): every block renders with the inline lexer state at its position. Pi 0f21a331 reuses the lines of a block of the same source from this render or the previous one (markdown.ts:381), so it autolinks a bare URL inside a still-open <a>, which marked does not.
	state := m.lexerState()
	src := state.blockRunes(content)
	tokens := state.blockLexer().lexRunes(src, content)
	trimPartialClosingFences(tokens)
	// Pi main 54280c4ff hides HTML comments; the pinned v1.5.0 renders them as text. Only a source that holds "<!--" has a comment.
	if strings.Contains(content, "<!--") {
		tokens = removeHTMLCommentBlocks(tokens)
	}

	var out []string
	if from != nil {
		out = from.prefix
	} else {
		out = make([]string, 0, len(tokens)*2)
	}
	var lines []string
	barrier, line, scanned := 0, 0, 0
	if rec != nil {
		lines = strings.Split(content, "\n")
		barrier = resumeBarrier(lines)
	}
	for i, token := range tokens {
		if rec != nil {
			for ; scanned < token.offset; scanned++ {
				if src[scanned] == '\n' {
					line++
				}
			}
			atLineStart := token.offset == 0 || src[token.offset-1] == '\n'
			if atLineStart && line > 0 && line <= barrier && line+1 < len(lines) && markdownResumable(lines[line-1], lines[line]) && len(m.lexerState().links) == 0 {
				base := 0
				if from != nil {
					base = from.line
				}
				rec.add(resumePoint{
					line: base + line, rawOff: rec.rawBase + rec.rawStarts[line], outLen: len(out),
					flags: m.lexerState().lexerFlags(), lexedBracket: m.lexerState().lexedBracket,
				})
			}
		}
		next := ""
		if i+1 < len(tokens) {
			next = tokens[i+1].kind
		}
		out = m.renderToken(out, token, width, next, nil)
	}
	return out, out, len(out)
}

// trimPartialClosingFences ends a streamed code block before the partial closing fence of its last line, so the block does not shrink when the fence arrives (markdown.ts trimPartialClosingFences). It only looks at the last token and its last list item or quoted token.
func trimPartialClosingFences(tokens []*markedBlock) {
	token := markedLast(tokens)
	if token == nil {
		return
	}
	switch token.kind {
	case "list":
		if len(token.items) > 0 {
			trimPartialClosingFences(token.items[len(token.items)-1].tokens)
		}
		return
	case "blockquote":
		trimPartialClosingFences(token.tokens)
		return
	case "code":
	default:
		return
	}
	// /^(`{3,}|~{3,})/ on the raw of the token.
	fence := byte(0)
	if token.raw != "" && (token.raw[0] == '`' || token.raw[0] == '~') {
		fence = token.raw[0]
	}
	markerLength := 0
	for markerLength < len(token.raw) && token.raw[markerLength] == fence {
		markerLength++
	}
	lastLine := token.raw[strings.LastIndexByte(token.raw, '\n')+1:]
	if fence == 0 || markerLength < 3 || lastLine == "" || len(lastLine) >= markerLength || lastLine != strings.Repeat(string(fence), len(lastLine)) {
		return
	}
	text := jsstring.Slice(token.text, 0, -len(lastLine))
	token.text = strings.TrimSuffix(text, "\n")
}

// renderToken appends the lines of one block token to out (markdown.ts renderToken). next is the type of the token that follows it, which decides its spacing, and ctx the style context of its inline text, or nil for the document's.
func (m *Markdown) renderToken(out []string, token *markedBlock, width int, next string, ctx *inlineStyleContext) []string {
	theme := m.markdownTheme()
	spaced := next != "" && next != "space"
	switch token.kind {
	case "heading":
		out = append(out, m.headingInline(token.entry.src, token.depth))
		if spaced {
			// Add spacing after headings (unless a space token follows).
			out = append(out, "")
		}
	case "paragraph":
		out = append(out, m.renderEntry(token.entry, ctx))
		// Don't add spacing if the next token is a space or a list.
		if spaced && next != "list" {
			out = append(out, "")
		}
	case "text":
		out = append(out, m.renderEntry(token.entry, ctx))
	case "latexBlock":
		block := widthx.JSTrim(token.raw)
		if m.latexEnabled() {
			block = renderBlockLatex(latexToken{raw: token.raw, text: token.text, pending: token.pending}, width)
		}
		for line := range strings.SplitSeq(block, "\n") {
			out = append(out, m.applyDefaultStyle(line))
		}
		if spaced {
			out = append(out, "")
		}
	case "code":
		out = append(out, m.renderCodeBlock(token.lang, []string{token.text})...)
		if spaced {
			// Add spacing after code blocks (unless a space token follows).
			out = append(out, "")
		}
	case "list":
		out = m.renderList(out, token, 0, width, ctx)
	case "table":
		out = m.renderMarkdownTable(out, token, width, ctx)
		if spaced && len(token.header) > 0 {
			out = append(out, "")
		}
	case "blockquote":
		out = m.renderQuote(out, token, width, next, ctx)
	case "hr":
		out = append(out, theme.Hr(strings.Repeat("─", min(width, 80))))
		if spaced {
			out = append(out, "")
		}
	case "html":
		// HTML renders as plain text without its comments, as in Pi main 54280c4ff.
		out = append(out, m.applyDefaultStyle(widthx.JSTrim(stripHTMLComments(token.raw))))
	case "space":
		// Space tokens represent blank lines in markdown.
		out = append(out, "")
	}
	return out
}

// renderQuote appends the rows of a blockquote token. A quote narrower than a border renders its content without one and adds no spacing of its own (Pi 8a2ebd385).
func (m *Markdown) renderQuote(out []string, token *markedBlock, width int, next string, ctx *inlineStyleContext) []string {
	theme := m.markdownTheme()
	quoteStyle := func(text string) string { return theme.Quote(theme.Italic(text)) }
	prefix := markdownStylePrefix(quoteStyle)

	// With no room for another border ("│ " plus at least one content column), render the nested content without one. Otherwise each extra level re-wraps every child line at width 1 and the output grows exponentially with nesting depth.
	if width < 3 {
		for i, child := range token.tokens {
			out = m.renderToken(out, child, width, markedKind(token.tokens, i+1), ctx)
		}
		return out
	}

	// Calculate the width for quote content: the border "│ " takes two columns.
	contentWidth := width - 2
	// Blockquotes contain block-level tokens, so their children render as blocks; the default message style does not apply inside them.
	quoteContext := &inlineStyleContext{applyText: func(s string) string { return s }, stylePrefix: prefix}
	var logical []string
	for i, child := range token.tokens {
		logical = m.renderToken(logical, child, contentWidth, markedKind(token.tokens, i+1), quoteContext)
	}
	// Avoid rendering an extra empty quote line before the outer blockquote spacing.
	for len(logical) > 0 && logical[len(logical)-1] == "" {
		logical = logical[:len(logical)-1]
	}
	for _, line := range logical {
		if prefix != "" {
			line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+prefix)
		}
		for _, wrapped := range widthx.WrapTextWithAnsi(quoteStyle(line), contentWidth) {
			out = append(out, theme.QuoteBorder("│ ")+wrapped)
		}
	}
	if next != "" && next != "space" {
		// Add spacing after blockquotes (unless a space token follows).
		out = append(out, "")
	}
	return out
}

// markedKind is the type of tokens[index], or "" past the end.
func markedKind(tokens []*markedBlock, index int) string {
	if index >= len(tokens) {
		return ""
	}
	return tokens[index].kind
}

// markdownOrderedMarker is getOrderedListMarker: /^(?: {0,3})(\d{1,9}[.)])[ \t]+/ on the raw of an item, and a space after it.
func markdownOrderedMarker(raw string) (string, bool) {
	spaces := 0
	for spaces < len(raw) && spaces < 3 && raw[spaces] == ' ' {
		spaces++
	}
	digits := 0
	for spaces+digits < len(raw) && digits < 9 && raw[spaces+digits] >= '0' && raw[spaces+digits] <= '9' {
		digits++
	}
	end := spaces + digits + 1
	if digits == 0 || end >= len(raw) || raw[end-1] != '.' && raw[end-1] != ')' || raw[end] != ' ' && raw[end] != '\t' {
		return "", false
	}
	return raw[spaces:end] + " ", true
}

// markdownUnorderedMarker is getUnorderedListMarker: /^(?: {0,3})([-+*])(?:[ \t]+|(?=\r?\n|$))/ on the raw of an item, and a space after it.
func markdownUnorderedMarker(raw string) (string, bool) {
	spaces := 0
	for spaces < len(raw) && spaces < 3 && raw[spaces] == ' ' {
		spaces++
	}
	if spaces >= len(raw) || raw[spaces] != '-' && raw[spaces] != '+' && raw[spaces] != '*' {
		return "", false
	}
	end := spaces + 1
	rest := raw[end:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '\n' && !strings.HasPrefix(rest, "\r\n") {
		return "", false
	}
	return raw[spaces:end] + " ", true
}

// renderList appends the rows of a list with its nesting (markdown.ts renderList).
func (m *Markdown) renderList(out []string, list *markedBlock, depth, width int, ctx *inlineStyleContext) []string {
	theme := m.markdownTheme()
	indent := strings.Repeat("    ", depth)
	start := 1
	if list.ordered {
		start = list.start
	}
	for index, item := range list.items {
		last := index == len(list.items)-1
		var bullet string
		switch {
		case list.ordered:
			bullet = strconv.Itoa(start+index) + ". "
			if marker, ok := markdownOrderedMarker(item.raw); m.options.PreserveOrderedListMarkers && ok {
				bullet = marker
			}
		default:
			bullet = "- "
			if marker, ok := markdownUnorderedMarker(item.raw); m.options.PreserveOrderedListMarkers && ok {
				bullet = marker
			}
		}
		task := ""
		if item.task {
			task = "[ ] "
			if item.checked {
				task = "[x] "
			}
		}
		marker := bullet + task
		first := indent + theme.ListBullet(marker)
		continuation := indent + strings.Repeat(" ", widthx.VisibleWidth(marker))
		itemWidth := max(1, width-widthx.VisibleWidth(first))
		rendered := false

		var lines []string
		for _, itemToken := range item.tokens {
			if itemToken.kind == "list" {
				out = m.renderList(out, itemToken, depth+1, width, ctx)
				rendered = true
				continue
			}
			lines = m.renderToken(lines[:0], itemToken, itemWidth, "", ctx)
			for _, line := range lines {
				for _, wrapped := range widthx.WrapTextWithAnsi(line, itemWidth) {
					prefix := continuation
					if !rendered {
						prefix = first
					}
					out = append(out, prefix+wrapped)
					rendered = true
				}
			}
		}
		if !rendered {
			out = append(out, first)
		}
		if list.loose && !last {
			out = append(out, "")
		}
	}
	return out
}
