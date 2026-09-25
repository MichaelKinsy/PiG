package tui

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/rivo/uniseg"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Application-owned text selection for TuiAltScreen: press/drag/release,
// double-click words, triple-click lines, edge auto-scroll, copy, and
// highlighting. Mirrors the selection half of upstream tui-alt-screen.ts.

// altCopyErrorFlashDurationMS keeps clipboard failures visible longer than a
// success flash. Mirrors upstream COPY_ERROR_FLASH_DURATION_MS.
const altCopyErrorFlashDurationMS = 5000

// altSelectionAutoScrollInterval mirrors upstream's setInterval(50ms) that
// advances the viewport while a selection drag holds past the visible edge.
const altSelectionAutoScrollInterval = 50 * time.Millisecond

// selectionPoint is a cell position in the selection coordinate space. When
// scrollView is set, row/col are in that view's content coordinates (row
// indexes scrollContentLines); otherwise they are absolute screen coordinates.
// boundary marks a point between cells rather than on one. Mirrors upstream
// SelectionPoint.
type selectionPoint struct {
	row        int
	col        int
	scrollView *ScrollView
	boundary   bool
}

// selectionRange mirrors upstream SelectionRange.
type selectionRange struct {
	start selectionPoint
	end   selectionPoint
}

// clickTarget records the last selection click for multi-click detection.
// Mirrors upstream ClickTarget.
type clickTarget struct {
	timestamp  time.Time
	count      int
	row        int
	scrollView *ScrollView
	wordStart  int
	wordEnd    int
}

const (
	selectCharacter = "character"
	selectWord      = "word"
	selectLine      = "line"
)

// getScrollSelectionPoint maps a pointer into scrollView content coordinates,
// clamped to the view's visible rect and content length, or nil when the view
// is not visibly painted. Caller holds t.mu. Mirrors upstream
// getScrollSelectionPoint.
func (t *TuiAltScreen) getScrollSelectionPoint(scrollView *ScrollView, x, y int) *selectionPoint {
	if t.currentLayout == nil {
		return nil
	}
	box := GetScrollViewBox(*t.currentLayout, scrollView)
	if box == nil || box.Rect.Height <= 0 || box.Clip.Height <= 0 {
		return nil
	}
	visibleTop := max(0, box.Rect.Y, box.Clip.Y)
	visibleBottom := min(t.height-1, box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1)
	if visibleBottom < visibleTop {
		return nil
	}
	pointerRow := max(visibleTop, min(visibleBottom, y))
	contentLen := len(box.ScrollContentLines)
	if box.ScrollContentLines == nil {
		contentLen = 1
	}
	maxContentRow := max(0, contentLen-1)
	return &selectionPoint{
		row:        max(0, min(maxContentRow, scrollView.ScrollTop()+pointerRow-box.Rect.Y)),
		col:        max(0, min(box.Rect.Width-1, x-box.Rect.X)),
		scrollView: scrollView,
	}
}

// getSelectionPoint prefers scrollView content coordinates and falls back to
// screen coordinates. Caller holds t.mu. Mirrors upstream getSelectionPoint.
func (t *TuiAltScreen) getSelectionPoint(event sgrMouseEvent, scrollView *ScrollView) selectionPoint {
	if scrollView != nil {
		if p := t.getScrollSelectionPoint(scrollView, event.x, event.y); p != nil {
			return *p
		}
	}
	return selectionPoint{row: max(0, min(t.height-1, event.y)), col: max(0, min(t.width-1, event.x))}
}

// getSelectionSourceLine returns the rendered line a point indexes. Caller
// holds t.mu. Mirrors upstream getSelectionSourceLine.
func (t *TuiAltScreen) getSelectionSourceLine(point selectionPoint) string {
	lines := t.previousScreen
	if point.scrollView != nil && t.currentLayout != nil {
		if box := GetScrollViewBox(*t.currentLayout, point.scrollView); box != nil && box.ScrollContentLines != nil {
			lines = box.ScrollContentLines
		}
	}
	if point.row >= 0 && point.row < len(lines) {
		return lines[point.row]
	}
	return ""
}

type wordSegment struct {
	start, end int
	selectable bool
	joiner     bool
}

