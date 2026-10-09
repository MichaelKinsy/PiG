package tui

import (
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var listItemPattern = lazyregexp.New(`^( *)([-+*]|\d{1,9}[.)])(?:[ \t]+(.*)|$)`)

// Markdown renders themed text with terminal-cell wrapping, padding and cached display transforms.
// Ports packages/tui/src/components/markdown.ts.
type Markdown struct {
	invalidatable
	Content              string
	paddingX, paddingY   int
	theme                *MarkdownTheme
	defaultTextStyle     *DefaultTextStyle
	options              MarkdownOptions
	styleContext         *inlineStyleContext
	styleOwner           *Markdown
	suppressBlockSpacing bool
	// lazyUnderlines are the indexes of a quote's lazy continuation lines that look like setext underlines.
	lazyUnderlines        []int
	defaultColorSet       bool
	defaultStylePrefix    string
	hasDefaultStylePrefix bool
	// inlineState is marked's lexer state for the document being rendered; child components for quotes and list items share it.
	inlineState *markedInlineState
	// Transform is an optional display-only rewrite of Content applied at the
	// render width before parsing, mirroring upstream MarkdownOptions.transform
	// (markdown.ts). Used to replace Mermaid code blocks with rendered diagrams.
	// A Transform that reads state outside (Content, width) must report it through
	// TransformState, or the render cache will serve a stale result.
	Transform func(markdown string, width int) string
	// TransformState reports external transform inputs so a retained Markdown component invalidates cached lines when those inputs change without a Content or width change.
	TransformState func() string
	// AsyncTransform rewrites off-loop and withholds new content until the complete rewrite is ready. During replacement it retains only a previously completed frame at the current width.
	AsyncTransform *AsyncMarkdownTransform
	asyncState     asyncMarkdownState
	revision       atomic.Uint64
	// defaultColor styles ordinary text tokens; explicit Markdown styles remain independent. Empty means the terminal-default foreground.
	defaultColor  string
	defaultItalic bool
	// Render cache: mirrors upstream markdown.ts cachedLines/cachedText/cachedWidth.
	// Short-circuits Render() when content and width haven't changed.
	cachedContent        string
	cachedWidth          int
	cachedLines          []string
	cachedTransformState string
	cachedTheme          *Theme
	cachedRevision       uint64
	cachedTransformed    string
}

func NewMarkdown(content string) *Markdown {
	return NewMarkdownWithOptions(content, 0, 0, nil, nil, nil)
}

// Invalidate reruns the display transform and parser on the next render, even when the source text is unchanged.
func (m *Markdown) Invalidate() {
	m.revision.Add(1)
	m.invalidatable.Invalidate()
}

// IsDirty reports true while TransformState reads external state or an async transform can publish between frames, so a parent Container re-renders the child and the Markdown cache key decides whether the lines changed.
func (m *Markdown) IsDirty() bool {
	return m.TransformState != nil || m.AsyncTransform != nil || m.invalidatable.IsDirty()
}

// pig additive (D91): SurfaceLive reports the transforms that change the lines without
// invalidating the Markdown, as IsDirty does.
func (m *Markdown) SurfaceLive() bool { return m.TransformState != nil || m.AsyncTransform != nil }

// Dispose revokes this component's pending publication and removes its queued transform.
func (m *Markdown) Dispose() { m.asyncState.dispose() }

// SetDefaultColor sets the ANSI foreground applied to ordinary Markdown text tokens. Headings, code, list markers and blockquotes retain their own theme styles. Passing an empty string restores terminal-default foreground and invalidates the cache.
func (m *Markdown) SetDefaultColor(open string) {
	if m.defaultColorSet && m.defaultColor == open {
		return
	}
	m.defaultColor = open
	m.defaultColorSet = true
	m.hasDefaultStylePrefix = false
	m.Invalidate()
}

// applyDefaultStyle wraps text tokens, leaving code spans and other explicitly themed tokens independent.
func (m *Markdown) applyDefaultStyle(s string) string {
	theme := m.markdownTheme()
	style := m.defaultTextStyle
	if m.defaultColorSet {
		if m.defaultColor != "" {
			s = m.defaultColor + s + FgClose(m.defaultColor)
		}
	} else if style != nil && style.Color != nil {
		s = style.Color(s)
	}
	if style != nil && style.Bold {
		s = theme.Bold(s)
	}
	if m.defaultItalic || (style != nil && style.Italic) {
		s = theme.Italic(s)
	}
	if style != nil && style.Strikethrough {
		s = theme.Strikethrough(s)
	}
	if style != nil && style.Underline {
		s = theme.Underline(s)
	}
	return s
}

func (m *Markdown) inlineMarkdown(s string) string {
	return m.renderInlineMarkdown(s, m.defaultInlineStyleContext())
}

func (m *Markdown) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	transformState := ""
	if m.TransformState != nil {
		transformState = m.TransformState()
	}
	contentWidth := max(1, width-m.paddingX*2)
	content := m.Content
	revision := m.revision.Load()
	if m.AsyncTransform != nil {
		var ready bool
		content, ready = m.asyncState.resolve(m.AsyncTransform, markdownTransformInput{text: content, width: contentWidth, state: transformState, theme: ActiveTheme(), revision: revision})
		if !ready {
			if m.cachedWidth == width {
				return m.cachedLines
			}
			return nil
		}
	}
	if m.cachedLines != nil && m.cachedContent == m.Content && m.cachedWidth == width &&
		m.cachedTransformState == transformState && (m.theme != nil || m.cachedTheme == ActiveTheme()) && m.cachedRevision == revision &&
		(m.AsyncTransform == nil || m.cachedTransformed == content) {
		return m.cachedLines
	}
	if m.Transform != nil && m.AsyncTransform == nil {
		content = m.Transform(content, contentWidth)
	}
	out := []string{}
	if widthx.JSTrim(content) != "" {
		content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
		source := strings.ReplaceAll(content, "\t", "   ")
		state := markedInlineStates.Get().(*markedInlineState)
		state.startDocument()
		m.inlineState = state
		rendered := m.renderContent(source, contentWidth)
		if state.relex {
			state.restartWithLinks()
			rendered = m.renderContent(source, contentWidth)
		}
		m.inlineState = nil
		markedInlineStates.Put(state)
		lines := wrapRenderedLines(rendered, contentWidth)
		margin := strings.Repeat(" ", m.paddingX)
		background := func(line string) string { return line }
		if m.defaultTextStyle != nil && m.defaultTextStyle.BgColor != nil {
			background = m.defaultTextStyle.BgColor
		}
		for _, line := range lines {
			if widthx.IsImageLine(line) {
				out = append(out, line)
				continue
			}
			line = margin + line + margin
			line += strings.Repeat(" ", max(0, width-widthx.VisibleWidth(line)))
			out = append(out, background(line))
		}
		emptyLines := make([]string, m.paddingY)
		for i := range emptyLines {
			emptyLines[i] = background(strings.Repeat(" ", width))
		}
		padded := make([]string, 0, len(out))
		padded = append(padded, emptyLines...)
		padded = append(padded, out...)
		padded = append(padded, emptyLines...)
		out = padded
		// markdown.ts render returns [""] when text that is not blank renders no lines (only link definitions).
		if len(out) == 0 {
			out = []string{""}
		}
	}
	m.cachedContent, m.cachedWidth, m.cachedLines = m.Content, width, out
	m.cachedTransformState, m.cachedTheme = transformState, ActiveTheme()
	m.cachedRevision, m.cachedTransformed = revision, content
	return out
}

