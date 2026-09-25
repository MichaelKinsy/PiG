package tui

import (
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Viewport input for TuiAltScreen: focus reports, keyboard navigation, and
// transcript search. Mirrors upstream handleViewportInput and the search
// methods of tui-alt-screen.ts.

// osc133PromptStart matches a leading OSC 133;A prompt-start mark, used by
// scrollToPrompt. Mirrors upstream OSC133_PROMPT_START.
var osc133PromptStart = regexp.MustCompile("^\x1b\\]133;A(?:\x07|\x1b\\\\)")

// altSearchSelectionMode mirrors upstream SearchSelectionMode.
type altSearchSelectionMode string

const (
	searchSelectQuery    altSearchSelectionMode = "query"
	searchSelectRetain   altSearchSelectionMode = "retain"
	searchSelectNext     altSearchSelectionMode = "next"
	searchSelectPrevious altSearchSelectionMode = "previous"
)

// altActiveSearch is the open transcript search. Fields other than overlay
// and component are guarded by t.mu. Mirrors upstream ActiveSearch.
type altActiveSearch struct {
	component     *AltScreenSearchComponent
	index         AltScreenSearchIndex
	overlay       *OverlayHandle
	query         string
	matches       []AltScreenSearchMatch
	selectedIndex int
	selectedKey   string
	anchorRow     int
	selectionMode altSearchSelectionMode
}

// altScreenSearchActions are matched only while the search box has focus.
var altScreenSearchActions = []TUIKeybinding{KBAltScreenSearchNext, KBAltScreenSearchPrevious, KBAltScreenSearchClose}

// altScreenScrollActions are the viewport navigation actions, in upstream
// match order.
var altScreenScrollActions = []TUIKeybinding{
	KBAltScreenPageUp, KBAltScreenPageDown, KBAltScreenHalfPageUp, KBAltScreenHalfPageDown,
	KBAltScreenLineUp, KBAltScreenLineDown, KBAltScreenPreviousPrompt, KBAltScreenNextPrompt,
	KBAltScreenTop, KBAltScreenBottom,
}

// HandleViewportInput routes a raw input chunk that targets the viewport:
// focus reports, mouse reports, and viewport keybindings. It returns true when
// it consumed the input so the driver does not dispatch it as a keystroke.
// Mirrors upstream handleViewportInput.
func (t *TuiAltScreen) HandleViewportInput(data string) bool {
	switch data {
	case altFocusOut:
		t.handleFocusOut()
		return true
	case altFocusIn:
		return true
	}
	if wheel, ok := parseWheelEvent(data); ok {
		return t.handleWheel(wheel)
	}
	if event, ok := parseSgrMouseEvent(data); ok {
		t.handleMouseEvent(event)
		return true
	}
	if isMouseSequence(data) {
		return true
	}
	action, ok := t.matchViewportAction(data)
	if !ok {
		return false
	}
	if !IsKeyRelease(data) {
		t.performViewportAction(action)
	}
	return true
}

// handleFocusOut cancels in-progress pointer gestures and drops a selection
// that was still being made. Mirrors upstream's FOCUS_OUT branch.
func (t *TuiAltScreen) handleFocusOut() {
	t.mu.Lock()
	hadActiveSelection := t.selectionPressActive
	_, _, nonEmpty := t.getSelectionBounds()
	hadNonEmptyActiveSelection := hadActiveSelection && nonEmpty
	t.selectionPressActive = false
	t.stopSelectionAutoScrollLocked()
	t.stopScrollbarHover()
	render := t.activeSearch != nil && t.activeSearch.component.SetHoveredNavigationDirection(0)
	t.stopScrollbarDrag()
	t.pressedURL = ""
	t.pressedURLSet = false
	t.selectionDragged = false
	if hadActiveSelection {
		t.selectionAnchor = nil
		t.selectionFocus = nil
		t.selectionGranularity = selectCharacter
		t.selectionInitialRange = nil
		render = render || hadNonEmptyActiveSelection
	}
	t.lastClick = nil
	t.unlockAndApplyHover()
	t.clearComponentMouseGesture()
	t.lastComponentClick = nil
	if render {
		t.RequestRender()
	}
}

// matchViewportAction returns the viewport action bound to data: search
// always, the search keys while the search box has focus, and navigation
// unless another overlay has focus. Mirrors the keybinding tail of upstream
// handleViewportInput.
func (t *TuiAltScreen) matchViewportAction(data string) (TUIKeybinding, bool) {
	keybindings := GetKeybindings()
	if keybindings.Matches(data, KBAltScreenSearch) {
		return KBAltScreenSearch, true
	}
	if t.IsSearchFocused() {
		for _, action := range altScreenSearchActions {
			if keybindings.Matches(data, action) {
				return action, true
			}
		}
	}
	if t.shouldDeferViewportInputToOverlay() {
		return "", false
	}
	for _, action := range altScreenScrollActions {
		if keybindings.Matches(data, action) {
			return action, true
		}
	}
	return "", false
}

// performViewportAction runs one viewport action. Mirrors the per-binding
// bodies of upstream handleViewportInput.
func (t *TuiAltScreen) performViewportAction(action TUIKeybinding) {
	viewportHeight := t.getPrimaryScrollViewLocked().ViewportHeight()
	switch action {
	case KBAltScreenSearch:
		t.toggleSearch()
	case KBAltScreenSearchNext:
		t.navigateSearch(1)
	case KBAltScreenSearchPrevious:
		t.navigateSearch(-1)
	case KBAltScreenSearchClose:
		t.closeSearch()
	case KBAltScreenPageUp:
		t.ScrollBy(-max(1, viewportHeight-altPageScrollOverlap))
	case KBAltScreenPageDown:
		t.ScrollBy(max(1, viewportHeight-altPageScrollOverlap))
	case KBAltScreenHalfPageUp:
		t.ScrollBy(-max(1, viewportHeight/2))
	case KBAltScreenHalfPageDown:
		t.ScrollBy(max(1, viewportHeight/2))
	case KBAltScreenLineUp:
		t.ScrollBy(-1)
	case KBAltScreenLineDown:
		t.ScrollBy(1)
	case KBAltScreenPreviousPrompt:
		t.scrollToPrompt(-1)
	case KBAltScreenNextPrompt:
		t.scrollToPrompt(1)
	case KBAltScreenTop:
		t.ScrollToTop()
	case KBAltScreenBottom:
		t.ScrollToBottom()
	}
}

func (t *TuiAltScreen) getPrimaryScrollViewLocked() *ScrollView {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.getPrimaryScrollView()
}

// shouldDeferViewportInputToOverlay reports whether a focused overlay other
// than the search box owns keyboard and wheel input. Mirrors upstream
// shouldDeferViewportInputToOverlay.
func (t *TuiAltScreen) shouldDeferViewportInputToOverlay() bool {
	return t.isOverlayFocused() && !t.IsSearchFocused()
}

// IsSearchFocused reports whether the transcript search box has focus.
func (t *TuiAltScreen) IsSearchFocused() bool {
	search := t.activeSearchSnapshot()
	return search != nil && search.overlay != nil && search.overlay.IsFocused()
}

// HandleFocusedSearchInput delivers a key to the transcript search box when it
// has focus and reports whether it did. Upstream's TUI hands focused-component
// input to the search box itself; PiG's driver owns input routing, so the
// driver calls this where upstream would reach the focused component.
func (t *TuiAltScreen) HandleFocusedSearchInput(data string) bool {
	search := t.activeSearchSnapshot()
	if search == nil || search.overlay == nil || !search.overlay.IsFocused() {
		return false
	}
	if ShouldDeliverKey(search.component, data) {
		search.component.HandleInput(data)
		t.RequestRender()
	}
	return true
}

func (t *TuiAltScreen) activeSearchSnapshot() *altActiveSearch {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activeSearch
}

// scrollToPrompt scrolls the primary view to the next or previous OSC 133
// prompt mark. Mirrors upstream scrollToPrompt.
func (t *TuiAltScreen) scrollToPrompt(direction int) {
	t.mu.Lock()
	if t.currentLayout == nil {
		t.mu.Unlock()
		return
	}
	sv := t.getPrimaryScrollView()
	box := GetScrollViewBox(*t.currentLayout, sv)
	t.mu.Unlock()
	if box == nil || box.ScrollContentLines == nil {
		return
	}
	lines := box.ScrollContentLines
	for row := sv.ScrollTop() + direction; row >= 0 && row < len(lines); row += direction {
		if osc133PromptStart.MatchString(lines[row]) {
			sv.ScrollTo(row)
			t.RequestRender()
			return
		}
	}
}

// toggleSearch opens the search box over the top-right corner, or closes it.
// Mirrors upstream toggleSearch.
func (t *TuiAltScreen) toggleSearch() {
	t.mu.Lock()
	if t.activeSearch != nil {
		t.mu.Unlock()
		t.closeSearch()
		return
	}
	search := &altActiveSearch{
		selectedIndex: -1,
		anchorRow:     t.getPrimaryScrollView().ScrollTop(),
		selectionMode: searchSelectQuery,
	}
	search.component = NewAltScreenSearchComponent(t.updateSearchQuery, t.searchNavigationButtonStyle)
	t.activeSearch = search
	t.mu.Unlock()
	margin := 1
	search.overlay = t.OpenOverlay(search.component, OverlayOptions{
		anchor: overlayTopRight, width: overlayPercent(40), minWidth: 32, marginAll: &margin,
	})
	search.component.SetFocused(search.overlay != nil && search.overlay.IsFocused())
	t.RequestRender()
}

// closeSearch removes the search box. Mirrors upstream closeSearch.
func (t *TuiAltScreen) closeSearch() {
	t.mu.Lock()
	search := t.activeSearch
	t.activeSearch = nil
	t.mu.Unlock()
	if search == nil {
		return
	}
	search.component.SetFocused(false)
	if search.overlay != nil {
		search.overlay.Close()
	}
	t.RequestRender()
}

// updateSearchQuery re-anchors the search at the selected match (or the
// viewport top) and searches for query on the next frame. Mirrors upstream
// updateSearchQuery.
func (t *TuiAltScreen) updateSearchQuery(query string) {
	t.mu.Lock()
	search := t.activeSearch
	if search == nil || query == search.query {
		t.mu.Unlock()
		return
	}
	if search.selectedIndex >= 0 && search.selectedIndex < len(search.matches) && len(search.matches[search.selectedIndex].Segments) > 0 {
		search.anchorRow = search.matches[search.selectedIndex].Segments[0].Row
	} else {
		search.anchorRow = t.getPrimaryScrollView().ScrollTop()
	}
	search.query = query
	search.selectionMode = searchSelectQuery
	search.component.SetResult(-1, 0)
	t.mu.Unlock()
	t.RequestRender()
}

// navigateSearch selects the next (1) or previous (-1) match on the next
// frame. Mirrors upstream navigateSearch.
func (t *TuiAltScreen) navigateSearch(direction int) {
	t.mu.Lock()
	search := t.activeSearch
	if search == nil || search.query == "" {
		t.mu.Unlock()
		return
	}
	search.selectionMode = searchSelectNext
	if direction < 0 {
		search.selectionMode = searchSelectPrevious
	}
	t.mu.Unlock()
	t.RequestRender()
}

// handleSearchMouseEvent hovers and presses the search box's navigation
// buttons. Mirrors upstream handleSearchMouseEvent.
func (t *TuiAltScreen) handleSearchMouseEvent(event sgrMouseEvent) bool {
	search := t.activeSearchSnapshot()
	if search == nil {
		return false
	}
	direction := 0
	if search.overlay != nil {
		if bounds, ok := search.overlay.GetBounds(); ok && event.x >= bounds.Col && event.x < bounds.Col+bounds.Width &&
			event.y >= bounds.Row && event.y < bounds.Row+bounds.Height {
			direction = search.component.GetNavigationDirectionAt(event.y-bounds.Row, event.x-bounds.Col)
		}
	}
	t.mu.Lock()
	hoverChanged := search.component.SetHoveredNavigationDirection(direction)
	t.mu.Unlock()
	if hoverChanged {
		t.RequestRender()
	}
	if direction == 0 || event.release || event.button&32 != 0 || event.button&3 != 0 {
		return false
	}
	t.navigateSearch(direction)
	return true
}

// selectSearchMatch picks the match index for the pending selection mode.
func selectSearchMatch(search *altActiveSearch, matches []AltScreenSearchMatch, changed bool) int {
	if len(matches) == 0 {
		return -1
	}
	exactIndex := search.selectedIndex
	if changed {
		exactIndex = -1
		if search.selectedKey != "" {
			exactIndex = slices.IndexFunc(matches, func(match AltScreenSearchMatch) bool {
				return GetAltScreenSearchMatchKey(match) == search.selectedKey
			})
		}
	}
	baseIndex := exactIndex
	if baseIndex < 0 {
		baseIndex = min(search.selectedIndex, len(matches)-1)
	}
	switch search.selectionMode {
	case searchSelectQuery:
		low := sort.Search(len(matches), func(i int) bool {
			first := 0
			if len(matches[i].Segments) > 0 {
				first = matches[i].Segments[0].Row
			}
			return first >= search.anchorRow
		})
		if low < len(matches) {
			return low
		}
		return 0
	case searchSelectNext:
		if baseIndex < 0 {
			return 0
		}
		return (baseIndex + 1) % len(matches)
	case searchSelectPrevious:
		if baseIndex < 0 {
			return len(matches) - 1
		}
		return (baseIndex - 1 + len(matches)) % len(matches)
	}
	if exactIndex >= 0 {
		return exactIndex
	}
	return min(max(0, search.selectedIndex), len(matches)-1)
}

// refreshSearch re-runs the search against the frame's primary transcript,
// updates the selection, and reveals a newly selected match. It reports
// whether the view scrolled, so the frame must be laid out again. Caller holds
// t.mu. Mirrors upstream refreshSearch.
func (t *TuiAltScreen) refreshSearch(layout *LayoutFrame) bool {
	search := t.activeSearch
	if search == nil {
		return false
	}
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = t.implicitScrollView
	}
	box := GetScrollViewBox(*layout, scrollView)
	if box == nil || box.ScrollContentLines == nil || normalizeSearchQuery(search.query) == "" {
		search.matches = nil
		search.selectedIndex = -1
		search.selectedKey = ""
		search.selectionMode = searchSelectRetain
		search.component.SetResult(-1, 0)
		return false
	}
	shouldRevealSelection := search.selectionMode != searchSelectRetain
	matches, changed := search.index.Search(box.ScrollContentLines, search.query)
	search.matches = matches
	if !changed && search.selectionMode == searchSelectRetain {
		return false
	}
	selectedIndex := selectSearchMatch(search, matches, changed)
	search.selectedIndex = selectedIndex
	search.selectedKey = ""
	if selectedIndex >= 0 {
		search.selectedKey = GetAltScreenSearchMatchKey(matches[selectedIndex])
	}
	search.selectionMode = searchSelectRetain
	search.component.SetResult(selectedIndex, len(matches))
	if !shouldRevealSelection || selectedIndex < 0 || len(matches[selectedIndex].Segments) == 0 {
		return false
	}
	return revealSearchMatch(scrollView, matches[selectedIndex])
}

