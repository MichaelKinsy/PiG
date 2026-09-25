package tui

import (
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports pi-tui's alt-screen-search.ts: transcript search over rendered lines
// and its overlay component. Forced Go mechanics: corpus offsets are UTF-8
// byte offsets instead of UTF-16 code units. They are only compared with each
// other, and the one place they become columns (linearColumns spans) holds
// printable ASCII, where bytes, code units, and cells coincide.

// altScreenPlatform is the platform the search controls label keys for.
// Tests override it; production reads runtime.GOOS once.
var altScreenPlatform = runtime.GOOS

type searchSourceSpan struct {
	textStart     int
	textEnd       int
	row           int
	startCol      int
	endCol        int
	linearColumns bool
}

type searchCorpus struct {
	text  string
	spans []searchSourceSpan
}

// AltScreenSearchSegment is one row-local cell range of a match. Mirrors
// upstream AltScreenSearchSegment.
type AltScreenSearchSegment struct {
	Row      int
	StartCol int
	EndCol   int
}

// AltScreenSearchMatch is one match, possibly spanning rows. Mirrors upstream
// AltScreenSearchMatch.
type AltScreenSearchMatch struct {
	Segments []AltScreenSearchSegment
}

var printableASCII = regexp.MustCompile(`^[\x20-\x7e]*$`)

// jsIsSpace mirrors the ECMAScript \s class (WhiteSpace and LineTerminator).
func jsIsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func isAllJSSpace(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if !jsIsSpace(r) {
			return false
		}
	}
	return true
}

type corpusBuilder struct {
	chunks           strings.Builder
	spans            []searchSourceSpan
	textLength       int
	pendingSeparator bool
}

func (b *corpusBuilder) appendSeparator() {
	if !b.pendingSeparator {
		return
	}
	b.chunks.WriteByte(' ')
	b.textLength++
	b.pendingSeparator = false
}

func (b *corpusBuilder) appendText(text string, row, startCol, endCol int, linear bool) {
	b.appendSeparator()
	b.chunks.WriteString(text)
	b.spans = append(b.spans, searchSourceSpan{
		textStart: b.textLength, textEnd: b.textLength + len(text),
		row: row, startCol: startCol, endCol: endCol, linearColumns: linear,
	})
	b.textLength += len(text)
}

// appendASCIILine indexes complete non-space runs at once instead of
// segmenting and allocating one mapping per cell.
func (b *corpusBuilder) appendASCIILine(line string, row int) {
	column := 0
	index := 0
	for index < len(line) {
		if line[index] == ' ' {
			if b.textLength > 0 {
				b.pendingSeparator = true
			}
			column++
			index++
			continue
		}
		end := index + 1
		for end < len(line) && line[end] != ' ' {
			end++
		}
		b.appendText(line[index:end], row, column, column+end-index, true)
		column += end - index
		index = end
	}
}

func (b *corpusBuilder) appendGraphemeLine(line string, row int) {
	column := 0
	for _, grapheme := range graphemeSegments(line) {
		width := widthx.VisibleWidth(grapheme.Text)
		if isAllJSSpace(grapheme.Text) {
			if b.textLength > 0 {
				b.pendingSeparator = true
			}
			column += width
			continue
		}
		b.appendText(grapheme.Text, row, column, column+width, false)
		column += width
	}
}

// buildSearchCorpus joins rendered lines into one searchable text where each
// whitespace run, and each row break, becomes a single space. Mirrors
// upstream buildSearchCorpus.
func buildSearchCorpus(lines []string) searchCorpus {
	var b corpusBuilder
	for row, raw := range lines {
		line := widthx.StripTerminalSequences(raw)
		// Rendered transcripts are overwhelmingly ASCII.
		if printableASCII.MatchString(line) {
			b.appendASCIILine(line, row)
		} else {
			b.appendGraphemeLine(line, row)
		}
		if b.textLength > 0 {
			b.pendingSeparator = true
		}
	}
	return searchCorpus{text: b.chunks.String(), spans: b.spans}
}