func (m *Markdown) renderContent(content string, width int) []string {
	lines := strings.Split(content, "\n")
	source := &markdownBlockSource{lines: lines}
	out := make([]string, 0, len(lines))

	// silentEnd is len(out) after the last block that rendered no lines: a link definition, or a quote holding only definitions. Pi still renders such a block as a token, so the blank line after it is not merged with a blank before it, and blanks before it do not trail the output.
	silentEnd := -1
	// emptyBlockRow is set after a block whose own row is empty (an empty heading), so the blank row that follows it is not taken for a duplicate.
	emptyBlockRow, sawEmptyHeading := false, false
	blank := func() {
		if emptyBlockRow {
			emptyBlockRow = false
			out = append(out, "")
			return
		}
		if len(out) == 0 || out[len(out)-1] == "" {
			return
		}
		out = append(out, "")
	}
	emitBlank := blank
	emitSpace := func() {
		if len(out) == silentEnd {
			out = append(out, "")
			return
		}
		blank()
	}
	if m.suppressBlockSpacing {
		emitBlank = func() {}
	}

	// Block text wraps here; list rendering adds its own first-line and continuation prefixes.
	emit := func(line string) { out = append(out, line) }

	inCode := false
	codeLang := ""
	codeFence := byte(0)
	codeFenceLen := 0
	var codeLines []string
	theme := m.markdownTheme()
	listEnd := -1

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		nextLine := ""
		if i+1 < len(lines) {
			nextLine = lines[i+1]
		}

		if inCode {
			if isMarkdownFenceClose(line, codeFence, codeFenceLen) {
				out = append(out, m.renderCodeBlock(codeLang, codeLines)...)
				if strings.TrimSpace(nextLine) != "" {
					emitBlank()
				}
				inCode = false
				codeLang = ""
				codeFence = 0
				codeFenceLen = 0
				continue
			}
			codeLines = append(codeLines, line)
			continue
		}
		// marked's indented code rule precedes fences; a paragraph has already absorbed an indented line it cannot be interrupted by.
		if code, next, ok := source.indentedCode(i); ok {
			out = append(out, m.renderCodeBlock("", code)...)
			if next < len(lines) && strings.TrimSpace(lines[next]) != "" {
				emitBlank()
			}
			i = next - 1
			continue
		}
		if fence, fenceLen, lang, ok := parseMarkdownFenceOpen(line); ok {
			// A list token adds no trailing blank row of its own (markdown.ts case "list"), so a fence that directly follows a list gets none.
			if i != listEnd {
				emitBlank()
			}
			inCode = true
			codeFence = fence
			codeFenceLen = fenceLen
			codeLang = lang
			codeLines = nil
			continue
		}

		// Block LaTeX ($$...$$, \[...\]): may span multiple lines. Checked
		// before paragraph handling and after code fences (code wins), mirroring
		// upstream markdown.ts block latex extension precedence.
		if blockLatexStart(line) {
			if tok, ok := tokenizeBlockLatex(strings.Join(lines[i:], "\n")); ok {
				block := widthx.JSTrim(tok.raw)
				if m.latexEnabled() {
					block = renderBlockLatex(tok)
				}
				for bl := range strings.SplitSeq(block, "\n") {
					emit(m.applyDefaultStyle(bl))
				}
				consumed := strings.Count(tok.raw, "\n")
				if strings.HasSuffix(tok.raw, "\n") {
					consumed--
				}
				i += consumed
				if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
					emitBlank()
				}
				continue
			}
		}

		if depth, text, ok := parseATXHeading(line); ok {
			emit(m.headingInline(text, depth))
			emptyBlockRow = text == "" && depth < 3
			sawEmptyHeading = sawEmptyHeading || emptyBlockRow
			if strings.TrimSpace(nextLine) != "" {
				emitBlank()
			}
			continue
		}

		if isMarkedHr(line) {
			out = append(out, theme.Hr(strings.Repeat("─", min(width, 80))))
			if strings.TrimSpace(nextLine) != "" {
				emitBlank()
			}
			continue
		}

		// Blockquote blocks, including lazy continuation lines.
		if isBlockquoteLine(line) {
			j := i
			quoted := make([]string, 0, 4)
			var lazyUnderlines []int
			for j < len(lines) {
				current := lines[j]
				switch {
				case isBlockquoteLine(current):
					quoted = append(quoted, stripBlockquotePrefix(current))
				case j > i && isLazyBlockquoteContinuation(current):
					// marked lexes lazy lines apart from the quoted lines before them and joins a paragraph there to the quoted one, so a lazy setext underline continues the quoted paragraph instead of closing a heading.
					if setextUnderlinePattern.MatchString(current) {
						lazyUnderlines = append(lazyUnderlines, len(quoted))
					}
					quoted = append(quoted, current)
				default:
					goto renderQuote
				}
				j++
			}
		renderQuote:
			quote := m.renderQuote(strings.Join(quoted, "\n"), lazyUnderlines, width)
			if len(quote) == 0 {
				silentEnd = len(out)
			}
			out = append(out, quote...)
			if j < len(lines) && widthx.JSTrim(lines[j]) != "" {
				emitBlank()
			}
			i = j - 1
			continue
		}

		// List items contain blocks as well as inline text; nested lists use depth-based indentation.
		if indent, _, _, ok := parseMarkdownListItem(line); ok && len(indent) <= 3 {
			list, next := parseMarkdownList(lines, i)
			out = append(out, m.renderList(list, 0, width)...)
			i = next - 1
			listEnd = next
			continue
		}

		// marked tries an HTML block, then a GFM table, after lists.
		if raw, next, ok := source.htmlBlock(i); ok {
			emit(m.applyDefaultStyle(widthx.JSTrim(raw)))
			i = next - 1
			continue
		}
		// marked tries a link reference definition after an HTML block (Lexer.ts:209-225); it registers the definition and renders nothing.
		if tag, href, next, ok := source.def(i); ok {
			m.lexerState().defineLink(tag, href)
			silentEnd = len(out)
			i = next - 1
			continue
		}
		if table, next, ok := source.table(i); ok {
			out = append(out, m.renderMarkdownTable(table, width)...)
			if next < len(lines) && strings.TrimSpace(lines[next]) != "" {
				emitBlank()
			}
			i = next - 1
			continue
		}

		// Empty line → blank
		if strings.TrimSpace(line) == "" {
			emitSpace()
			continue
		}

		// marked tries a setext heading before a paragraph: the lines up to an underline, unless a blank line or one of its interrupting blocks comes first. A rule line does not interrupt it.
		if end, depth, ok := setextHeadingEnd(lines, i, m.lazyUnderlines); ok {
			emit(m.headingInline(widthx.JSTrim(strings.Join(lines[i:end], "\n")), depth))
			if end+1 < len(lines) && strings.TrimSpace(lines[end+1]) != "" {
				emitBlank()
			}
			i = end
			continue
		}

		// Lexer.ts:241-270 clips a top-level paragraph where a block extension's start() matches src.slice(1). pi-tui's latexBlock start(), /(?:^|\n) {0,3}(?:\$\$|\\\[)/ (packages/tui/src/components/markdown.ts:127-130), also matches at the start of that slice: "$$" or "\[" right after the first character, behind at most three spaces, leaves that character as the paragraph and the rest of the line is lexed next. A rest that is a latex block ends the paragraph; otherwise the paragraph that follows joins the clipped one (lastParagraphClipped). List items lex text rather than top-level paragraphs and are never clipped.
		var clipped []string
		latexRest := false
		for !m.suppressBlockSpacing && !latexRest {
			first, size := utf8.DecodeRuneInString(line)
			// slice(1) splits an astral character, and the lone surrogate it leaves cannot start the match.
			if first > 0xFFFF || !blockLatexStart(line[size:]) {
				break
			}
			clipped = append(clipped, line[:size])
			line = line[size:]
			lines[i] = line
			source.runes, source.offsets = nil, nil
			latexRest = markdownLatexBlockAt(lines, i)
		}
		if latexRest {
			emit(m.inlineMarkdown(strings.Join(clipped, "\n")))
			emitBlank()
			i--
			continue
		}

		// marked's GFM paragraph runs until a line its rule says interrupts it; the latex block extension also clips it. Soft line breaks stay inside the paragraph until styling and wrapping.
		j := i + 1
		for j < len(lines) && !source.paragraphInterrupted(j) && !markdownLatexBlockAt(lines, j) {
			j++
		}
		// A definition right after a paragraph (one the GFM table rule interrupted) joins the paragraph's text and is not registered (Lexer.ts:212-216). A definition covers whole lines, so the text is still the source lines.
		for j < len(lines) {
			_, _, next, ok := source.def(j)
			if !ok {
				break
			}
			j = next
		}
		text := strings.Join(lines[i:j], "\n")
		if len(clipped) > 0 {
			text = strings.Join(clipped, "\n") + "\n" + text
		}
		emit(m.inlineMarkdown(text))
		// Upstream's paragraph adds spacing unless the next token is a space or a list.
		if j < len(lines) && markdownParagraphFollowedByBlock(lines[j]) {
			emitBlank()
		}
		i = j - 1
	}

	// Unclosed code block
	if inCode {
		if len(codeLines) > 0 {
			last := codeLines[len(codeLines)-1]
			if len(last) > 0 && len(last) < codeFenceLen && last == strings.Repeat(string(codeFence), len(last)) {
				codeLines = codeLines[:len(codeLines)-1]
			}
		}
		out = append(out, m.renderCodeBlock(codeLang, codeLines)...)
	}
	for len(out) > max(silentEnd, 0) && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	// A text that holds only empty headings still shows the heading's empty row.
	if len(out) == 0 && sawEmptyHeading {
		out = append(out, "")
	}
	// marked ends the token stream with a space token when the text ends in a blank line, and Pi renders it as one empty row (markdown.ts case "space").
	if len(out) > 0 && len(lines) >= 2 && lines[len(lines)-1] == "" && strings.TrimSpace(lines[len(lines)-2]) == "" {
		out = append(out, "")
	}
	return out
}

