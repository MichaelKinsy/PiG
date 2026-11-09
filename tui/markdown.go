package tui

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"weak"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Markdown renders themed text with terminal-cell wrapping, padding and cached display transforms.
// Ports packages/tui/src/components/markdown.ts.
type Markdown struct {
	invalidatable
	Content               string
	paddingX, paddingY    int
	theme                 *MarkdownTheme
	defaultTextStyle      *DefaultTextStyle
	options               MarkdownOptions
	defaultColorSet       bool
	defaultStylePrefix    string
	hasDefaultStylePrefix bool
	// inlineState is marked's lexer state for the document being rendered.
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
	// cachedBuffered is whether cachedLines is rowBuf, the array a render for a borrowing container reuses.
	cachedBuffered bool
	rowBuf         []string
	// inlinesOn is set while a render of a document that is not a quote or list item fills the inline renderings of memos;
	// highlightsOn is the same for its highlighted code, which the document keeps for a quote or list item.
	inlinesOn, highlightsOn bool
	// markdownMemos is what the render that is running keeps for the next one of a longer or equal text. It is set only
	// while a render runs; between renders memosRef holds it weakly, as markdown.ts holds its rendered blocks in a WeakRef:
	// a full garbage collection releases it, and the next render rebuilds it.
	*markdownMemos
	memosRef weak.Pointer[markdownMemos]
	// finished reports that the text no longer streams: a render keeps none of the memos (FinishStreaming).
	finished bool
}

// markdownMemos is what a streamed render keeps for the next delta. The rows are the same with or without it.
type markdownMemos struct {
	// wraps holds the rows the last render wrapped, keyed by the overflowing
	// line at wrapsWidth. wrapTextWithAnsi is a function of the line and the
	// width, so a streamed render wraps only the lines that changed.
	wraps, nextWraps map[string][]string
	wrapsWidth       int
	// inlines holds the inline renderings of the last render's text in the default style, keyed by the text and marked's
	// lexer state on entry. A rendering is a function of those and of inlinesContext, the inputs the style functions read,
	// so it is kept only while every style function is one of tui's own (markdownPureStyles). Invalidate and a change of
	// inlinesContext clear it, SetText does not.
	inlines, nextInlines map[inlineKey]inlineRendering
	inlinesContext       inlineContext
	// highlights holds the highlighted lines of the last render's code blocks, keyed by language and code, as markdown.ts
	// keeps the highlighting of each top-level block it renders. The theme's highlighter runs for a block only when the
	// code is new, so a streamed answer keeps the highlighting of its earlier blocks, and a resize or a spacing change
	// keeps it for a block that renders again. A render keeps only what it used, which drops a streamed block's earlier
	// versions. A change of highlightsEpoch (a theme change or a loaded grammar) and Invalidate clear it, SetText does
	// not.
	highlights, nextHighlights map[highlightKey][]string
	highlightsEpoch            HighlightEpoch
	// resume is what the last render kept to resume the next one of a longer text from (markdown_resume.go).
	resume *resumeState
}

// FinishStreaming drops the memos a streamed render keeps for the next delta (the wraps, the inline renderings, the
// highlighted code and the resume point) and keeps none from later renders. The rows are the same with or without them.
// A text not drawn since its last change keeps them for that one render, which resumes from them and then drops them: the
// frame after message_end draws the last delta as the frames before it drew theirs, instead of the whole answer again.
func (m *Markdown) FinishStreaming() {
	m.finished = true
	if m.cachedLines != nil && m.cachedContent == m.Content && m.cachedRevision == m.revision.Load() {
		m.dropStreamingMemos()
	}
}

// finishedMarkdown is m for a text that never streams: a user, summary or skill message is whole when it is built.
func finishedMarkdown(m *Markdown) *Markdown {
	m.FinishStreaming()
	return m
}

func (m *Markdown) dropStreamingMemos() {
	m.markdownMemos = nil
	m.memosRef = weak.Pointer[markdownMemos]{}
}

// holdMemos makes the memos the render that starts fills: the last render's while no garbage collection has released
// them, empty ones after one has (markdown.ts renders a block again when its WeakRef was cleared).
func (m *Markdown) holdMemos() {
	if m.markdownMemos != nil {
		return
	}
	if m.markdownMemos = m.memosRef.Value(); m.markdownMemos == nil {
		m.markdownMemos = &markdownMemos{}
	}
}

// releaseMemos ends a render's hold: from here the memos stay reachable only through memosRef, so the next collection
// that finds nothing else holding them releases them.
func (m *Markdown) releaseMemos() {
	if m.markdownMemos != nil {
		m.memosRef = weak.Make(m.markdownMemos)
		m.markdownMemos = nil
	}
}