// revealSearchMatch scrolls a match that is off screen to a third of the way
// down the viewport without re-enabling follow-end. Mirrors the reveal tail
// of upstream refreshSearch.
func revealSearchMatch(scrollView *ScrollView, match AltScreenSearchMatch) bool {
	viewportHeight := scrollView.ViewportHeight()
	if viewportHeight <= 0 {
		return false
	}
	first := match.Segments[0]
	last := match.Segments[len(match.Segments)-1]
	before := scrollView.ScrollTop()
	target := before
	if first.Row < before || last.Row > before+viewportHeight-1 {
		target = first.Row - viewportHeight/3
	}
	scrollView.ScrollToWithOptions(target, ScrollViewScrollToOptions{DisableFollow: true})
	return scrollView.ScrollTop() != before
}

// applySearchTextHighlight styles the plain runs of text, keeping embedded
// escape sequences intact. Mirrors upstream applySearchTextHighlight.
func (t *TuiAltScreen) applySearchTextHighlight(text string, current bool) string {
	style := t.searchMatchStyle
	if current {
		style = t.searchCurrentMatchStyle
	}
	var result strings.Builder
	plainStart := 0
	index := 0
	for index < len(text) {
		code, n := widthx.ExtractAnsiCode(text, index)
		if n == 0 {
			index++
			continue
		}
		if index > plainStart {
			result.WriteString(style(text[plainStart:index]))
		}
		result.WriteString(code)
		index += n
		plainStart = index
	}
	if plainStart < len(text) {
		result.WriteString(style(text[plainStart:]))
	}
	return result.String()
}