// markdownParagraphFollowedByBlock reports whether the line that ended a paragraph starts a token other than space or list, after which upstream adds a blank line.
func markdownParagraphFollowedByBlock(line string) bool {
	if strings.TrimLeft(line, " \t") == "" {
		return false
	}
	if indent, _, _, ok := parseMarkdownListItem(line); ok && len(indent) <= 3 {
		return isMarkedHr(line)
	}
	return true
}

var (
	setextUnderlinePattern = lazyregexp.New(`^ {0,3}(=+|-+) *$`)
	// The blocks that end a setext heading's text in marked's gfm lheading rule; the tag and table-delimiter forms need a following line.
	setextInterruptPattern = lazyregexp.New("^(?: {0,3}(?:[*+-]|\\d{1,9}[.)]) | {4}| {0,3}\\t| {0,3}(?:`{3,}|~{3,})| {0,3}>| {0,3}#{1,6}(?:\\s|$))")
	setextTagLinePattern   = lazyregexp.New(`^ {0,3}<[^>]+>$`)
	setextTablePattern     = lazyregexp.New(`^ {0,3}\|?(?:[:\- ]*\|)+[:\- ]*$`)
)

// setextHeadingEnd applies marked's gfm lheading rule at lines[start]: it returns the index of the first "=" or "-" underline and the heading depth when no blank or interrupting line comes before it.
// Lines whose index is in lazy are lazy blockquote continuations, which marked never reads as an underline.
func setextHeadingEnd(lines []string, start int, lazy []int) (end, depth int, ok bool) {
	interrupts := func(index int) bool {
		line := lines[index]
		hasNext := index+1 < len(lines)
		return setextInterruptPattern.MatchString(line) || hasNext && (setextTagLinePattern.MatchString(line) || setextTablePattern.MatchString(line))
	}
	if interrupts(start) {
		return 0, 0, false
	}
	for index := start + 1; index < len(lines); index++ {
		if match := setextUnderlinePattern.FindStringSubmatch(lines[index]); match != nil && !slices.Contains(lazy, index) {
			if match[1][0] == '=' {
				return index, 1, true
			}
			return index, 2, true
		}
		if strings.TrimSpace(lines[index]) == "" || interrupts(index) {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// wrapRenderedLines is upstream Markdown.render's final pass: every rendered
// non-image line goes through wrapTextWithAnsi(line, contentWidth), so no
// block renderer (tables, code, quotes, lists) can emit a row wider than the
// component. wrapTextWithAnsi returns a fitting line unchanged, so only
// overflowing rows are touched.
func wrapRenderedLines(lines []string, width int) []string {
	for i, line := range lines {
		if widthx.IsImageLine(line) || (!strings.ContainsAny(line, "\r\n") && widthx.VisibleWidth(line) <= width) {
			continue
		}
		out := append([]string(nil), lines[:i]...)
		for _, l := range lines[i:] {
			if widthx.IsImageLine(l) || (!strings.ContainsAny(l, "\r\n") && widthx.VisibleWidth(l) <= width) {
				out = append(out, l)
				continue
			}
			out = append(out, widthx.WrapTextWithAnsi(l, width)...)
		}
		return out
	}
	return lines
}

func parseMarkdownFenceOpen(line string) (fence byte, fenceLen int, lang string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 || trimmed[0] != '`' && trimmed[0] != '~' {
		return 0, 0, "", false
	}
	fence = trimmed[0]
	for fenceLen < len(trimmed) && trimmed[fenceLen] == fence {
		fenceLen++
	}
	if fenceLen < 3 {
		return 0, 0, "", false
	}
	info := widthx.JSTrim(trimmed[fenceLen:])
	if fence == '`' && strings.ContainsRune(info, '`') {
		return 0, 0, "", false
	}
	// Tokenizer.fences unescapes backslash-escaped punctuation in the info string.
	return fence, fenceLen, markedUnescapePunctuation(info), true
}

func isMarkdownFenceClose(line string, fence byte, minLen int) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < minLen || trimmed[0] != fence {
		return false
	}
	fenceLen := 0
	for fenceLen < len(trimmed) && trimmed[fenceLen] == fence {
		fenceLen++
	}
	return fenceLen >= minLen && strings.TrimSpace(trimmed[fenceLen:]) == ""
}

const maxATXHeadingDepth = len("######")

func parseATXHeading(line string) (depth int, text string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0, "", false
	}
	for depth < len(trimmed) && depth <= maxATXHeadingDepth && trimmed[depth] == '#' {
		depth++
	}
	if depth == 0 || depth > maxATXHeadingDepth {
		return 0, "", false
	}
	// marked's heading rule is `#{1,6}(?=\s|$)`: the hashes may end the line, which is an empty heading.
	if depth == len(trimmed) {
		return depth, "", true
	}
	if trimmed[depth] != ' ' {
		return 0, "", false
	}
	return depth, strings.TrimSpace(trimmed[depth+1:]), true
}

func isTableHeaderLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

func isTableSeparatorLine(line string) bool {
	if !isTableHeaderLine(line) {
		return false
	}
	for _, cell := range splitTableRow(line) {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			return false
		}
		trimmed = strings.TrimPrefix(trimmed, ":")
		trimmed = strings.TrimSuffix(trimmed, ":")
		if trimmed == "" || strings.Trim(trimmed, "-") != "" {
			return false
		}
	}
	return true
}