// normalizeSearchQuery collapses whitespace runs to one space and trims.
// Mirrors upstream normalizeQuery.
func normalizeSearchQuery(query string) string {
	return strings.Join(strings.FieldsFunc(query, jsIsSpace), " ")
}

func matchSegments(spans []searchSourceSpan, spanIndex, start, end int) []AltScreenSearchSegment {
	var segments []AltScreenSearchSegment
	for index := spanIndex; index < len(spans); index++ {
		span := spans[index]
		if span.textStart >= end {
			break
		}
		if span.textEnd <= start {
			continue
		}
		startCol, endCol := span.startCol, span.endCol
		if span.linearColumns {
			startCol = span.startCol + max(start, span.textStart) - span.textStart
			endCol = span.startCol + min(end, span.textEnd) - span.textStart
		}
		if n := len(segments); n > 0 && segments[n-1].Row == span.row && startCol <= segments[n-1].EndCol {
			segments[n-1].EndCol = max(segments[n-1].EndCol, endCol)
		} else {
			segments = append(segments, AltScreenSearchSegment{Row: span.row, StartCol: startCol, EndCol: endCol})
		}
	}
	return segments
}

// findSearchCorpusMatches maps case-insensitive literal matches back to
// rendered cells. Mirrors upstream findSearchCorpusMatches.
func findSearchCorpusMatches(corpus searchCorpus, normalizedQuery string) []AltScreenSearchMatch {
	if normalizedQuery == "" {
		return nil
	}
	expression := regexp.MustCompile("(?i)" + regexp.QuoteMeta(normalizedQuery))
	var matches []AltScreenSearchMatch
	spanIndex := 0
	for _, location := range expression.FindAllStringIndex(corpus.text, -1) {
		start, end := location[0], location[1]
		for spanIndex < len(corpus.spans) && corpus.spans[spanIndex].textEnd <= start {
			spanIndex++
		}
		segments := matchSegments(corpus.spans, spanIndex, start, end)
		for spanIndex < len(corpus.spans) && corpus.spans[spanIndex].textEnd <= end {
			spanIndex++
		}
		if len(segments) > 0 {
			matches = append(matches, AltScreenSearchMatch{Segments: segments})
		}
	}
	return matches
}

// AltScreenSearchIndex caches the searchable corpus and matches while the
// rendered transcript lines remain unchanged. Mirrors upstream
// AltScreenSearchIndex.
type AltScreenSearchIndex struct {
	sourceLines     []string
	corpus          *searchCorpus
	normalizedQuery *string
	matches         []AltScreenSearchMatch
}

// Search returns the matches for query over lines and whether they changed
// since the previous call. Mirrors upstream AltScreenSearchIndex.search.
func (i *AltScreenSearchIndex) Search(lines []string, query string) (matches []AltScreenSearchMatch, changed bool) {
	sourceChanged := i.sourceLines == nil || len(i.sourceLines) != len(lines)
	if !sourceChanged {
		for index, line := range lines {
			if i.sourceLines[index] != line {
				sourceChanged = true
				break
			}
		}
	}
	if sourceChanged || i.corpus == nil {
		i.sourceLines = append([]string{}, lines...)
		corpus := buildSearchCorpus(lines)
		i.corpus = &corpus
	}
	normalizedQuery := normalizeSearchQuery(query)
	changed = sourceChanged || i.normalizedQuery == nil || normalizedQuery != *i.normalizedQuery
	if changed {
		i.normalizedQuery = &normalizedQuery
		i.matches = findSearchCorpusMatches(*i.corpus, normalizedQuery)
	}
	return i.matches, changed
}

// FindAltScreenSearchMatches searches lines for query without caching. Mirrors
// upstream findAltScreenSearchMatches.
func FindAltScreenSearchMatches(lines []string, query string) []AltScreenSearchMatch {
	normalizedQuery := normalizeSearchQuery(query)
	if normalizedQuery == "" {
		return nil
	}
	return findSearchCorpusMatches(buildSearchCorpus(lines), normalizedQuery)
}

