package tui

import (
	"reflect"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// A streamed message grows at its end, so a render of the longer text resumes from the last top-level block start that
// the new text cannot have changed, instead of lexing and laying out every block again.
//
// A resume point is the start of a block that follows a blank line, where the loop of renderBlocks is between blocks. Every
// decision about the blocks before it reads the lines up to the first non-blank line after the block (a paragraph, quote,
// table or setext heading ends at a blank line; a list or indented code looks at the next non-blank line to continue; a
// fenced code block, HTML block or indented block that reaches the end of the text has no block after it). Three kinds of
// text read further, or on the whole remainder, and are never resumed past: a line that opens a LaTeX block (its pending form
// runs to the end of the text, and its closing delimiter may be anywhere later) or whose rest after its first character opens
// one (a paragraph is clipped there once a closing delimiter arrives), a line that may start a link definition (its
// title and label can span lines up to a closing quote or bracket anywhere later), and a link definition anywhere in the text
// before the point (references after it resolve against it). The render resumes from the second-last point, so the block
// before the one it rebuilds also has the whole of its following block as slack.

// resumePoint is the state of the block loop of renderBlocks at the start of a block, and the byte offset of that block in
// the text that was rendered.
// index is the position of the point in the points of its state, set when it is picked.
type resumePoint struct {
	index, line, rawOff, outLen                                   int
	silentEnd, emptyFloor, listSpaceEnd, latexNoSpaceEnd, listEnd int
	emptyBlockRow, sawEmptyHeading, lexedBracket                  bool
	flags                                                         markedLexerFlags
	prefix                                                        []string
}

func absoluteListEnd(listEnd, base int) int {
	if listEnd < 0 {
		return -1
	}
	return listEnd + base
}

// resumeRecorder collects the resume points of one render. rawStarts holds the byte offset, in the text before its line
// endings and tabs were normalized, of each line of the text rendered; rawBase is the offset of that text in the whole.
type resumeRecorder struct {
	points    []resumePoint
	rawStarts []int
	rawBase   int
}

func (r *resumeRecorder) add(p resumePoint) { r.points = append(r.points, p) }

// markdownResumable reports whether a block may start at line, which follows prev: a blank line before a non-blank one.
func markdownResumable(prev, line string) bool {
	return markedBlankLine(prev) && !markedBlankLine(line)
}

// resumeBarrier is the index of the first line from which no resume point is recorded: one that may start a block LaTeX, a
// paragraph line whose rest after its first character may start one (a closing delimiter anywhere later clips the paragraph
// there), or a line that may start a link definition. It is len(lines) when there is none, so every line may start a point.
func resumeBarrier(lines []string) int {
	for i, line := range lines {
		if _, clips := latexClipsFirstCharacter(line); blockLatexStart(line) || clips || markedDefCandidate(line) {
			return i
		}
	}
	return len(lines)
}

// resumeKey is everything besides the text that the rows of a render are a function of.
type resumeKey struct {
	width, contentWidth, paddingX, paddingY int
	context                                 inlineContext
	theme                                   *MarkdownTheme
	codeBlockIndent                         string
	latex, preserveOrdered, preserveEscapes bool
}

// markdownPureHighlight is the code of the highlighter of GetMarkdownTheme.
var markdownPureHighlight = reflect.ValueOf(highlightMarkdownCode).Pointer()

// resumeKey is the key of a render now, and false when the render cannot be resumed from or by another: a quote or list item
// (it renders inside its document), or a style function that is not one of tui's own, whose result may depend on what the
// function captured.
func (m *Markdown) resumeKey(width, contentWidth int) (resumeKey, bool) {
	if m.styleContext != nil || m.styleOwner != nil {
		return resumeKey{}, false
	}
	context, pure := m.currentInlineContext()
	if !pure || m.defaultTextStyle != nil && m.defaultTextStyle.BgColor != nil {
		return resumeKey{}, false
	}
	theme := m.markdownTheme()
	for _, style := range [...]func(string) string{theme.Heading, theme.Link, theme.LinkUrl, theme.Code, theme.CodeBlock, theme.CodeBlockBorder, theme.Quote, theme.QuoteBorder, theme.Hr, theme.ListBullet, theme.Bold, theme.Italic, theme.Strikethrough, theme.Underline} {
		if !markdownPureStyles[reflect.ValueOf(style).Pointer()] {
			return resumeKey{}, false
		}
	}
	if theme.HighlightCode == nil || reflect.ValueOf(theme.HighlightCode).Pointer() != markdownPureHighlight {
		return resumeKey{}, false
	}
	key := resumeKey{width: width, contentWidth: contentWidth, paddingX: m.paddingX, paddingY: m.paddingY, context: context, theme: m.theme,
		latex: m.latexEnabled(), preserveOrdered: m.options.PreserveOrderedListMarkers, preserveEscapes: m.options.PreserveBackslashEscapes}
	if theme.CodeBlockIndent != nil {
		key.codeBlockIndent = "\x00" + *theme.CodeBlockIndent
	}
	return key, true
}

// resumeState is what a render keeps for the next one: the loop's lines, the rows laid out from them, and the points.
// rowStart[k] is the number of rows laid out from the first k lines.
type resumeState struct {
	key      resumeKey
	raw      string
	stable   int
	out      []string
	rows     []string
	rowStart []int
	points   []resumePoint
}

// pick returns the point to resume a render of content from, or nil when content does not keep the complete lines of the
// text this state rendered, or there is no point with another after it.
func (r *resumeState) pick(content string) *resumePoint {
	if len(r.points) < 2 || !strings.HasPrefix(content, r.raw[:r.stable]) {
		return nil
	}
	index := len(r.points) - 2
	p := r.points[index]
	p.index = index
	p.prefix = r.out[:p.outLen]
	return &p
}

// normalizeSource is the text blocks are parsed from: line endings as "\n" and tabs as three spaces.
func normalizeSource(content string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n"), "\t", "   ")
}

// rawLineStarts is the byte offset in content of each line normalizeSource(content) has.
func rawLineStarts(content string) []int {
	starts := []int{0}
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case '\r':
			if i+1 < len(content) && content[i+1] == '\n' {
				i++
			}
			starts = append(starts, i+1)
		case '\n':
			starts = append(starts, i+1)
		}
	}
	return starts
}