// memos is what the last render kept, or nil when none was kept or a collection released it. A caller outside a render
// uses it, as the render's own fields are set only while it runs.
func (m *Markdown) memos() *markdownMemos {
	if m.markdownMemos != nil {
		return m.markdownMemos
	}
	return m.memosRef.Value()
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
	if memos := m.memos(); memos != nil {
		clear(memos.inlines)
		clear(memos.highlights)
		memos.resume = nil
	}
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

// renderEntry is the inline rendering of the source of a block token in the style context ctx, or in the document's when ctx is nil. The document's lexer state moves on as marked's does when the inline queue is lexed in order.
func (m *Markdown) renderEntry(entry *markedInlineEntry, ctx *inlineStyleContext) string {
	s := entry.src
	state := m.inlineState
	style := func() inlineStyleContext {
		if ctx != nil {
			return *ctx
		}
		return m.defaultInlineStyleContext()
	}
	// Only text with a "[" can hold a reference, so other text renders the same whatever links the document defines.
	if !m.inlinesOn || len(state.links) > 0 && strings.Contains(s, "[") {
		return renderInline(m, s, style())
	}
	key := inlineKey{src: s, entry: state.lexerFlags()}
	if ctx != nil {
		key.quoted, key.quotePrefix = true, ctx.stylePrefix
	}
	rendering, ok := m.nextInlines[key]
	if !ok {
		rendering, ok = m.inlines[key]
	}
	if ok {
		state.setLexerFlags(rendering.exit)
		state.lexedBracket = state.lexedBracket || strings.Contains(s, "[")
	} else {
		rendering = inlineRendering{rendered: renderInline(m, s, style()), exit: state.lexerFlags()}
	}
	m.nextInlines[key] = rendering
	return rendering.rendered
}

// startInlines starts reusing the last render's inline renderings for a document rendered in its own style.
func (m *Markdown) startInlines() {
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

// startHighlights starts reusing the last render's highlighted code for a document rendered in its own style. The code of
// a quote or list item renders through a child of the document and is kept by the document (highlightCode).
func (m *Markdown) startHighlights() {
	if epoch := CurrentHighlightEpoch(); epoch != m.highlightsEpoch {
		clear(m.highlights)
		m.highlightsEpoch = epoch
	}
	if m.nextHighlights == nil {
		m.nextHighlights = map[highlightKey][]string{}
	}
	m.highlightsOn = true
}

// endHighlights keeps this render's highlighted code only, so the memo holds one render's code blocks.
func (m *Markdown) endHighlights() {
	if !m.highlightsOn {
		return
	}
	m.highlightsOn = false
	clear(m.highlights)
	m.highlights, m.nextHighlights = m.nextHighlights, m.highlights
}

// highlightKey names a highlighted code block: its language and code.
type highlightKey struct{ lang, code string }

// highlightCode is the theme's highlighted lines of a code block, run for code the last render did not highlight. The
// lines are shared with later renders, so a caller does not modify them.
func (m *Markdown) highlightCode(highlight func(code, lang string) []string, lang, code string) []string {
	memo := m
	if !memo.highlightsOn {
		return highlight(code, lang)
	}
	key := highlightKey{lang: lang, code: code}
	lines, ok := memo.nextHighlights[key]
	if !ok {
		lines, ok = memo.highlights[key]
	}
	if !ok {
		lines = highlight(code, lang)
	}
	memo.nextHighlights[key] = lines
	return lines
}

// renderInline is the inline rendering of text with style.
var renderInline = (*Markdown).renderInlineMarkdown

//perf:hot
func (m *Markdown) Render(width int) []string { return m.render(width, false) }

// renderBorrowedChild is Render for a container that borrows the rows: a rebuilt document is laid out into an array the
// component keeps for its next render, so a streamed message does not allocate its whole row list each frame. The rows
// stay valid until the component renders again; a later Render copies them first, so the array it returns is its own.
// Only a container that holds the component as a child calls it, under that container's lock.
//
//perf:hot
func (m *Markdown) renderBorrowedChild(width int) []string { return m.render(width, true) }

// ownCachedLines is the cached rows as an array of the caller's own when the last render left them in the reusable one. A cache
// hit only reads the component, so the copy is not kept: public renders of one component from several goroutines stay reads.
func (m *Markdown) ownCachedLines() []string {
	if m.cachedBuffered {
		return slices.Clone(m.cachedLines)
	}
	return m.cachedLines
}

func (m *Markdown) render(width int, borrow bool) []string {
	// Pi throws a RangeError for a negative width (" ".repeat(width)); a negative width renders as width 0 here.
	if width < 0 {
		width = 0
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
				if borrow {
					return m.cachedLines
				}
				return m.ownCachedLines()
			}
			return nil
		}
	}
	if m.cachedLines != nil && m.cachedContent == m.Content && m.cachedWidth == width &&
		m.cachedTransformState == transformState && (m.theme != nil || m.cachedTheme == ActiveTheme()) && m.cachedRevision == revision &&
		(m.AsyncTransform == nil || m.cachedTransformed == content) {
		if borrow {
			return m.cachedLines
		}
		return m.ownCachedLines()
	}
	if m.Transform != nil && m.AsyncTransform == nil {
		content = m.Transform(content, contentWidth)
	}
	// cachedTransformed is compared with the async transform's output above, so it keeps that output as it came.
	transformed := content
	// A JavaScript string never holds a high half followed by a low half: they are one character. Canonical joins two such halves a caller built with a plain Go concatenation. It copies the text, so only text that has such a pair goes through it; the rest, valid or not, is rendered as it is.
	if jsstring.HasAdjacentSurrogates(content) {
		content = jsstring.Canonical(content)
	}
	out := []string{}
	buffered := false
	if widthx.JSTrim(content) != "" {
		out, buffered = m.renderDocument(content, width, contentWidth, borrow)
	} else if memos := m.memos(); memos != nil {
		memos.resume = nil
	}
	m.cachedContent, m.cachedWidth, m.cachedLines, m.cachedBuffered = m.Content, width, out, buffered
	m.cachedTransformState, m.cachedTheme = transformState, ActiveTheme()
	m.cachedRevision, m.cachedTransformed = revision, transformed
	if m.finished {
		m.dropStreamingMemos()
	}
	return out
}

