package tui

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

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
	// keepPartialFence is set for content that is not the last token of the text: markdown.ts trimPartialClosingFences only looks at the last token and its last list item or quoted token.
	keepPartialFence bool
	// lazyUnderlines are the indexes of a quote's lazy continuation lines that look like setext underlines.
	lazyUnderlines []int
	// lazyLines are the indexes of a quote's lazy continuation lines: marked joins each to the quoted paragraph, so one that looks like a table delimiter row starts no table.
	lazyLines []int
	// lazyIndented are the lazy lines that lost four leading spaces to the quote: they continue the quoted paragraph whatever they now look like.
	lazyIndented []int
	// lazyBreaks are the lazy lines that start a block of their own beside the quoted paragraph.
	lazyBreaks            []int
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
	// wraps holds the rows the last render wrapped, keyed by the overflowing
	// line at wrapsWidth. wrapTextWithAnsi is a function of the line and the
	// width, so a streamed render wraps only the lines that changed.
	wraps, nextWraps map[string][]string
	wrapsWidth       int
	// inlines holds the inline renderings of the last render's text in the default style, keyed by the text and marked's
	// lexer state on entry. A rendering is a function of those and of inlinesContext, the inputs the style functions read,
	// so it is kept only while every style function is one of tui's own (markdownPureStyles). Invalidate and a change of
	// inlinesContext clear it, SetText does not. inlinesOn is set while a render of a document that is not a quote or list
	// item fills it.
	inlines, nextInlines map[inlineKey]inlineRendering
	inlinesContext       inlineContext
	inlinesOn            bool
	// resume is what the last render kept to resume the next one of a longer text from (markdown_resume.go).
	resume *resumeState
	// finished reports that the text no longer streams: a render keeps none of the memos above, which only serve
	// the next render of a longer or equal text (FinishStreaming).
	finished bool
}

// FinishStreaming drops the memos a streamed render keeps for the next delta (the wraps, the inline renderings and
// the resume point) and keeps none from later renders. The rows are the same with or without them. A text not drawn
// since its last change keeps them for that one render, which resumes from them and then drops them: the frame after
// message_end draws the last delta as the frames before it drew theirs, instead of the whole answer again.
func (m *Markdown) FinishStreaming() {
	m.finished = true
	if m.cachedLines != nil && m.cachedContent == m.Content && m.cachedRevision == m.revision.Load() {
		m.dropStreamingMemos()
	}
}

func (m *Markdown) dropStreamingMemos() {
	m.wraps, m.nextWraps, m.inlines, m.nextInlines, m.resume = nil, nil, nil, nil, nil
}

// inlineContext is everything besides the text and the lexer state that an inline rendering in the default style reads.
type inlineContext struct {
	theme                            *Theme
	hyperlinks                       bool
	styles                           [7]uintptr
	color                            string
	colorSet, italic                 bool
	bold, styleItalic, strike, under bool
}

// markdownPureStyles holds the code of the style functions in GetMarkdownTheme. They capture nothing and read only their
// argument and the active theme, so their code identifies what they return.
var markdownPureStyles = func() map[uintptr]bool {
	theme := GetMarkdownTheme()
	pure := map[uintptr]bool{}
	for _, style := range []func(string) string{theme.Heading, theme.Link, theme.LinkUrl, theme.Code, theme.CodeBlock, theme.CodeBlockBorder, theme.Quote, theme.QuoteBorder, theme.Hr, theme.ListBullet, theme.Bold, theme.Italic, theme.Strikethrough, theme.Underline} {
		pure[reflect.ValueOf(style).Pointer()] = true
	}
	return pure
}()

// currentInlineContext is the context of an inline rendering now, and false when a style function is not one of tui's own.
func (m *Markdown) currentInlineContext() (inlineContext, bool) {
	context := inlineContext{theme: ActiveTheme(), hyperlinks: GetCapabilities().Hyperlinks, color: m.defaultColor, colorSet: m.defaultColorSet, italic: m.defaultItalic}
	if style := m.defaultTextStyle; style != nil {
		if style.Color != nil && !m.defaultColorSet {
			return context, false
		}
		context.bold, context.styleItalic, context.strike, context.under = style.Bold, style.Italic, style.Strikethrough, style.Underline
	}
	theme := m.markdownTheme()
	for i, style := range [...]func(string) string{theme.Bold, theme.Italic, theme.Strikethrough, theme.Underline, theme.Code, theme.Link, theme.LinkUrl} {
		code := reflect.ValueOf(style).Pointer()
		if !markdownPureStyles[code] {
			return context, false
		}
		context.styles[i] = code
	}
	return context, true
}

// inlineKey names an inline rendering: the text, the lexer flags it starts with, and, inside a quote, the style prefix of the quote (a quote's text style is the identity, so its prefix is the whole of its style context).
type inlineKey struct {
	src         string
	entry       markedLexerFlags
	quoted      bool
	quotePrefix string
}

type inlineRendering struct {
	rendered string
	exit     markedLexerFlags
}

func NewMarkdown(content string) *Markdown {
	return NewMarkdownWithOptions(content, 0, 0, nil, nil, nil)
}