// GetAltScreenSearchMatchKey identifies a match by its first and last cells.
// Mirrors upstream getAltScreenSearchMatchKey.
func GetAltScreenSearchMatchKey(match AltScreenSearchMatch) string {
	if len(match.Segments) == 0 {
		return ""
	}
	first := match.Segments[0]
	last := match.Segments[len(match.Segments)-1]
	return strconv.Itoa(first.Row) + ":" + strconv.Itoa(first.StartCol) + ":" + strconv.Itoa(last.Row) + ":" + strconv.Itoa(last.EndCol)
}

// AltScreenSearchComponent is the transcript search box: an input, a result
// counter, and previous/next buttons in its bottom border. Mirrors upstream
// AltScreenSearchComponent. Navigation directions are -1 (previous), 1 (next),
// and 0 (none, upstream undefined).
type AltScreenSearchComponent struct {
	input                      *TextInput
	onQueryChange              func(query string)
	navigationButtonStyle      func(text string, hovered bool) string
	resultCount                int
	resultIndex                int
	previousButtonStart        int
	previousButtonEnd          int
	nextButtonStart            int
	nextButtonEnd              int
	hoveredNavigationDirection int
	focused                    bool
}

// NewAltScreenSearchComponent creates the search box. A nil
// navigationButtonStyle leaves the buttons unstyled.
func NewAltScreenSearchComponent(onQueryChange func(query string), navigationButtonStyle func(text string, hovered bool) string) *AltScreenSearchComponent {
	if navigationButtonStyle == nil {
		navigationButtonStyle = func(text string, _ bool) string { return text }
	}
	prompt := " "
	input := NewInput(InputOptions{
		Prompt:           &prompt,
		Placeholder:      "Find in transcript",
		PlaceholderStyle: func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
	})
	input.Focused = false
	return &AltScreenSearchComponent{
		input:                 input,
		onQueryChange:         onQueryChange,
		navigationButtonStyle: navigationButtonStyle,
		resultIndex:           -1,
		previousButtonStart:   -1,
		previousButtonEnd:     -1,
		nextButtonStart:       -1,
		nextButtonEnd:         -1,
	}
}

// Focused reports the Focusable state. Mirrors upstream get focused.
func (c *AltScreenSearchComponent) Focused() bool { return c.focused }

// SetFocused updates the Focusable state and forwards it to the input.
func (c *AltScreenSearchComponent) SetFocused(value bool) {
	c.focused = value
	c.input.Focused = value
}

// SetResult sets the displayed match index (-1 for none) and count.
func (c *AltScreenSearchComponent) SetResult(index, count int) {
	c.resultIndex = index
	c.resultCount = count
}

// GetNavigationDirectionAt returns the button under a component-local cell.
func (c *AltScreenSearchComponent) GetNavigationDirectionAt(row, column int) int {
	if row != 2 {
		return 0
	}
	if column >= c.previousButtonStart && column < c.previousButtonEnd {
		return -1
	}
	if column >= c.nextButtonStart && column < c.nextButtonEnd {
		return 1
	}
	return 0
}

// SetHoveredNavigationDirection records the hovered button and reports
// whether it changed.
func (c *AltScreenSearchComponent) SetHoveredNavigationDirection(direction int) bool {
	if direction == c.hoveredNavigationDirection {
		return false
	}
	c.hoveredNavigationDirection = direction
	return true
}

// HandleInput edits the query and reports a changed query.
func (c *AltScreenSearchComponent) HandleInput(data string) {
	previous := c.input.Text()
	c.input.HandleInput(data)
	if query := c.input.Text(); query != previous {
		c.onQueryChange(query)
	}
}

// Invalidate invalidates the input.
func (c *AltScreenSearchComponent) Invalidate() { c.input.Invalidate() }