// renderDocument is the rows of a component whose text is content, a non-blank text.
func (m *Markdown) renderDocument(content string, width, contentWidth int, borrow bool) ([]string, bool) {
	state := markedInlineStates.Get().(*markedInlineState)
	state.startDocument()
	m.inlineState = state
	m.holdMemos()
	m.startInlines()
	m.startHighlights()
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
	m.endHighlights()
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
	m.releaseMemos()
	body := rows[:starts[run.finalBase]]
	var padding []string
	if m.defaultTextStyle != nil && m.defaultTextStyle.BgColor != nil {
		// The background function runs once per row, in order, then once per padding row, as markdown.ts renderBlock and render call it.
		background := m.defaultTextStyle.BgColor
		body, tail = applyMarkdownBackground(body, background), applyMarkdownBackground(tail, background)
		padding = make([]string, max(m.paddingY, 0))
		for i := range padding {
			padding[i] = background(strings.Repeat(" ", width))
		}
	} else {
		padding = slices.Repeat([]string{strings.Repeat(" ", width)}, max(m.paddingY, 0))
	}
	if len(padding)*2+len(body)+len(tail) == 0 {
		// markdown.ts render returns [""] when text that is not blank renders no lines (only link definitions).
		return []string{""}, false
	}
	if borrow {
		out := slices.Grow(m.rowBuf[:0], len(padding)*2+len(body)+len(tail))
		out = append(append(append(append(out, padding...), body...), tail...), padding...)
		m.rowBuf = out
		return out, true
	}
	return slices.Concat(padding, body, tail, padding), false
}

// applyMarkdownBackground is rows with the background function applied to every row that is not an image line.
func applyMarkdownBackground(rows []string, background func(string) string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row
		if !widthx.IsImageLine(row) {
			out[i] = background(row)
		}
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

// markdownTableFallsBack is whether a table of numCols columns is too narrow to render and is shown as its raw markdown.
func markdownTableFallsBack(numCols, availableWidth int) bool {
	return numCols > 0 && availableWidth-(3*numCols+1) < numCols
}

// renderMarkdownTable appends the rows of a table token (markdown.ts renderTable). The cells are lexed once, header first, in marked's tokenization order, so the document's inline lexer state advances as in Pi.
func (m *Markdown) renderMarkdownTable(out []string, table *markedBlock, availableWidth int, ctx *inlineStyleContext) []string {
	header := make([]string, len(table.header))
	for i, cell := range table.header {
		header[i] = m.renderEntry(cell, ctx)
	}
	rows := make([][]string, len(table.rows))
	for r, row := range table.rows {
		rows[r] = make([]string, len(row))
		for i, cell := range row {
			rows[r][i] = m.renderEntry(cell, ctx)
		}
	}
	numCols := len(header)
	if numCols == 0 {
		return out
	}
	borderOverhead := 3*numCols + 1
	availableForCells := availableWidth - borderOverhead
	if markdownTableFallsBack(numCols, availableWidth) {
		// Too narrow to render a stable table: fall back to raw markdown.
		return append(out, widthx.WrapTextWithAnsi(table.raw, availableWidth)...)
	}
	prefix := ""
	if ctx != nil {
		prefix = ctx.stylePrefix
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
	lines := out
	lines = append(lines, "┌─"+strings.Join(topCells, "─┬─")+"─┐")

	headerWrapped := make([][]string, numCols)
	maxHeaderLines := 1
	for i, text := range header {
		headerWrapped[i] = wrapCellText(text, columnWidths[i], prefix)
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
			wrapped[col] = wrapCellText(text, columnWidths[col], prefix)
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

// wrapCellText wraps a table cell to a column and, after each wrapped row but the last, resets the text styles and restores the style around the table (markdown.ts wrapCellText).
func wrapCellText(text string, width int, stylePrefix string) []string {
	lines := widthx.WrapTextWithAnsi(text, max(1, width))
	for i := range lines {
		if i < len(lines)-1 {
			lines[i] += "\x1b[22;23;24;25;27;28;29;39m"
		}
		lines[i] += stylePrefix
	}
	return lines
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
		for _, line := range m.highlightCode(theme.HighlightCode, lang, text) {
			out = append(out, indent+line)
		}
	} else {
		for line := range strings.SplitSeq(text, "\n") {
			out = append(out, indent+theme.CodeBlock(line))
		}
	}
	return append(out, theme.CodeBlockBorder("```"))
}