// Invalidate reruns the display transform and parser on the next render, even when the source text is unchanged.
func (m *Markdown) Invalidate() {
	clear(m.inlines)
	m.resume = nil
	m.textChanged()
}

func (m *Markdown) textChanged() {
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
	state := m.inlineState
	// A list item or a quote renders through a child of the document and shares its lexer state and default style, so the document's memo serves the child's text too.
	memo := m
	if m.styleOwner != nil {
		memo = m.styleOwner
	}
	// Only text with a "[" can hold a reference, so other text renders the same whatever links the document defines.
	if !memo.inlinesOn || len(state.links) > 0 && strings.Contains(s, "[") {
		return renderInline(m, s, m.defaultInlineStyleContext())
	}
	key := inlineKey{src: s, entry: state.lexerFlags()}
	if m.styleContext != nil {
		key.quoted, key.quotePrefix = true, m.styleContext.stylePrefix
	}
	rendering, ok := memo.nextInlines[key]
	if !ok {
		rendering, ok = memo.inlines[key]
	}
	if ok {
		state.setLexerFlags(rendering.exit)
		state.lexedBracket = state.lexedBracket || strings.Contains(s, "[")
	} else {
		rendering = inlineRendering{rendered: renderInline(m, s, m.defaultInlineStyleContext()), exit: state.lexerFlags()}
	}
	memo.nextInlines[key] = rendering
	return rendering.rendered
}

// startInlines starts reusing the last render's inline renderings for a document rendered in its own style.
func (m *Markdown) startInlines() {
	if m.styleContext != nil || m.styleOwner != nil {
		return
	}
	context, pure := m.currentInlineContext()
	if !pure || context != m.inlinesContext {
		clear(m.inlines)
		m.inlinesContext = context
	}
	if !pure {
		return
	}
	if m.nextInlines == nil {
		m.nextInlines = map[inlineKey]inlineRendering{}
	}
	m.inlinesOn = true
}

// endInlines keeps this render's inline renderings only, so the cache holds one render's text.
func (m *Markdown) endInlines() {
	if !m.inlinesOn {
		return
	}
	m.inlinesOn = false
	clear(m.inlines)
	m.inlines, m.nextInlines = m.nextInlines, m.inlines
}

// renderInline is the inline rendering of text with style.
var renderInline = (*Markdown).renderInlineMarkdown

//perf:hot
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
		out = m.renderDocument(content, width, contentWidth)
	} else {
		m.resume = nil
	}
	m.cachedContent, m.cachedWidth, m.cachedLines = m.Content, width, out
	m.cachedTransformState, m.cachedTheme = transformState, ActiveTheme()
	m.cachedRevision, m.cachedTransformed = revision, content
	if m.finished {
		m.dropStreamingMemos()
	}
	return out
}