func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func longestWordWidth(text string) int {
	words := strings.Fields(text)
	if len(words) == 0 {
		return 1
	}
	longest := 1
	for _, word := range words {
		if w := widthx.VisibleWidth(word); w > longest {
			longest = w
		}
	}
	if longest > 30 {
		return 30
	}
	return longest
}

// padTableCell left-aligns a cell; pi-tui renders every column left-aligned.
func padTableCell(text string, width int) string {
	return text + strings.Repeat(" ", max(0, width-widthx.VisibleWidth(text)))
}

// renderMarkdownTable ports markdown.ts renderTable. Cells are lexed once, header first, in marked's tokenization order, so the document's inline lexer state advances as in Pi.
func (m *Markdown) renderMarkdownTable(table markdownTable, availableWidth int) []string {
	header := make([]string, len(table.header))
	for i, cell := range table.header {
		header[i] = m.inlineMarkdown(cell)
	}
	rows := make([][]string, len(table.rows))
	for r, row := range table.rows {
		rows[r] = make([]string, len(row))
		for i, cell := range row {
			rows[r][i] = m.inlineMarkdown(cell)
		}
	}
	numCols := len(header)
	if numCols == 0 {
		return nil
	}
	borderOverhead := 3*numCols + 1
	availableForCells := availableWidth - borderOverhead
	if availableForCells < numCols {
		return widthx.WrapTextWithAnsi(table.raw, availableWidth)
	}

	natural := make([]int, numCols)
	minWordWidths := make([]int, numCols)
	for i, text := range header {
		natural[i] = widthx.VisibleWidth(text)
		minWordWidths[i] = longestWordWidth(text)
	}
	for _, row := range rows {
		for i := range numCols {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			if w := widthx.VisibleWidth(cell); w > natural[i] {
				natural[i] = w
			}
			if w := longestWordWidth(cell); w > minWordWidths[i] {
				minWordWidths[i] = w
			}
		}
	}

	minWidths := append([]int(nil), minWordWidths...)
	minCellsWidth := 0
	for _, w := range minWidths {
		minCellsWidth += w
	}
	if minCellsWidth > availableForCells {
		for i := range minWidths {
			minWidths[i] = 1
		}
		remaining := availableForCells - numCols
		if remaining > 0 {
			totalWeight := 0
			for _, width := range minWordWidths {
				totalWeight += max(0, width-1)
			}
			allocated := 0
			for i, width := range minWordWidths {
				weight := max(0, width-1)
				growth := 0
				if totalWeight > 0 {
					growth = int(math.Floor(float64(weight) / float64(totalWeight) * float64(remaining)))
				}
				minWidths[i] += growth
				allocated += growth
			}
			leftover := remaining - allocated
			for i := 0; leftover > 0 && i < numCols; i++ {
				minWidths[i]++
				leftover--
			}
		}
		minCellsWidth = 0
		for _, width := range minWidths {
			minCellsWidth += width
		}
	}

	totalNatural := borderOverhead
	for _, w := range natural {
		totalNatural += w
	}
	columnWidths := make([]int, numCols)
	copy(columnWidths, minWidths)
	if totalNatural <= availableWidth {
		for i := range numCols {
			if natural[i] > columnWidths[i] {
				columnWidths[i] = natural[i]
			}
		}
	} else {
		extraWidth := max(0, availableForCells-minCellsWidth)
		totalGrowPotential := 0
		for i := range numCols {
			totalGrowPotential += max(0, natural[i]-minWidths[i])
		}
		for i := range numCols {
			grow := 0
			if totalGrowPotential > 0 {
				grow = int(math.Floor(float64(max(0, natural[i]-minWidths[i])) / float64(totalGrowPotential) * float64(extraWidth)))
			}
			columnWidths[i] = minWidths[i] + grow
		}
		allocated := 0
		for _, w := range columnWidths {
			allocated += w
		}
		remaining := availableForCells - allocated
		for remaining > 0 {
			grew := false
			for i := range numCols {
				if remaining == 0 {
					break
				}
				if columnWidths[i] < natural[i] {
					columnWidths[i]++
					remaining--
					grew = true
				}
			}
			if !grew {
				break
			}
		}
	}

	topCells := make([]string, numCols)
	for i, w := range columnWidths {
		topCells[i] = strings.Repeat("─", w)
	}
	separator := "├─" + strings.Join(topCells, "─┼─") + "─┤"
	lines := []string{"┌─" + strings.Join(topCells, "─┬─") + "─┐"}

	headerWrapped := make([][]string, numCols)
	maxHeaderLines := 1
	for i, text := range header {
		headerWrapped[i] = m.wrapCellText(text, columnWidths[i])
		if len(headerWrapped[i]) > maxHeaderLines {
			maxHeaderLines = len(headerWrapped[i])
		}
	}
	theme := m.markdownTheme()
	for lineIdx := range maxHeaderLines {
		parts := make([]string, numCols)
		for col := range numCols {
			text := ""
			if lineIdx < len(headerWrapped[col]) {
				text = headerWrapped[col][lineIdx]
			}
			parts[col] = theme.Bold(padTableCell(text, columnWidths[col]))
		}
		lines = append(lines, "│ "+strings.Join(parts, " │ ")+" │")
	}
	lines = append(lines, separator)

	for rowIdx, row := range rows {
		wrapped := make([][]string, numCols)
		maxRowLines := 1
		for col := range numCols {
			text := ""
			if col < len(row) {
				text = row[col]
			}
			wrapped[col] = m.wrapCellText(text, columnWidths[col])
			if len(wrapped[col]) > maxRowLines {
				maxRowLines = len(wrapped[col])
			}
		}
		for lineIdx := range maxRowLines {
			parts := make([]string, numCols)
			for col := range numCols {
				text := ""
				if lineIdx < len(wrapped[col]) {
					text = wrapped[col][lineIdx]
				}
				parts[col] = padTableCell(text, columnWidths[col])
			}
			lines = append(lines, "│ "+strings.Join(parts, " │ ")+" │")
		}
		if rowIdx < len(rows)-1 {
			lines = append(lines, separator)
		}
	}

	bottomCells := make([]string, numCols)
	for i, w := range columnWidths {
		bottomCells[i] = strings.Repeat("─", w)
	}
	lines = append(lines, "└─"+strings.Join(bottomCells, "─┴─")+"─┘")
	return lines
}

