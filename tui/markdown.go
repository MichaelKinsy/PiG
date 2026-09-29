package tui

import (
	"math"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var listItemPattern = lazyregexp.New(`^( *)([-+*]|\d{1,9}[.)])(?:[ \t]+(.*)|$)`)

var emailPattern = lazyregexp.New(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

// Markdown renders themed text with terminal-cell wrapping, padding and cached display transforms.
// Ports packages/tui/src/components/markdown.ts.
type Markdown struct {
	invalidatable
	Content               string
	paddingX, paddingY    int
	theme                 *MarkdownTheme
	defaultTextStyle      *DefaultTextStyle
	options               MarkdownOptions
	styleContext          *inlineStyleContext
	styleOwner            *Markdown
	suppressBlockSpacing  bool
	defaultColorSet       bool
	defaultStylePrefix    string
	hasDefaultStylePrefix bool
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
			s = m.defaultColor + s + SGRFgReset
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
		lines := wrapRenderedLines(m.renderContent(strings.ReplaceAll(content, "\t", "   "), contentWidth), contentWidth)
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
	}
	m.cachedContent, m.cachedWidth, m.cachedLines = m.Content, width, out
	m.cachedTransformState, m.cachedTheme = transformState, ActiveTheme()
	m.cachedRevision, m.cachedTransformed = revision, content
	return out
}

func (m *Markdown) renderContent(content string, width int) []string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))

	emitBlank := func() {
		if len(out) == 0 || out[len(out)-1] == "" {
			return
		}
		out = append(out, "")
	}
	emitSpace := emitBlank
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
		if fence, fenceLen, lang, ok := parseMarkdownFenceOpen(line); ok {
			emitBlank()
			inCode = true
			codeFence = fence
			codeFenceLen = fenceLen
			codeLang = lang
			codeLines = nil
			continue
		}

		if isTableHeaderLine(line) && isTableSeparatorLine(nextLine) {
			header := splitTableRow(line)
			align := parseTableAlignment(nextLine)
			var rows [][]string
			j := i + 2
			for j < len(lines) && isTableHeaderLine(lines[j]) {
				rows = append(rows, splitTableRow(lines[j]))
				j++
			}
			raw := strings.Join(lines[i:j], "\n")
			out = append(out, m.renderMarkdownTable(header, rows, align, raw, width)...)
			if j < len(lines) && strings.TrimSpace(lines[j]) != "" {
				emitBlank()
			}
			i = j - 1
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

		theme := m.markdownTheme()
		if depth, text, ok := parseATXHeading(line); ok {
			emit(m.headingInline(text, depth))
			if strings.TrimSpace(nextLine) != "" {
				emitBlank()
			}
			continue
		}

		if line == "---" || line == "***" || line == "___" {
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
			for j < len(lines) {
				current := lines[j]
				switch {
				case isBlockquoteLine(current):
					quoted = append(quoted, stripBlockquotePrefix(current))
				case j > i && isLazyBlockquoteContinuation(current):
					quoted = append(quoted, current)
				default:
					goto renderQuote
				}
				j++
			}
		renderQuote:
			out = append(out, m.renderQuote(strings.Join(quoted, "\n"), width)...)
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
			continue
		}

		// Empty line → blank
		if strings.TrimSpace(line) == "" {
			emitSpace()
			continue
		}

		// Keep soft line breaks inside the paragraph until styling and wrapping. A table starts a new block without requiring a source blank line.
		j := i + 1
		for j < len(lines) && isMarkdownParagraphContinuation(lines, j) {
			if isTableHeaderLine(lines[j]) && j+1 < len(lines) && isTableSeparatorLine(lines[j+1]) {
				break
			}
			j++
		}
		emit(m.inlineMarkdown(strings.Join(lines[i:j], "\n")))
		if j+1 < len(lines) && isTableHeaderLine(lines[j]) && isTableSeparatorLine(lines[j+1]) {
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
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
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
	lang = strings.TrimSpace(trimmed[fenceLen:])
	if fence == '`' && strings.ContainsRune(lang, '`') {
		return 0, 0, "", false
	}
	return fence, fenceLen, lang, true
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
	if depth == 0 || depth > maxATXHeadingDepth || depth >= len(trimmed) || trimmed[depth] != ' ' {
		return 0, "", false
	}
	return depth, strings.TrimSpace(trimmed[depth+1:]), true
}

type tableAlignment int

const (
	tableAlignLeft tableAlignment = iota
	tableAlignCenter
	tableAlignRight
)

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

func parseTableAlignment(line string) []tableAlignment {
	parts := splitTableRow(line)
	out := make([]tableAlignment, len(parts))
	for i, cell := range parts {
		trimmed := strings.TrimSpace(cell)
		switch {
		case strings.HasPrefix(trimmed, ":") && strings.HasSuffix(trimmed, ":"):
			out[i] = tableAlignCenter
		case strings.HasSuffix(trimmed, ":"):
			out[i] = tableAlignRight
		default:
			out[i] = tableAlignLeft
		}
	}
	return out
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

func alignTableCell(text string, width int, align tableAlignment) string {
	pad := max(0, width-widthx.VisibleWidth(text))
	switch align {
	case tableAlignRight:
		return strings.Repeat(" ", pad) + text
	case tableAlignCenter:
		left := pad / 2
		right := pad - left
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
	default:
		return text + strings.Repeat(" ", pad)
	}
}

func (m *Markdown) renderMarkdownTable(header []string, rows [][]string, align []tableAlignment, raw string, availableWidth int) []string {
	numCols := len(header)
	if numCols == 0 {
		return nil
	}
	borderOverhead := 3*numCols + 1
	availableForCells := availableWidth - borderOverhead
	if availableForCells < numCols {
		return widthx.WrapTextWithAnsi(raw, availableWidth)
	}

	natural := make([]int, numCols)
	minWordWidths := make([]int, numCols)
	for i, cell := range header {
		text := m.inlineMarkdown(cell)
		natural[i] = widthx.VisibleWidth(text)
		minWordWidths[i] = longestWordWidth(text)
	}
	for _, row := range rows {
		for i := range numCols {
			cell := ""
			if i < len(row) {
				cell = m.inlineMarkdown(row[i])
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
	for i, cell := range header {
		headerWrapped[i] = m.wrapCellText(m.inlineMarkdown(cell), columnWidths[i])
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
			parts[col] = theme.Bold(alignTableCell(text, columnWidths[col], tableAlignLeft))
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
				text = m.inlineMarkdown(row[col])
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
				parts[col] = alignTableCell(text, columnWidths[col], tableAlignLeft)
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

func (m *Markdown) renderInlineMarkdown(s string, style inlineStyleContext) string {
	theme := m.markdownTheme()
	var out, plain strings.Builder
	applyText := func(text string) string {
		parts := strings.Split(text, "\n")
		for i := range parts {
			parts[i] = style.applyText(parts[i])
		}
		return strings.Join(parts, "\n")
	}
	flushText := func() {
		if plain.Len() > 0 {
			out.WriteString(applyText(plain.String()))
			plain.Reset()
		}
	}
	emitToken := func(token string) {
		flushText()
		out.WriteString(token)
		out.WriteString(style.stylePrefix)
	}
	i := 0
	runes := []rune(s)
	var autoLinks autoLinkScanner
	for i < len(runes) {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '\n' {
			flushText()
			out.WriteByte('\n')
			i += 2
			continue
		}
		if runes[i] == ' ' {
			end := i + 1
			for end < len(runes) && runes[end] == ' ' {
				end++
			}
			if end-i >= 2 && end < len(runes) && runes[end] == '\n' {
				flushText()
				out.WriteByte('\n')
				i = end + 1
				continue
			}
		}
		if runes[i] == '\\' && i+1 < len(runes) {
			if runes[i+1] == '(' || runes[i+1] == '[' {
				if tok, ok := tokenizeInlineLatex(string(runes[i:])); ok {
					flushText()
					text := tok.raw
					if m.latexEnabled() {
						text = renderInlineLatex(tok)
					}
					out.WriteString(applyText(text))
					i += utf8.RuneCountInString(tok.raw)
					continue
				}
			}
			if strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", runes[i+1]) {
				flushText()
				text := string(runes[i+1])
				if m.options.PreserveBackslashEscapes {
					text = string(runes[i : i+2])
				}
				out.WriteString(style.applyText(text))
				i += 2
				continue
			}
		}
		// Markdown link: [text](url)
		if runes[i] == '[' {
			if text, url, next, ok := parseMarkdownLink(runes, i); ok {
				emitToken(m.styleMarkdownLink(m.renderInlineMarkdown(text, style), text, url))
				i = next
				continue
			}
		}
		// Code spans use an equal-length closing backtick run.
		if runes[i] == '`' {
			count := 1
			for i+count < len(runes) && runes[i+count] == '`' {
				count++
			}
			closing := -1
			for j := i + count; j < len(runes); {
				if runes[j] != '`' {
					j++
					continue
				}
				end := j + 1
				for end < len(runes) && runes[end] == '`' {
					end++
				}
				if end-j == count {
					closing = j
					break
				}
				j = end
			}
			if closing < 0 {
				plain.WriteString(string(runes[i : i+count]))
				i += count
				continue
			}
			code := strings.ReplaceAll(string(runes[i+count:closing]), "\n", " ")
			if strings.HasPrefix(code, " ") && strings.HasSuffix(code, " ") && strings.Trim(code, " ") != "" {
				code = code[1 : len(code)-1]
			}
			emitToken(theme.Code(code))
			i = closing + count
			continue
		}
		// Bold: **...**
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			j := i + 2
			for j+1 < len(runes) && (runes[j] != '*' || runes[j+1] != '*') {
				j++
			}
			if j+1 < len(runes) {
				emitToken(theme.Bold(m.renderInlineMarkdown(string(runes[i+2:j]), style)))
				i = j + 2
				continue
			}
		}
		// Italic: *...*
		if runes[i] == '*' {
			j := i + 1
			for j < len(runes) && runes[j] != '*' {
				j++
			}
			if j < len(runes) {
				emitToken(theme.Italic(m.renderInlineMarkdown(string(runes[i+1:j]), style)))
				i = j + 1
				continue
			}
		}
		// Strikethrough: ~~...~~
		if i+1 < len(runes) && runes[i] == '~' && runes[i+1] == '~' {
			j := i + 2
			for j+1 < len(runes) && (runes[j] != '~' || runes[j+1] != '~') {
				j++
			}
			if j+1 < len(runes) {
				emitToken(theme.Strikethrough(m.renderInlineMarkdown(string(runes[i+2:j]), style)))
				i = j + 2
				continue
			}
		}
		// Bare URLs and emails.
		if text, url, next, ok := autoLinks.parseAutoLink(runes, i); ok {
			emitToken(m.styleMarkdownLink(style.applyText(text), text, url))
			i = next
			continue
		}
		// Code spans take precedence over inline LaTeX.
		if runes[i] == '$' || (runes[i] == '\\' && i+1 < len(runes) && (runes[i+1] == '(' || runes[i+1] == '[')) {
			if tok, ok := tokenizeInlineLatex(string(runes[i:])); ok {
				flushText()
				text := tok.raw
				if m.latexEnabled() {
					text = renderInlineLatex(tok)
				}
				out.WriteString(applyText(text))
				i += utf8.RuneCountInString(tok.raw)
				continue
			}
		}
		plain.WriteRune(runes[i])
		i++
	}
	flushText()
	result := out.String()
	for style.stylePrefix != "" && strings.HasSuffix(result, style.stylePrefix) {
		result = strings.TrimSuffix(result, style.stylePrefix)
	}
	return result
}

func parseMarkdownLink(runes []rune, start int) (text, url string, next int, ok bool) {
	closeBracket := -1
	for i := start + 1; i < len(runes); i++ {
		if runes[i] == ']' {
			closeBracket = i
			break
		}
	}
	if closeBracket == -1 || closeBracket+1 >= len(runes) || runes[closeBracket+1] != '(' {
		return "", "", start, false
	}
	closeParen := -1
	for i := closeBracket + 2; i < len(runes); i++ {
		if runes[i] == ')' {
			closeParen = i
			break
		}
	}
	if closeParen == -1 {
		return "", "", start, false
	}
	text = string(runes[start+1 : closeBracket])
	url = string(runes[closeBracket+2 : closeParen])
	return text, url, closeParen + 1, true
}

func autoLinkPrefix(runes []rune, start int) string {
	return string(runes[start:min(start+len("https://"), len(runes))])
}

// autoLinkScanner owns lookahead for one inline source. Word boundaries and the possible email suffix are scanned once, including when inline tokens skip over part of a word. No state survives the render or retains source text.
type autoLinkScanner struct {
	end        int
	emailStart int
	emailAt    int
	emailEnd   int
}

func (s *autoLinkScanner) scanWord(runes []rune, start int) {
	s.end = start
	s.emailAt = -1
	for s.end < len(runes) && !unicode.IsSpace(runes[s.end]) {
		if runes[s.end] == '@' {
			s.emailAt = s.end
		}
		s.end++
	}
	if s.emailAt < 0 {
		return
	}
	s.emailStart = s.emailAt
	for s.emailStart > start && isEmailLocalRune(runes[s.emailStart-1]) {
		s.emailStart--
	}
	// Parentheses cannot belong to an email. At an email's start every trailing ')' is unmatched, irrespective of earlier text in this word.
	s.emailEnd = s.end
	for s.emailEnd > s.emailAt && strings.ContainsRune(".,;:!?)", runes[s.emailEnd-1]) {
		s.emailEnd--
	}
	if s.emailStart == s.emailAt || !emailPattern.MatchString(string(runes[s.emailStart:s.emailEnd])) {
		s.emailAt = -1
	}
}

func isEmailLocalRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._%+-", r)
}

func (s *autoLinkScanner) parseAutoLink(runes []rune, start int) (text, url string, next int, ok bool) {
	if start >= s.end {
		s.scanWord(runes, start)
	}
	remaining := autoLinkPrefix(runes, start)
	if strings.HasPrefix(remaining, "https://") || strings.HasPrefix(remaining, "http://") {
		candidate := trimTrailingLinkPunctuation(string(runes[start:s.end]))
		return candidate, candidate, start + runeLen(candidate), true
	}
	if start >= s.emailStart && start < s.emailAt {
		candidate := string(runes[start:s.emailEnd])
		return candidate, "mailto:" + candidate, s.emailEnd, true
	}
	return "", "", start, false
}

func trimTrailingLinkPunctuation(s string) string {
	unmatchedClosing := strings.Count(s, ")") - strings.Count(s, "(")
	for s != "" {
		switch s[len(s)-1] {
		case '.', ',', ';', ':', '!', '?':
			s = s[:len(s)-1]
		case ')':
			if unmatchedClosing > 0 {
				s = s[:len(s)-1]
				unmatchedClosing--
				continue
			}
			return s
		default:
			return s
		}
	}
	return s
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

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