// renderDocument is the rows of a component whose text is content, a non-blank text.
func (m *Markdown) renderDocument(content string, width, contentWidth int) []string {
	state := markedInlineStates.Get().(*markedInlineState)
	state.startDocument()
	m.inlineState = state
	m.startInlines()
	key, resumable := m.resumeKey(width, contentWidth)
	previous := m.resume
	m.resume = nil
	var run documentRun
	if resumable && previous != nil && previous.key == key {
		if from := previous.pick(content); from != nil {
			state.setLexerFlags(from.flags)
			state.lexedBracket = from.lexedBracket
			run = m.resumeDocument(content, width, contentWidth, previous, from)
		}
	}
	if !run.ok {
		state.startDocument()
		run = m.renderDocumentFrom(content, width, contentWidth, resumable)
		if state.relex {
			state.restartWithLinks()
			run = m.renderDocumentFrom(content, width, contentWidth, false)
		}
	}
	m.endInlines()
	m.inlineState = nil
	state.release()
	rows, starts := run.rows, run.rowStart
	if run.points != nil {
		m.resume = &resumeState{key: key, raw: content, stable: run.stable, out: run.loopOut, rows: rows, rowStart: starts, points: run.points}
	}
	extra := run.final[run.finalBase:]
	var tail []string
	if len(extra) > 0 {
		tail, _ = m.layoutRows(nil, nil, extra, width, contentWidth)
	}
	m.keepWraps()
	body := rows[:starts[run.finalBase]]
	padding := make([]string, m.paddingY)
	background := func(line string) string { return line }
	if m.defaultTextStyle != nil && m.defaultTextStyle.BgColor != nil {
		background = m.defaultTextStyle.BgColor
	}
	for i := range padding {
		padding[i] = background(strings.Repeat(" ", width))
	}
	out := make([]string, 0, len(padding)*2+len(body)+len(tail))
	out = append(out, padding...)
	out = append(out, body...)
	out = append(out, tail...)
	out = append(out, padding...)
	// markdown.ts render returns [""] when text that is not blank renders no lines (only link definitions).
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// documentRun is one pass over a document: its lines before and after the end-of-text rules, the rows laid out from the
// lines before them, and, when the pass records resume points, those points and the offset of the text's last line.
type documentRun struct {
	loopOut, final []string
	finalBase      int
	rows           []string
	rowStart       []int
	points         []resumePoint
	stable         int
	ok             bool
}

// renderDocumentFrom renders content from its start.
func (m *Markdown) renderDocumentFrom(content string, width, contentWidth int, record bool) documentRun {
	source := normalizeSource(content)
	var rec *resumeRecorder
	if record {
		rec = &resumeRecorder{rawStarts: rawLineStarts(content)}
	}
	var run documentRun
	run.loopOut, run.final, run.finalBase = m.renderBlocks(source, contentWidth, nil, rec)
	run.ok = true
	if rec != nil && !m.inlineState.relex {
		run.points = rec.points
		run.stable = rec.rawStarts[len(rec.rawStarts)-1]
		if run.points == nil {
			run.points = []resumePoint{}
		}
	}
	run.rows, run.rowStart = m.layoutRows(nil, nil, run.loopOut, width, contentWidth)
	run.rowStart = append(run.rowStart, len(run.rows))
	return run
}

// resumeDocument renders content from the point, which previous recorded for the text its raw holds the start of.
func (m *Markdown) resumeDocument(content string, width, contentWidth int, previous *resumeState, from *resumePoint) documentRun {
	tail := content[from.rawOff:]
	rec := &resumeRecorder{rawStarts: rawLineStarts(tail), rawBase: from.rawOff}
	var run documentRun
	run.loopOut, run.final, run.finalBase = m.renderBlocks(normalizeSource(tail), contentWidth, from, rec)
	run.ok = true
	if m.inlineState.relex {
		// A definition after the point may resolve a reference before it, which was lexed without it.
		run.ok = false
		return run
	}
	// The points before the one resumed from are the same for the longer text, and previous is not used again.
	from.prefix = nil
	run.points = append(append(previous.points[:from.index], *from), rec.points...)
	run.stable = rec.rawBase + rec.rawStarts[len(rec.rawStarts)-1]
	c := from.outLen
	run.rows, run.rowStart = previous.rows[:previous.rowStart[c]], previous.rowStart[:c]
	run.rows, run.rowStart = m.layoutRows(run.rows, run.rowStart, run.loopOut[c:], width, contentWidth)
	run.rowStart = append(run.rowStart, len(run.rows))
	return run
}

func (m *Markdown) renderContent(content string, width int) []string {
	_, out, _ := m.renderBlocks(content, width, nil, nil)
	return out
}

// renderBlocks renders the blocks of content. A top-level render passes rec to record the points a later render of a longer text resumes from, and from to start at one: content is then the text from that point's line (base is its line number), and the state of the loop is the point's.
// It returns the rendered lines before the end-of-text spacing rules (loopOut), those lines after them (final) and how many leading lines of final are loopOut's.
func (m *Markdown) renderBlocks(content string, width int, from *resumePoint, rec *resumeRecorder) (loopOut, final []string, finalBase int) {
	lines := strings.Split(content, "\n")
	source := &markdownBlockSource{lines: lines, lazy: m.lazyLines, indented: m.lazyIndented, breaks: m.lazyBreaks}
	base := 0
	var out []string
	var barrier int

	// silentEnd is len(out) after the last block that rendered no lines: a link definition, or a quote holding only definitions. Pi still renders such a block as a token, so the blank line after it is not merged with a blank before it, and blanks before it do not trail the output.
	silentEnd := -1
	// emptyBlockRow is set after a block whose own row is empty (an empty heading), so the blank row that follows it is not taken for a duplicate.
	emptyBlockRow, sawEmptyHeading := false, false
	// emptyFloor is len(out) after the last empty heading: its row is the block's own and survives the trim of trailing spacing.
	emptyFloor := -1
	// listSpaceEnd is len(out) after the empty row a list block adds for the spaces that end it, which is the same space token the end of the text would add.
	listSpaceEnd := -1
	// latexNoSpaceEnd is len(out) after a LaTeX block that leaves no space token behind.
	latexNoSpaceEnd := -1
	listEnd := -1
	// htmlEnd is the line after an HTML block that ends on a non-blank line: markdown.ts case "html" adds no spacing row, so a fence that follows it gets none.
	htmlEnd := -1
	if from == nil {
		out = make([]string, 0, len(lines))
	} else {
		base = from.line
		out = from.prefix
		silentEnd, emptyBlockRow, sawEmptyHeading, emptyFloor, listSpaceEnd, latexNoSpaceEnd = from.silentEnd, from.emptyBlockRow, from.sawEmptyHeading, from.emptyFloor, from.listSpaceEnd, from.latexNoSpaceEnd
		if from.listEnd >= 0 {
			listEnd = from.listEnd - base
		}
	}
	if rec != nil {
		barrier = resumeBarrier(lines)
	}
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

	// A definition of a label that is already defined is dropped without a token (Lexer.ts: `this.tokens.links[r.tag] || (... t.push(r))`), so the token after the block ends is the one past the dropped definitions: the block adds no spacing row of its own when that is the end of the text or a space token.
	// The lookahead is only for the blocks that decide their spacing from the next line (a heading, a rule, a fence's close, block LaTeX, a quote).
	spacingNextLine := func(i int) string {
		for at := i + 1; at < len(lines); {
			tag, _, end, ok := source.def(at)
			if !ok {
				return lines[at]
			}
			if _, defined := m.lexerState().defined[tag]; !defined {
				return lines[at]
			}
			at = end
		}
		return ""
	}

	// onlyDroppedDefinitions reports that what follows the lines before index is nothing but definitions of labels that are already defined, and then the end of the text: no token follows.
	onlyDroppedDefinitions := func(index int) bool {
		for index < len(lines) {
			tag, _, end, ok := source.def(index)
			if _, defined := m.lexerState().defined[tag]; !ok || !defined {
				break
			}
			index = end
		}
		return markdownTextEndsAt(lines, index)
	}

	inCode := false
	codeLang := ""
	codeFence := byte(0)
	codeFenceLen := 0
	// codeIndent is the indent of a backtick fence, which the fence removes from its code lines.
	codeIndent, codeOpenIndent := 0, 0
	var codeLines []string
	theme := m.markdownTheme()

	for i := 0; i < len(lines); i++ {
		if rec != nil && !inCode && i > 0 && i <= barrier && i+1 < len(lines) && markdownResumable(lines[i-1], lines[i]) && len(m.lexerState().links) == 0 {
			rec.add(resumePoint{
				line: base + i, rawOff: rec.rawBase + rec.rawStarts[i], outLen: len(out),
				silentEnd: silentEnd, emptyBlockRow: emptyBlockRow, sawEmptyHeading: sawEmptyHeading, emptyFloor: emptyFloor,
				listSpaceEnd: listSpaceEnd, latexNoSpaceEnd: latexNoSpaceEnd, listEnd: absoluteListEnd(listEnd, base),
				flags: m.lexerState().lexerFlags(), lexedBracket: m.lexerState().lexedBracket,
			})
		}
		line := lines[i]
		if inCode {
			if isMarkdownFenceClose(line, codeFence, codeFenceLen) {
				out = append(out, m.renderCodeBlock(codeLang, codeLines)...)
				if !markedBlankLine(spacingNextLine(i)) {
					emitBlank()
				}
				inCode = false
				codeLang = ""
				codeFence = 0
				codeFenceLen = 0
				continue
			}
			codeLines = append(codeLines, indentCodeCompensation(line, codeIndent))
			continue
		}
		// marked's indented code rule precedes fences; a paragraph has already absorbed an indented line it cannot be interrupted by.
		if code, next, ok := source.indentedCode(i); ok {
			out = append(out, m.renderCodeBlock("", code)...)
			if next < len(lines) && !markedBlankLine(lines[next]) {
				emitBlank()
			}
			if blockEndsInSpaceLine(lines, next) {
				listSpaceEnd = len(out)
			}
			i = next - 1
			continue
		}
		if fence, fenceLen, lang, ok := parseMarkdownFenceOpen(line); ok {
			// A list token adds no trailing blank row of its own (markdown.ts case "list"), so a fence that directly follows a list gets none.
			if i != listEnd && i != htmlEnd {
				emitBlank()
			}
			inCode = true
			codeFence = fence
			codeFenceLen = fenceLen
			codeIndent, codeOpenIndent = 0, markdownIndent(line)
			if fence == '`' {
				codeIndent = codeOpenIndent
			}
			codeLang = lang
			codeLines = nil
			continue
		}

		// Block LaTeX ($$...$$, \[...\]): may span multiple lines. Checked
		// before paragraph handling and after code fences (code wins), mirroring
		// upstream markdown.ts block latex extension precedence.
		if blockLatexStart(line) {
			remaining := strings.Join(lines[i:], "\n")
			if tok, ok := tokenizeBlockLatex(remaining); ok {
				block := widthx.JSTrim(tok.raw)
				if m.latexEnabled() {
					block = renderBlockLatex(tok)
				}
				for bl := range strings.SplitSeq(block, "\n") {
					emit(m.applyDefaultStyle(bl))
				}
				// A block whose text is only whitespace renders as one empty row of its own: the trim of trailing spacing keeps it and the blank row after it is not a duplicate.
				if block == "" {
					emptyBlockRow = true
					emptyFloor = len(out)
				}
				consumed := strings.Count(tok.raw, "\n")
				if strings.HasSuffix(tok.raw, "\n") {
					consumed--
				}
				i += consumed
				if !markedBlankLine(spacingNextLine(i)) {
					emitBlank()
				}
				// The block's raw takes one newline (all of them while pending), so what follows it is a space token only when longer than one character.
				if len(remaining)-len(tok.raw) < 2 {
					latexNoSpaceEnd = len(out)
				}
				continue
			}
		}

		if depth, text, ok := parseATXHeading(line); ok {
			emit(m.headingInline(text, depth))
			emptyBlockRow = text == "" && depth < 3
			sawEmptyHeading = sawEmptyHeading || emptyBlockRow
			if emptyBlockRow {
				emptyFloor = len(out)
			}
			if !markedBlankLine(spacingNextLine(i)) {
				emitBlank()
			}
			continue
		}

		if isMarkedHr(line) {
			out = append(out, theme.Hr(strings.Repeat("─", min(width, 80))))
			if !markedBlankLine(spacingNextLine(i)) {
				emitBlank()
			}
			continue
		}

		// Blockquote blocks, including lazy continuation lines.
		if isBlockquoteLine(line) {
			j := i
			quoted := make([]string, 0, 4)
			var lazyUnderlines, lazyLines, lazyIndented, lazyBreaks []int
			// lazyCodeRun is set while the lazy lines since the quoted line are an indented run that marked's code tokenizer joined to the quoted paragraph.
			lazyCodeRun, lazyCodeNewline := false, false
			for j < len(lines) {
				current := lines[j]
				switch {
				case isBlockquoteLine(current):
					content := stripBlockquotePrefix(current)
					if lazyCodeRun {
						lazyIndented = append(lazyIndented, len(quoted))
						if markdownIndent(content) >= 4 {
							// An indented quoted line continues the code run.
							content = content[min(markdownIndent(content), 4):]
							lazyCodeNewline = false
						} else {
							// The run ends with a newline that the next quoted line joins to the paragraph as an empty row.
							quoted = append(quoted, "")
							lazyCodeRun = false
						}
					}
					quoted = append(quoted, content)
				case j > i && !source.quoteLazyInterrupted(j) && quoted[len(quoted)-1] != "" && (!isBlockquoteLine(lines[j-1]) || !markdownQuoteEndsInCode(quoted)):
					// marked lexes lazy lines apart from the quoted lines before them and joins a paragraph there to the quoted one, so a lazy setext underline continues the quoted paragraph instead of closing a heading.
					if setextUnderlinePattern.MatchString(current) {
						lazyUnderlines = append(lazyUnderlines, len(quoted))
					}
					// An indented-code line cannot interrupt the quoted paragraph; marked's blockquote tokenizer removes up to four of its leading spaces (codeRemoveIndent) when it joins it.
					indented := false
					if markdownIndent(current) >= 4 && (isBlockquoteLine(lines[j-1]) && markdownQuoteEndsInText(quoted) || lazyCodeRun) {
						current = current[min(markdownIndent(current), 4):]
						indented = true
					}
					switch {
					case indented:
						lazyCodeRun, lazyCodeNewline = true, true
					case lazyCodeRun:
						// The code tokenizer took the indented run and keeps its newline, which the next lazy line joins to the paragraph as an empty row; a setext underline joins without it.
						if !setextUnderlinePattern.MatchString(current) {
							if lazyCodeNewline {
								lazyIndented = append(lazyIndented, len(quoted))
								quoted = append(quoted, "")
							}
							lazyCodeRun = false
						} else {
							// The underline is indented to four spaces like any lazy underline, and the code run's tokenizer removes them.
							current = strings.TrimLeft(current, " ")
						}
					}
					if markdownQuoteListEndsInSpaces(quoted) {
						quoted = append(quoted, "")
					}
					// A setext underline after a lazy line would close a heading in the lazy run, so marked's blockquote tokenizer joins it to the quoted paragraph indented by four spaces.
					if !indented && !lazyCodeRun && !isBlockquoteLine(lines[j-1]) && setextUnderlinePattern.MatchString(current) {
						current = "    " + strings.TrimLeft(current, " ")
					}
					lazyLines = append(lazyLines, len(quoted))
					if isBlockquoteLine(lines[j-1]) {
						// A run of lazy lines is lexed on its own: a block that is not a paragraph or code (an HTML block start such as <span>, or a blank line) stays a token of its own next to the quoted paragraph.
						if _, _, ok := (&markdownBlockSource{lines: lines[j:]}).htmlBlock(0); ok || strings.Trim(current, " \t") == "" {
							lazyBreaks = append(lazyBreaks, len(quoted))
						}
					}
					if indented {
						lazyIndented = append(lazyIndented, len(quoted))
						// The underline is only the stripped text of an indented line; marked keeps the indent, so it never closes a heading.
						if setextUnderlinePattern.MatchString(current) {
							lazyUnderlines = append(lazyUnderlines, len(quoted))
						}
					}
					quoted = append(quoted, current)
				default:
					goto renderQuote
				}
				j++
			}
		renderQuote:
			quote := m.renderQuote(strings.Join(quoted, "\n"), lazyUnderlines, lazyLines, lazyIndented, lazyBreaks, width, !onlyDroppedDefinitions(j))
			if len(quote) == 0 {
				silentEnd = len(out)
			}
			out = append(out, quote...)
			// A lazy last line of spaces is a space token inside the quote, so the end of the text adds none of its own.
			if last := lines[len(lines)-1]; j == len(lines) && last != "" && strings.Trim(last, " \t") == "" {
				listSpaceEnd = len(out)
			}
			if !markedBlankLine(spacingNextLine(j - 1)) {
				if len(quote) == 0 && !m.suppressBlockSpacing {
					// A quote that renders no rows is still a token, so its spacing row is not merged with a blank before it.
					out = append(out, "")
				} else {
					emitBlank()
				}
			}
			i = j - 1
			continue
		}

		// List items contain blocks as well as inline text; nested lists use depth-based indentation.
		//portlint:allow utf16units indent is parseMarkdownListItem's leading ASCII spaces, so its byte length is its .length
		if indent, _, _, ok := parseMarkdownListItem(line); ok && len(indent) <= 3 {
			list, next := parseMarkdownList(lines, i, nil)
			keep := m.keepPartialFence
			m.keepPartialFence = keep || !markdownTextEndsAt(lines, next)
			out = append(out, m.renderList(list, 0, width)...)
			m.keepPartialFence = keep
			// A list token's raw is trimmed of its trailing whitespace, so spaces ending its last line stay in the text as a space token longer than one newline (markdown.ts case "space" renders an empty row).
			lastItemLine := next - 1
			for lastItemLine > i && strings.Trim(lines[lastItemLine], " \t") == "" {
				lastItemLine--
			}
			last := lines[lastItemLine]
			spaces := len(last) - len(strings.TrimRight(last, " \t"))
			if lastItemLine+1 < len(lines) {
				spaces++
			}
			if spaces >= 2 && strings.Trim(last, " \t") != "" {
				if out[len(out)-1] != "" {
					out = append(out, "")
				}
				emptyFloor = len(out)
				listSpaceEnd = len(out)
			}
			// Tokenizer.list trims the list raw with JavaScript trimEnd, which also removes U+00A0, U+3000 and the other Unicode spaces, and the lexer reads on from there: that text is a paragraph of its own.
			if resume, suffix, ok := markdownListResidue(lines, i, next); ok {
				lines[resume] = suffix
				i = resume - 1
				if suffix == "" {
					// The residue then starts with the newline that ended the last line, which is a space token of one character and renders no row.
					i = resume
				}
				listEnd = resume
				continue
			}
			i = next - 1
			listEnd = next
			continue
		}

		// marked tries an HTML block, then a GFM table, after lists.
		if raw, next, ok := source.htmlBlock(i); ok {
			emit(m.applyDefaultStyle(widthx.JSTrim(raw)))
			if blockEndsInSpaceLine(lines, next) {
				listSpaceEnd = len(out)
			}
			if !markedBlankLine(lines[next-1]) {
				htmlEnd = next
			}
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
			// A fallback's last wrapped row of raw text is a row of the table's own even when it is empty (a raw last line that only wraps to nothing, such as one wide space at width 1).
			rawEndsEmpty := markdownTableFallsBack(table, width) && len(out) > 0 && out[len(out)-1] == "" && widthx.JSTrim(table.raw[strings.LastIndexByte(table.raw, '\n')+1:]) == ""
			if table.foldsNewline || rawEndsEmpty {
				// The empty last row of a narrow table's raw is the table's own, so the trim of trailing spacing keeps it.
				emptyFloor = len(out)
			}
			if next < len(lines) && !markedBlankLine(lines[next]) {
				if markdownTableFallsBack(table, width) {
					// The fallback adds its own empty row when a block follows (renderTable), after the empty row the folded newline already wrapped to.
					// A list item renders its tokens with no next token (renderList passes undefined), so its table adds none.
					if !m.suppressBlockSpacing {
						out = append(out, "")
					}
				} else {
					emitBlank()
				}
			}
			if blockEndsInSpaceLine(lines, next) {
				listSpaceEnd = len(out)
			}
			i = next - 1
			continue
		}

		// Empty line → blank
		if markedBlankLine(line) {
			// marked's newline rule, /^(?:[ \t]*(?:\n|$))+/, makes a text that starts with blank lines begin with a space token, which markdown.ts case "space" renders as one empty row.
			if base+i == 0 && strings.Trim(line, " \t") == "" {
				out = append(out, "")
				for i+1 < len(lines) && strings.Trim(lines[i+1], " \t") == "" {
					i++
				}
				continue
			}
			emitSpace()
			continue
		}

		// marked tries a setext heading before a paragraph: the lines up to an underline, unless a blank line or one of its interrupting blocks comes first. A rule line does not interrupt it.
		if end, depth, ok := setextHeadingEnd(lines, i, m.lazyUnderlines); ok {
			emit(m.headingInline(widthx.JSTrim(strings.Join(lines[i:end], "\n")), depth))
			if end+1 < len(lines) && !markedBlankLine(lines[end+1]) {
				emitBlank()
			}
			i = end
			continue
		}

		// Lexer.ts:241-270 clips a top-level paragraph where a block extension's start() matches src.slice(1). pi-tui's latexBlock start(), /(?:^|\n) {0,3}(?:\$\$|\\\[)/ (packages/tui/src/components/markdown.ts:127-130), also matches at the start of that slice: "$$" or "\[" right after the first character, behind at most three spaces, leaves that character as the paragraph and the rest of the line is lexed next. A rest that is a latex block ends the paragraph; otherwise the paragraph that follows joins the clipped one (lastParagraphClipped). List items lex text rather than top-level paragraphs and are never clipped.
		var clipped []string
		latexRest := false
		for !m.suppressBlockSpacing && !latexRest {
			size, ok := latexClipsFirstCharacter(line)
			if !ok {
				break
			}
			//portlint:allow utf16units size is the byte length of the first character, which latexClipsFirstCharacter admits only in the BMP: src.slice(0, 1) and src.slice(1)
			clipped = append(clipped, line[:size])
			//portlint:allow utf16units size is the byte length of one BMP character, so this is src.slice(1)
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
		// A list item lexes text rather than a paragraph, so every line gets the chance to start an HTML block (Lexer.ts blockTokens with top false).
		for j < len(lines) && !source.paragraphInterrupted(j) && !markdownLatexBlockAt(lines, j) && (!m.suppressBlockSpacing || slices.Contains(m.lazyIndented, j) || !source.startsHTMLBlock(j)) {
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
		// An indented code block cannot interrupt a paragraph (Lexer.ts blockTokens): the code token that follows one the GFM table rule interrupted joins its text, with its indent removed and the newline its raw ends with.
		if j < len(lines) {
			if code, next, ok := source.indentedCode(j); ok {
				text += "\n" + strings.Join(code, "\n")
				j = next
			}
		}
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
		// marked's fence rule ends an unterminated block with the newline before the end of the text, so the empty last line that newline splits off is not a code line.
		if len(codeLines) >= 2 && codeLines[len(codeLines)-1] == "" {
			codeLines = codeLines[:len(codeLines)-1]
		}
		if len(codeLines) > 0 {
			last := codeLines[len(codeLines)-1]
			if !m.keepPartialFence && codeOpenIndent == 0 && len(last) > 0 && len(last) < codeFenceLen && last == strings.Repeat(string(codeFence), len(last)) {
				codeLines = codeLines[:len(codeLines)-1]
			}
		}
		out = append(out, m.renderCodeBlock(codeLang, codeLines)...)
	}
	loopOut = out
	for len(out) > max(silentEnd, emptyFloor, 0) && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	finalBase = len(out)
	// A text that holds only empty headings still shows the heading's empty row.
	if len(out) == 0 && sawEmptyHeading {
		out = append(out, "")
	}
	// marked ends the token stream with a space token when the text ends in a blank line, and Pi renders it as one empty row (markdown.ts case "space").
	// The text after the last block is one space token when it is longer than the single newline marked folds into the block's raw: two or more trailing blank lines, or one that holds spaces.
	trailing := 0
	for trailing < len(lines) && strings.Trim(lines[len(lines)-1-trailing], " \t") == "" {
		trailing++
	}
	// A paragraph at the end of the text takes a last line of spaces into its own raw, so that line is no space token.
	absorbed := len(out) > 0 && strings.HasSuffix(strings.TrimRight(out[len(out)-1], " \t"), "\n")
	if (len(out) > 0 || silentEnd >= 0) && !inCode && !absorbed && latexNoSpaceEnd != len(out) && listSpaceEnd != len(out) && base+len(lines) >= 2 && (trailing >= 2 || trailing == 1 && lines[len(lines)-1] != "") {
		out = append(out, "")
	}
	return loopOut, out, finalBase
}

// markdownQuoteEndsInText reports that the last token of a quote's quoted lines is a paragraph an indented lazy line can join. A last quoted line of spaces or tabs is emptied by marked's spaceLine rule, so the paragraph is the one before it, if any.
func markdownQuoteEndsInText(quoted []string) bool {
	last := quoted[len(quoted)-1]
	if last != "" && markedBlankLine(last) {
		return len(quoted) >= 2 && quoted[len(quoted)-2] != "" && markdownIsTextLine(quoted[len(quoted)-2], beforeLast(quoted[:len(quoted)-1]))
	}
	return markdownIsTextLine(last, beforeLast(quoted))
}

// blockEndsInSpaceLine reports that the single last line of the text, which holds only spaces or tabs, is no space token of its own after a block ending at next: marked's indented code (`(?:\n[ \t]*(?:\n|$))*`), HTML block (`|$`) and table rules read it into their raw, and a lone space left after a block is one character, which Lexer.lex folds into the block's raw as a newline.
func blockEndsInSpaceLine(lines []string, next int) bool {
	last := len(lines) - 1
	if last < 1 || markedBlankLine(lines[last-1]) || lines[last] == "" || !markedBlankLine(lines[last]) {
		return false
	}
	return next == len(lines) || next == last && lines[last] == " "
}

// latexClipsFirstCharacter reports whether pi-tui's latexBlock start() matches a top-level paragraph line's src.slice(1), and the byte size of the first character it leaves as the paragraph.
func latexClipsFirstCharacter(line string) (int, bool) {
	first, size := utf8.DecodeRuneInString(line)
	// slice(1) splits an astral character, and the lone surrogate it leaves cannot start the match.
	//portlint:allow utf16units size is the first character's byte length, and the match needs it in the BMP, so line[size:] is src.slice(1)
	return size, first <= 0xFFFF && blockLatexStart(line[size:])
}

// markdownParagraphFollowedByBlock reports whether the line that ended a paragraph starts a token other than space or list, after which upstream adds a blank line.
func markdownParagraphFollowedByBlock(line string) bool {
	if strings.TrimLeft(line, " \t") == "" {
		return false
	}
	//portlint:allow utf16units indent is parseMarkdownListItem's leading ASCII spaces, so its byte length is its .length
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
		if widthx.JSTrim(lines[index]) == "" || interrupts(index) {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// wrapTextWithAnsi is the wrap of an overflowing rendered line.
var wrapTextWithAnsi = widthx.WrapTextWithAnsi

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

// isMarkdownFenceClose is the fences rule's closing alternative, / {0,3}\1[~`]* *(?=\n|$)/: the opener's own run, then any run of '~' and '`', then spaces only.
func isMarkdownFenceClose(line string, fence byte, minLen int) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, strings.Repeat(string(fence), minLen)) {
		return false
	}
	rest := strings.TrimLeft(trimmed[minLen:], "~`")
	return strings.Trim(rest, " ") == ""
}

// indentCodeCompensation is marked's helper of that name: a backtick fence indented by n spaces removes n units from every code line whose own leading whitespace is at least that long.
func indentCodeCompensation(line string, n int) string {
	if n == 0 {
		return line
	}
	leading := 0
	for _, r := range line {
		if !widthx.IsJSSpace(r) {
			break
		}
		leading++
	}
	if leading >= n {
		return jsstring.Slice(line, n)
	}
	return line
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
	// The rule's `(.*)` cannot cross U+2028 or U+2029 and must be followed by a newline or the end, so a line holding either is not a heading.
	if strings.ContainsAny(line, "\u2028\u2029") {
		return 0, "", false
	}
	// marked's heading rule is `#{1,6}(?=\s|$)`: the hashes may end the line, which is an empty heading.
	if depth == len(trimmed) {
		return depth, "", true
	}
	// (?=\s|$): the \s is JavaScript whitespace, a tab or a no-break space as well as a space.
	if r, _ := utf8.DecodeRuneInString(trimmed[depth:]); !widthx.IsJSSpace(r) {
		return 0, "", false
	}
	text = widthx.JSTrim(trimmed[depth:])
	// marked's heading tokenizer drops a closing run of '#' that is the whole text or follows a space (Tokenizer.heading).
	if strings.HasSuffix(text, "#") {
		if rest := strings.TrimRight(text, "#"); rest == "" || strings.HasSuffix(rest, " ") {
			text = widthx.JSTrim(rest)
		}
	}
	return depth, text, true
}

func longestWordWidth(text string) int {
	words := strings.FieldsFunc(text, widthx.IsJSSpace)
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

// markdownTableFallsBack is whether the table is too narrow to render and is shown as its raw markdown.
func markdownTableFallsBack(table markdownTable, availableWidth int) bool {
	numCols := len(table.header)
	return numCols > 0 && availableWidth-(3*numCols+1) < numCols
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
	if markdownTableFallsBack(table, availableWidth) {
		raw := table.raw
		if table.foldsNewline {
			raw += "\n"
		}
		return widthx.WrapTextWithAnsi(raw, availableWidth)
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

// parseMarkdownListItem matches line as `^( *)([-+*]|\d{1,9}[.)])(?:[ \t]+(.*)|$)` does, with \d ASCII digits and "." any character but "\n": the indent, the marker, and the text after the spaces and tabs that follow the marker. Every line of the blocks a streamed frame renders again is asked, so it runs no regexp and builds no submatch slice.
func parseMarkdownListItem(line string) (indent, marker, body string, ok bool) {
	start := 0
	for start < len(line) && line[start] == ' ' {
		start++
	}
	end := start
	if end < len(line) && (line[end] == '-' || line[end] == '+' || line[end] == '*') {
		end++
	} else {
		for end < len(line) && end-start <= 9 && line[end] >= '0' && line[end] <= '9' {
			end++
		}
		if end == start || end-start > 9 || end == len(line) || line[end] != '.' && line[end] != ')' {
			return "", "", "", false
		}
		end++
	}
	text := end
	for text < len(line) && (line[text] == ' ' || line[text] == '\t') {
		text++
	}
	if text == end && end < len(line) {
		return "", "", "", false
	}
	body = line[text:]
	if newline := strings.IndexByte(body, '\n'); newline >= 0 {
		body = body[:newline]
	}
	return line[:start], line[start:end], body, true
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
	// listIsTask is /^\[[ xX]\] +\S/: the spaces must be followed by a character that is not whitespace, so "[ ] " alone or before a newline is text, not a task.
	if next, size := utf8.DecodeRuneInString(body[len(match[0]):]); size == 0 || widthx.IsJSSpace(next) {
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

// markdownQuoteListEndsInSpaces reports that the quoted lines so far end in a list item line with trailing spaces: marked trims the list's raw, so the spaces become a space token and a following lazy line is a paragraph of its own, not a continuation of the list.
func markdownQuoteListEndsInSpaces(quoted []string) bool {
	if len(quoted) == 0 {
		return false
	}
	last := quoted[len(quoted)-1]
	if _, _, _, item := parseMarkdownListItem(last); !item {
		return false
	}
	return len(last) != len(strings.TrimRight(last, " \t"))
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
	// identity says applyText returns its argument unchanged, which only the default context without a text style guarantees.
	identity bool
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