func (m *Markdown) wrapCellText(text string, width int) []string {
	lines := widthx.WrapTextWithAnsi(text, max(1, width))
	prefix := ""
	if m.styleContext != nil {
		prefix = m.styleContext.stylePrefix
	}
	for i := range lines {
		if i < len(lines)-1 {
			lines[i] += "\x1b[22;23;24;25;27;28;29;39m"
		}
		lines[i] += prefix
	}
	return lines
}

func parseMarkdownListItem(line string) (indent, marker, body string, ok bool) {
	match := listItemPattern.FindStringSubmatch(line)
	if match == nil {
		return "", "", "", false
	}
	return match[1], match[2], match[3], true
}

// listTaskPattern mirrors marked's GFM listIsTask/listReplaceTask rules: a
// list item whose text starts with "[ ]", "[x]", or "[X]" plus a space is a
// task item, and the checkbox with its trailing spaces leaves the item text.
var listTaskPattern = lazyregexp.New(`^\[([ xX])\] +`)

// splitListTaskMarker returns the normalized task marker ("[x] " or "[ ] ")
// and the remaining item text, as markdown.ts renderList builds taskMarker
// from item.task and item.checked.
func splitListTaskMarker(body string) (taskMarker, rest string) {
	match := listTaskPattern.FindStringSubmatch(body)
	if match == nil {
		return "", body
	}
	if match[1] == " " {
		return "[ ] ", body[len(match[0]):]
	}
	return "[x] ", body[len(match[0]):]
}

func isBlockquoteLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	return strings.HasPrefix(trimmed, ">")
}

func stripBlockquotePrefix(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, ">") {
		return line
	}
	after := trimmed[1:]
	if strings.HasPrefix(after, " ") {
		return after[1:]
	}
	return after
}

func isLazyBlockquoteContinuation(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if isBlockquoteLine(line) {
		return false
	}
	if strings.HasPrefix(trimmed, "```") || isTableHeaderLine(line) || isTableSeparatorLine(line) {
		return false
	}
	if trimmed == "---" || trimmed == "***" || trimmed == "___" {
		return false
	}
	if strings.HasPrefix(trimmed, "#") {
		return false
	}
	if _, _, _, ok := parseMarkdownListItem(line); ok {
		return false
	}
	return true
}

// lineDisplayWidth returns the visible column count of a line with ANSI
// escape sequences stripped.
func lineDisplayWidth(s string) int {
	// Use go-runewidth for proper terminal column width. Plain rune counting
	// undercounts emoji and CJK wide characters (each 2 terminal columns but
	// 1 rune), causing paintBgWith to pad one space too many per wide char,
	// pushing the line to width+1 columns and triggering a terminal soft-wrap.
	// The overflow space on the next row has no background color → visible
	// stripe of terminal background between every bg-painted tool-output line.
	return widthx.VisibleWidth(s)
}