// formatSearchKey labels a key for the navigation buttons: each part
// capitalized, with alt named Option on macOS; "Unbound" without a key.
func formatSearchKey(keys []string) string {
	if len(keys) == 0 || keys[0] == "" {
		return "Unbound"
	}
	parts := strings.Split(keys[0], "+")
	for i, part := range parts {
		if altScreenPlatform == "darwin" && strings.EqualFold(part, "alt") {
			parts[i] = "Option"
			continue
		}
		if part != "" {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "+")
}

func (c *AltScreenSearchComponent) renderContent(innerWidth int) string {
	query := c.input.Text()
	result := ""
	switch {
	case query == "":
	case c.resultCount == 0:
		result = "No matches"
	default:
		result = strconv.Itoa(c.resultIndex+1) + "/" + strconv.Itoa(c.resultCount)
	}
	visibleResult := widthx.TruncateToWidth(result, max(0, innerWidth-3), "", false)
	resultText := ""
	if visibleResult != "" {
		resultText = "\x1b[2m " + visibleResult + " \x1b[22m"
	}
	inputWidth := max(0, innerWidth-widthx.VisibleWidth(resultText))
	inputLine := widthx.TruncateToWidth(c.input.Render(max(1, inputWidth))[0], inputWidth, "", false)
	inputPadding := strings.Repeat(" ", max(0, inputWidth-widthx.VisibleWidth(inputLine)))
	return inputLine + inputPadding + resultText
}

// Render draws the box: top border, input row, and a bottom border carrying
// the navigation buttons. Mirrors upstream AltScreenSearchComponent.render.
func (c *AltScreenSearchComponent) Render(width int) []string {
	safeWidth := max(1, width)
	innerWidth := max(0, safeWidth-2)
	keybindings := GetKeybindings()
	previousKey := formatSearchKey(keybindings.GetKeys(KBAltScreenSearchPrevious))
	nextKey := formatSearchKey(keybindings.GetKeys(KBAltScreenSearchNext))
	content := c.renderContent(innerWidth)

	previousButton := "↑ " + previousKey
	nextButton := "↓ " + nextKey
	separator := " · "
	const outerGapWidth = 1
	availableControlsWidth := max(0, innerWidth-outerGapWidth*2-1)
	controlsWidth := widthx.VisibleWidth(previousButton) + widthx.VisibleWidth(separator) + widthx.VisibleWidth(nextButton)
	if controlsWidth > availableControlsWidth {
		previousButton, nextButton, separator = "↑", "↓", " "
		controlsWidth = widthx.VisibleWidth(previousButton) + widthx.VisibleWidth(separator) + widthx.VisibleWidth(nextButton)
	}
	showButtons := controlsWidth <= availableControlsWidth
	renderedButtons := ""
	outerGapsWidth := 0
	shownControlsWidth := 0
	if showButtons {
		renderedButtons = c.navigationButtonStyle(previousButton, c.hoveredNavigationDirection == -1) +
			separator + c.navigationButtonStyle(nextButton, c.hoveredNavigationDirection == 1)
		outerGapsWidth = outerGapWidth * 2
		shownControlsWidth = controlsWidth
	}
	rightRuleWidth := 0
	if renderedButtons != "" && innerWidth > controlsWidth+outerGapsWidth {
		rightRuleWidth = 1
	}
	leftRuleWidth := max(0, innerWidth-shownControlsWidth-outerGapsWidth-rightRuleWidth)
	c.previousButtonStart, c.previousButtonEnd, c.nextButtonStart, c.nextButtonEnd = -1, -1, -1, -1
	if showButtons {
		c.previousButtonStart = 1 + leftRuleWidth + outerGapWidth
		c.previousButtonEnd = c.previousButtonStart + widthx.VisibleWidth(previousButton)
		c.nextButtonStart = c.previousButtonEnd + widthx.VisibleWidth(separator)
		c.nextButtonEnd = c.nextButtonStart + widthx.VisibleWidth(nextButton)
	}

	if safeWidth == 1 {
		return []string{"┌", "│", "└"}
	}
	gap := ""
	if renderedButtons != "" {
		gap = " "
	}
	return []string{
		"┌" + strings.Repeat("─", innerWidth) + "┐",
		"│" + content + "│",
		"└" + strings.Repeat("─", leftRuleWidth) + gap + renderedButtons + gap + strings.Repeat("─", rightRuleWidth) + "┘",
	}
}