// isWordLike mirrors Intl.Segmenter's isWordLike: a word segment holding a
// letter or number.
func isWordLike(segment string) bool {
	for _, r := range segment {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

func wordSegments(line string) []wordSegment {
	var segments []wordSegment
	start := 0
	state := -1
	for line != "" {
		var segment string
		segment, line, state = uniseg.FirstWordInString(line, state)
		end := start + widthx.VisibleWidth(segment)
		// Regular mode delegates double-click selection to the terminal
		// emulator. Fullscreen owns mouse selection, so mirror common terminal
		// word selection by keeping paths and kebab-case tokens whole.
		joiner := segment == "/" || segment == "-"
		segments = append(segments, wordSegment{start: start, end: end, selectable: isWordLike(segment) || joiner, joiner: joiner})
		start = end
	}
	return segments
}

// getWordSelection selects the word under a point, joining slash- and
// hyphen-separated segments. Caller holds t.mu. Mirrors upstream
// getWordSelection.
func (t *TuiAltScreen) getWordSelection(point selectionPoint) (selectionRange, bool) {
	segments := wordSegments(widthx.StripTerminalSequences(t.getSelectionSourceLine(point)))
	clicked := slices.IndexFunc(segments, func(s wordSegment) bool { return point.col >= s.start && point.col < s.end })
	if clicked < 0 {
		return selectionRange{}, false
	}
	canJoin := func(left, right wordSegment) bool {
		return left.selectable && right.selectable && (left.joiner || right.joiner)
	}
	selectionStart, selectionEnd := segments[clicked].start, segments[clicked].end
	for index := clicked; index > 0 && canJoin(segments[index-1], segments[index]); index-- {
		selectionStart = segments[index-1].start
	}
	for index := clicked; index < len(segments)-1 && canJoin(segments[index], segments[index+1]); index++ {
		selectionEnd = segments[index+1].end
	}
	start, end := point, point
	start.col = selectionStart
	end.col = selectionEnd
	end.boundary = true
	return selectionRange{start: start, end: end}, true
}

// getLineSelection selects a whole rendered line. Caller holds t.mu. Mirrors
// upstream getLineSelection.
func (t *TuiAltScreen) getLineSelection(point selectionPoint) selectionRange {
	start, end := point, point
	start.col = 0
	end.col = widthx.VisibleWidth(t.getSelectionSourceLine(point))
	end.boundary = true
	return selectionRange{start: start, end: end}
}

// updateSelectionFocus extends the selection to point, snapping to whole
// words or lines after a double or triple click. Caller holds t.mu. Mirrors
// upstream updateSelectionFocus.
func (t *TuiAltScreen) updateSelectionFocus(point selectionPoint) {
	if t.selectionGranularity == selectCharacter || t.selectionInitialRange == nil {
		t.selectionFocus = &point
		return
	}
	var target selectionRange
	if t.selectionGranularity == selectWord {
		word, ok := t.getWordSelection(point)
		if !ok {
			return
		}
		target = word
	} else {
		target = t.getLineSelection(point)
	}
	initial := *t.selectionInitialRange
	if target.start.row < initial.start.row || (target.start.row == initial.start.row && target.start.col < initial.start.col) {
		t.selectionAnchor = &initial.end
		t.selectionFocus = &target.start
	} else {
		t.selectionAnchor = &initial.start
		t.selectionFocus = &target.end
	}
}

// getClickCount counts consecutive clicks on the same word within the
// double-click interval, cycling 1-2-3. Caller holds t.mu. Mirrors upstream
// getClickCount.
func (t *TuiAltScreen) getClickCount(point selectionPoint, word selectionRange, hasWord bool) int {
	now := t.now()
	previous := t.lastClick
	count := 1
	if hasWord && previous != nil && now.Sub(previous.timestamp) <= altDoubleClickInterval &&
		previous.row == point.row && previous.scrollView == point.scrollView &&
		previous.wordStart == word.start.col && previous.wordEnd == word.end.col {
		count = previous.count%3 + 1
	}
	t.lastClick = nil
	if hasWord {
		t.lastClick = &clickTarget{
			timestamp: now, count: count, row: point.row, scrollView: point.scrollView,
			wordStart: word.start.col, wordEnd: word.end.col,
		}
	}
	return count
}

// updateSelectionAutoScroll arms or stops the edge auto-scroll timer based on
// where the drag pointer sits relative to the anchor view's visible rect.
// Caller holds t.mu. Mirrors upstream updateSelectionAutoScroll.
func (t *TuiAltScreen) updateSelectionAutoScroll(event sgrMouseEvent) {
	var sv *ScrollView
	if t.selectionAnchor != nil {
		sv = t.selectionAnchor.scrollView
	}
	if sv == nil || t.currentLayout == nil {
		t.stopSelectionAutoScrollLocked()
		return
	}
	box := GetScrollViewBox(*t.currentLayout, sv)
	if box == nil || box.Rect.Height <= 0 || box.Clip.Height <= 0 {
		t.stopSelectionAutoScrollLocked()
		return
	}
	visibleTop := max(0, box.Rect.Y, box.Clip.Y)
	visibleBottom := min(t.height-1, box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1)
	t.selectionDragPointer = &pointerXY{x: event.x, y: event.y}
	switch {
	case event.y <= visibleTop:
		t.selectionAutoScrollDir = -1
	case event.y >= visibleBottom:
		t.selectionAutoScrollDir = 1
	default:
		t.selectionAutoScrollDir = 0
	}
	if t.selectionAutoScrollDir == 0 {
		t.stopSelectionAutoScrollLocked()
		return
	}
	if t.selectionAutoScrollTimer != nil {
		return
	}
	gen := t.selectionAutoScrollGen
	t.selectionAutoScrollTimer = t.afterFunc(altSelectionAutoScrollInterval, func() { t.dispatchAutoScrollTick(func() { t.autoScrollSelection(gen) }) })
}

// dispatchAutoScrollTick marshals the auto-scroll tick onto the driver's owner
// loop (the same loop that owns rendering) through the owner-loop tick seam
// when one is installed, else runs it inline. Upstream runs the whole tick
// synchronously in its single event loop, so scrollBy, the selectionFocus
// update, and the render never interleave. The seam must not drop the tick: a
// dropped tick would leave the fired one-shot timer's stale non-nil pointer in
// place so the tick never re-arms, wedging auto-scroll: which is why the lossy
// render dispatcher is not reused here.
func (t *TuiAltScreen) dispatchAutoScrollTick(fn func()) {
	t.mu.Lock()
	dispatch := t.tickOnMain
	t.mu.Unlock()
	if dispatch != nil {
		dispatch(fn)
		return
	}
	fn()
}

// autoScrollSelection is the owned auto-scroll timer tick (upstream's
// setInterval(50ms) callback). It scrolls the view outside t.mu (ScrollBy
// calls back into RequestRender, which locks t.mu) and re-arms itself while
// the pointer stays past the viewport edge. Mirrors upstream
// autoScrollSelection.
func (t *TuiAltScreen) autoScrollSelection(gen uint64) {
	t.mu.Lock()
	if t.stopped || gen != t.selectionAutoScrollGen {
		t.mu.Unlock()
		return
	}
	var sv *ScrollView
	if t.selectionAnchor != nil {
		sv = t.selectionAnchor.scrollView
	}
	pointer := t.selectionDragPointer
	dir := t.selectionAutoScrollDir
	if sv == nil || pointer == nil || dir == 0 {
		t.stopSelectionAutoScrollLocked()
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()

	remaining := sv.ScrollBy(dir)

	t.mu.Lock()
	// Revalidate after the unlocked ScrollBy: a release, focus-out, or stop may
	// have cancelled this generation while we were scrolling. Bailing instead
	// of re-arming prevents resurrecting cleared selection state and leaking a
	// zombie timer.
	if t.stopped || gen != t.selectionAutoScrollGen {
		t.mu.Unlock()
		return
	}
	if remaining == dir {
		t.stopSelectionAutoScrollLocked()
		t.mu.Unlock()
		return
	}
	if p := t.getScrollSelectionPoint(sv, pointer.x, pointer.y); p != nil {
		t.updateSelectionFocus(*p)
	}
	t.selectionAutoScrollTimer = t.afterFunc(altSelectionAutoScrollInterval, func() { t.dispatchAutoScrollTick(func() { t.autoScrollSelection(gen) }) })
	t.mu.Unlock()
	t.RequestRender()
}

// stopSelectionAutoScrollLocked stops the auto-scroll timer and clears its
// state. Caller holds t.mu. Mirrors upstream stopSelectionAutoScroll.
func (t *TuiAltScreen) stopSelectionAutoScrollLocked() {
	if t.selectionAutoScrollTimer != nil {
		t.selectionAutoScrollTimer.Stop()
		t.selectionAutoScrollTimer = nil
	}
	// Invalidate any in-flight callback so a tick mid-ScrollBy does not re-arm.
	t.selectionAutoScrollGen++
	t.selectionAutoScrollDir = 0
	t.selectionDragPointer = nil
}

// clearTextSelectionLocked drops the selection and its gesture state. Caller
// holds t.mu. Mirrors upstream clearTextSelection.
func (t *TuiAltScreen) clearTextSelectionLocked() {
	t.stopSelectionAutoScrollLocked()
	t.selectionPressActive = false
	t.selectionAnchor = nil
	t.selectionFocus = nil
	t.selectionGranularity = selectCharacter
	t.selectionInitialRange = nil
	t.pressedURL = ""
	t.pressedURLSet = false
	t.selectionDragged = false
}

// handleSelectionRelease completes a press: it activates a clicked link,
// offers an unmoved click to controls, or copies the selection. Caller holds
// t.mu.
func (t *TuiAltScreen) handleSelectionRelease(event sgrMouseEvent, point selectionPoint, eff *mouseEffects) {
	if !t.selectionPressActive {
		return
	}
	t.selectionPressActive = false
	t.stopSelectionAutoScrollLocked()
	if t.selectionAnchor == nil {
		return
	}
	t.updateSelectionFocus(point)
	anchor := t.selectionAnchor
	isClick := !t.selectionDragged && anchor.scrollView == point.scrollView && anchor.row == point.row && anchor.col == point.col
	clickedURL, clickedURLSet := t.pressedURL, isClick && t.pressedURLSet
	t.pressedURL = ""
	t.pressedURLSet = false
	if clickedURLSet && t.openURL != nil {
		t.selectionAnchor = nil
		t.selectionFocus = nil
		eff.openURL = clickedURL
		eff.openURLSet = true
		eff.render = true
		return
	}
	if isClick {
		clickCount := 1
		if t.lastClick != nil {
			clickCount = t.lastClick.count
		}
		eff.clickEvent = &TuiMouseEvent{
			Type: MouseClick, Button: decodeMouseButton(event.button), X: event.x, Y: event.y, ScreenX: event.x, ScreenY: event.y,
			Width: max(1, t.width), Height: max(1, t.height),
			Shift: event.button&4 != 0, Alt: event.button&8 != 0, Ctrl: event.button&16 != 0, ClickCount: clickCount,
		}
	}
	if t.copyOnSelect {
		eff.selectionText, _ = t.activeSelectionTextLocked()
	}
	eff.render = true
}

// handleSelectionPress anchors a new selection, snapping to a word on a
// double click and a line on a triple click. Caller holds t.mu.
func (t *TuiAltScreen) handleSelectionPress(event sgrMouseEvent, eff *mouseEffects) {
	t.stopSelectionAutoScrollLocked()
	t.selectionPressActive = true
	var scrollView *ScrollView
	if !t.hasOverlay() && t.currentLayout != nil {
		if svs := GetScrollViewsAt(*t.currentLayout, event.x, event.y); len(svs) > 0 {
			scrollView = svs[0]
		}
	}
	anchor := t.getSelectionPoint(event, scrollView)
	word, hasWord := t.getWordSelection(anchor)
	clickCount := t.getClickCount(anchor, word, hasWord)
	var snapped *selectionRange
	t.selectionGranularity = selectCharacter
	switch clickCount {
	case 2:
		snapped = &word
		t.selectionGranularity = selectWord
	case 3:
		line := t.getLineSelection(anchor)
		snapped = &line
		t.selectionGranularity = selectLine
	}
	t.selectionInitialRange = snapped
	start, end := anchor, anchor
	if snapped != nil {
		start, end = snapped.start, snapped.end
	}
	t.selectionAnchor = &start
	t.selectionFocus = &end
	t.selectionDragged = false
	t.pressedURL = ""
	t.pressedURLSet = false
	if snapped == nil {
		line := ""
		if row := max(0, min(t.height-1, event.y)); row < len(t.previousScreen) {
			line = t.previousScreen[row]
		}
		t.pressedURL, t.pressedURLSet = widthx.GetOsc8LinkAtColumn(line, max(0, min(t.width-1, event.x)))
	}
	eff.render = true
}

// handleSelectionMouseEvent runs the primary-button selection state machine.
// Caller holds t.mu; effects land in eff. Mirrors upstream
// handleSelectionMouseEvent.
func (t *TuiAltScreen) handleSelectionMouseEvent(event sgrMouseEvent, eff *mouseEffects) {
	button := event.button & 3
	// Button code 3 is the generic release some terminals report.
	if button != 0 && (!event.release || button != 3) {
		return
	}
	var anchorScrollView *ScrollView
	if t.selectionAnchor != nil {
		anchorScrollView = t.selectionAnchor.scrollView
	}
	point := t.getSelectionPoint(event, anchorScrollView)
	switch {
	case event.release:
		t.handleSelectionRelease(event, point, eff)
	case event.button&32 != 0:
		if !t.selectionPressActive || t.selectionAnchor == nil {
			return
		}
		t.selectionDragged = true
		t.lastClick = nil
		t.pressedURL = ""
		t.pressedURLSet = false
		t.updateSelectionFocus(point)
		t.updateSelectionAutoScroll(event)
		eff.render = true
	default:
		t.handleSelectionPress(event, eff)
	}
}

// activeSelectionTextLocked returns the selected text with trailing spaces
// trimmed per row. Caller holds t.mu. Mirrors upstream getActiveSelectionText.
func (t *TuiAltScreen) activeSelectionTextLocked() (string, bool) {
	start, end, ok := t.getSelectionBounds()
	if !ok {
		return "", false
	}
	sourceLines := t.previousScreen
	if start.scrollView != nil {
		if t.currentLayout == nil {
			return "", false
		}
		box := GetScrollViewBox(*t.currentLayout, start.scrollView)
		if box == nil || box.ScrollContentLines == nil {
			return "", false
		}
		sourceLines = box.ScrollContentLines
	}
	lines := make([]string, 0, end.row-start.row+1)
	for row := start.row; row <= end.row; row++ {
		line := ""
		if row >= 0 && row < len(sourceLines) {
			line = sourceLines[row]
		}
		colStart, colEnd := t.getSelectionColumns(line, row, start, end, 0, widthx.VisibleWidth(line))
		segment := widthx.StripTerminalSequences(widthx.SliceByColumn(line, colStart, max(0, colEnd-colStart), true))
		lines = append(lines, strings.TrimRightFunc(segment, jsIsSpace))
	}
	text := strings.Join(lines, "\n")
	return text, text != ""
}

// copyTextToClipboard copies through the injected clipboard, or OSC 52, and
// flashes the outcome. It runs without t.mu. Mirrors upstream
// copyTextToClipboard.
func (t *TuiAltScreen) copyTextToClipboard(text string) bool {
	if t.copySelection != nil {
		if err := t.copySelection(text); err != nil {
			message := err.Error()
			if message == "" {
				message = "Copy failed"
			}
			t.flash(message, altCopyErrorFlashDurationMS)
			return false
		}
	} else {
		_, _ = fmt.Fprintf(t.out, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	}
	t.flash("Copied!", altScreenFlashDefaultDurationMS)
	return true
}

// getSelectionBounds returns the ordered selection endpoints, or ok=false when
// there is no selection, the endpoints span different scroll views, or anchor
// and focus coincide. Caller holds t.mu. Mirrors upstream getSelectionBounds.
func (t *TuiAltScreen) getSelectionBounds() (start, end selectionPoint, ok bool) {
	if t.selectionAnchor == nil || t.selectionFocus == nil {
		return selectionPoint{}, selectionPoint{}, false
	}
	anchor, focus := *t.selectionAnchor, *t.selectionFocus
	if anchor.scrollView != focus.scrollView || (anchor.row == focus.row && anchor.col == focus.col) {
		return selectionPoint{}, selectionPoint{}, false
	}
	if anchor.row < focus.row || (anchor.row == focus.row && anchor.col < focus.col) {
		return anchor, focus, true
	}
	return focus, anchor, true
}

// getSelectionColumns computes the [start, end) columns selected on line,
// clamped to [minColumn, maxColumn], snapping to grapheme cells unless the end
// is a between-cell boundary. Mirrors upstream getSelectionColumns.
func (t *TuiAltScreen) getSelectionColumns(line string, row int, start, end selectionPoint, minColumn, maxColumn int) (int, int) {
	lineWidth := widthx.VisibleWidth(line)
	colStart := max(0, minColumn)
	colEnd := min(lineWidth, maxColumn)
	if row == start.row {
		if s, _, ok := widthx.GraphemeCellRange(line, start.col); ok {
			colStart = s
		} else {
			colStart = min(start.col, lineWidth)
		}
	}
	if row == end.row {
		switch {
		case end.boundary:
			colEnd = min(end.col, lineWidth)
		default:
			if _, e, ok := widthx.GraphemeCellRange(line, end.col); ok {
				colEnd = e
			} else {
				colEnd = min(end.col+1, lineWidth)
			}
		}
	}
	return max(minColumn, colStart), min(maxColumn, colEnd)
}

// applySelectionHighlight wraps text in reverse video, re-asserting it after
// every embedded SGR so highlighting survives colour changes. Mirrors
// upstream applySelectionHighlight.
func (t *TuiAltScreen) applySelectionHighlight(text string) string {
	var b strings.Builder
	b.WriteString("\x1b[7m")
	i := 0
	for i < len(text) {
		code, n := widthx.ExtractAnsiCode(text, i)
		if n == 0 {
			b.WriteByte(text[i])
			i++
			continue
		}
		b.WriteString(code)
		if strings.HasSuffix(code, "m") {
			b.WriteString("\x1b[7m")
		}
		i += n
	}
	b.WriteString("\x1b[27m")
	return b.String()
}

// highlightColumns replaces [start, end) of line with style(selected).
func highlightColumns(line string, start, end int, style func(string) string) string {
	lineWidth := widthx.VisibleWidth(line)
	before := widthx.SliceByColumn(line, 0, start, true)
	selected := widthx.SliceByColumn(line, start, end-start, true)
	after := widthx.SliceByColumn(line, end, max(0, lineWidth-end), true)
	return before + style(selected) + after
}

// applySelection paints the reverse-video selection onto screen, translating
// scroll-view endpoints to screen cells and clipping to the view. Caller
// holds t.mu. Mirrors upstream applySelection.
func (t *TuiAltScreen) applySelection(screen []string, layout *LayoutFrame) []string {
	start, end, ok := t.getSelectionBounds()
	if !ok {
		return screen
	}
	screenStart, screenEnd := start, end
	minRow, maxRow := 0, len(screen)-1
	minColumn, maxColumn := 0, t.width
	if start.scrollView != nil {
		if layout == nil {
			return screen
		}
		box := GetScrollViewBox(*layout, start.scrollView)
		if box == nil {
			return screen
		}
		minRow = max(0, box.Rect.Y, box.Clip.Y)
		maxRow = min(len(screen)-1, box.Rect.Y+box.Rect.Height-1, box.Clip.Y+box.Clip.Height-1)
		minColumn = max(0, box.Rect.X, box.Clip.X)
		maxColumn = min(t.width, box.Rect.X+box.Rect.Width, box.Clip.X+box.Clip.Width)
		scrollTop := start.scrollView.ScrollTop()
		screenStart.row = box.Rect.Y + start.row - scrollTop
		screenStart.col = box.Rect.X + start.col
		screenEnd.row = box.Rect.Y + end.row - scrollTop
		screenEnd.col = box.Rect.X + end.col
	}
	result := slices.Clone(screen)
	for row, line := range result {
		if row < minRow || row > maxRow || row < screenStart.row || row > screenEnd.row || widthx.IsImageLine(line) {
			continue
		}
		colStart, colEnd := t.getSelectionColumns(line, row, screenStart, screenEnd, minColumn, maxColumn)
		if colEnd <= colStart {
			continue
		}
		result[row] = highlightColumns(line, colStart, colEnd, t.applySelectionHighlight)
	}
	return result
}