// headingInline uses a heading-specific context so inline token resets restore heading styles without leaking them into padding.
func (m *Markdown) headingInline(text string, depth int) string {
	theme := m.markdownTheme()
	style := func(text string) string { return theme.Heading(theme.Bold(text)) }
	if depth == 1 {
		style = func(text string) string { return theme.Heading(theme.Bold(theme.Underline(text))) }
	}
	context := inlineStyleContext{applyText: style, stylePrefix: markdownStylePrefix(style)}
	result := m.renderInlineMarkdown(text, context)
	if depth >= 3 {
		result = style(strings.Repeat("#", depth)+" ") + result
	}
	return result
}

type inlineStyleContext struct {
	applyText   func(string) string
	stylePrefix string
}

// ansiSpan mirrors nested theme decorations: an inner closing code restores the outer span until its own close.
func ansiSpan(open, close, text string) string {
	text = strings.ReplaceAll(text, close, close+open)
	var out strings.Builder
	out.WriteString(open)
	for {
		newline := strings.IndexByte(text, '\n')
		if newline < 0 {
			break
		}
		end := newline
		if end > 0 && text[end-1] == '\r' {
			end--
		}
		out.WriteString(text[:end])
		out.WriteString(close)
		out.WriteString(text[end : newline+1])
		out.WriteString(open)
		text = text[newline+1:]
	}
	out.WriteString(text)
	out.WriteString(close)
	return out.String()
}