// searchHighlightRange is one row-local highlight. Mirrors upstream
// SearchHighlightRange.
type searchHighlightRange struct {
	startCol, endCol int
	current          bool
}

// searchHighlightRanges maps the visible matches to screen rows and columns,
// clipped to the transcript box and left of its scrollbar.
func (t *TuiAltScreen) searchHighlightRanges(search *altActiveSearch, box *LayoutBox, scrollView *ScrollView, screenRows int) map[int][]searchHighlightRange {
	rangesByRow := map[int][]searchHighlightRange{}
	maxColumn := min(t.width, box.Rect.X+box.Rect.Width, box.Clip.X+box.Clip.Width)
	if geometry := GetScrollbarGeometry(box); geometry != nil {
		maxColumn = min(maxColumn, geometry.Column)
	}
	minRow := max(0, box.Rect.Y, box.Clip.Y)
	maxRow := min(screenRows, box.Rect.Y+box.Rect.Height, box.Clip.Y+box.Clip.Height)
	minColumn := max(0, box.Rect.X, box.Clip.X)
	scrollTop := scrollView.ScrollTop()
	minContentRow := scrollTop + minRow - box.Rect.Y
	maxContentRow := scrollTop + maxRow - box.Rect.Y - 1
	low := sort.Search(len(search.matches), func(i int) bool {
		segments := search.matches[i].Segments
		lastRow := -1
		if len(segments) > 0 {
			lastRow = segments[len(segments)-1].Row
		}
		return lastRow >= minContentRow
	})
	for matchIndex := low; matchIndex < len(search.matches); matchIndex++ {
		match := search.matches[matchIndex]
		if len(match.Segments) > 0 && match.Segments[0].Row > maxContentRow {
			break
		}
		for _, segment := range match.Segments {
			row := box.Rect.Y + segment.Row - scrollTop
			if row < minRow || row >= maxRow {
				continue
			}
			startCol := max(minColumn, box.Rect.X+segment.StartCol)
			endCol := min(maxColumn, box.Rect.X+segment.EndCol)
			if endCol <= startCol {
				continue
			}
			rangesByRow[row] = append(rangesByRow[row], searchHighlightRange{startCol, endCol, matchIndex == search.selectedIndex})
		}
	}
	return rangesByRow
}

// applySearchHighlights styles every visible match, the selected one with the
// current-match style. Caller holds t.mu. Mirrors upstream
// applySearchHighlights.
func (t *TuiAltScreen) applySearchHighlights(screen []string, layout *LayoutFrame) []string {
	search := t.activeSearch
	if search == nil || search.selectedIndex < 0 || len(search.matches) == 0 {
		return screen
	}
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = t.implicitScrollView
	}
	box := GetScrollViewBox(*layout, scrollView)
	if box == nil {
		return screen
	}
	result := slices.Clone(screen)
	for row, ranges := range t.searchHighlightRanges(search, box, scrollView, len(screen)) {
		line := result[row]
		if widthx.IsImageLine(line) {
			continue
		}
		lineWidth := widthx.VisibleWidth(line)
		slices.SortStableFunc(ranges, func(a, b searchHighlightRange) int { return b.startCol - a.startCol })
		for _, r := range ranges {
			startCol, endCol := min(r.startCol, lineWidth), min(r.endCol, lineWidth)
			if endCol <= startCol {
				continue
			}
			current := r.current
			line = highlightColumns(line, startCol, endCol, func(text string) string { return t.applySearchTextHighlight(text, current) })
		}
		result[row] = line
	}
	return result
}