// layoutRows lays out lines in rows of the component's width, with the margins and background, and appends them to rows and
// the number of rows before each line to starts. The wraps it uses are kept for the next render by keepWraps.
func (m *Markdown) layoutRows(rows []string, starts []int, lines []string, width, contentWidth int) ([]string, []int) {
	if m.wrapsWidth != contentWidth {
		clear(m.wraps)
		m.wrapsWidth = contentWidth
	}
	if m.nextWraps == nil {
		m.nextWraps = map[string][]string{}
	}
	margin := strings.Repeat(" ", m.paddingX)
	background := func(line string) string { return line }
	if m.defaultTextStyle != nil && m.defaultTextStyle.BgColor != nil {
		background = m.defaultTextStyle.BgColor
	}
	add := func(row string) {
		if widthx.IsImageLine(row) {
			rows = append(rows, row)
			return
		}
		row = margin + row + margin
		row += strings.Repeat(" ", max(0, width-widthx.VisibleWidth(row)))
		rows = append(rows, background(row))
	}
	for _, line := range lines {
		starts = append(starts, len(rows))
		if widthx.IsImageLine(line) || (!strings.ContainsAny(line, "\r\n") && widthx.VisibleWidth(line) <= contentWidth) {
			add(line)
			continue
		}
		wrapped, ok := m.wraps[line]
		if !ok {
			wrapped = wrapTextWithAnsi(line, contentWidth)
		}
		m.nextWraps[line] = wrapped
		for _, row := range wrapped {
			add(row)
		}
	}
	return rows, starts
}

// keepWraps ends a render's layout: the rows of lines this render did not wrap are dropped, so the cache holds one render's
// rows.
func (m *Markdown) keepWraps() {
	clear(m.wraps)
	m.wraps, m.nextWraps = m.nextWraps, m.wraps
}