// renderInlineMarkdown lexes one inline source with marked's rules and the document's lexer state, then renders the tokens.
func (m *Markdown) renderInlineMarkdown(s string, style inlineStyleContext) string {
	return m.renderInlineTokens(lexMarkedInline(s, m.lexerState()), style)
}

// lexerState is the document's marked lexer state, created for a component used outside Render.
func (m *Markdown) lexerState() *markedInlineState {
	if m.inlineState == nil {
		m.inlineState = &markedInlineState{}
	}
	return m.inlineState
}

func (m *Markdown) styleMarkdownLink(display, rawText, url string) string {
	theme := m.markdownTheme()
	styled := theme.Link(theme.Underline(display))
	if GetCapabilities().Hyperlinks {
		return Hyperlink(styled, url)
	}
	hrefForComparison := url
	if after, ok := strings.CutPrefix(hrefForComparison, "mailto:"); ok {
		hrefForComparison = after
	}
	if rawText == url || rawText == hrefForComparison {
		return styled
	}
	return styled + theme.LinkUrl(" ("+url+")")
}

// renderCodeBlock emits complete code rows; the final content-width wrapping pass handles prefixes and continuation rows.
func (m *Markdown) renderCodeBlock(lang string, lines []string) []string {
	theme := m.markdownTheme()
	indent := "  "
	if theme.CodeBlockIndent != nil {
		indent = *theme.CodeBlockIndent
	}
	out := []string{theme.CodeBlockBorder("```" + lang)}
	text := strings.Join(lines, "\n")
	if theme.HighlightCode != nil {
		for _, line := range theme.HighlightCode(text, lang) {
			out = append(out, indent+line)
		}
	} else {
		for line := range strings.SplitSeq(text, "\n") {
			out = append(out, indent+theme.CodeBlock(line))
		}
	}
	return append(out, theme.CodeBlockBorder("```"))
}
