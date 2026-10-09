package tui

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// SelectListTheme styles a [SelectList]. A nil function leaves the text unstyled.
//
// upstream: packages/tui/src/components/select-list.ts:20 (SelectListTheme)
type SelectListTheme struct {
	SelectedPrefix func(text string) string
	SelectedText   func(text string) string
	Description    func(text string) string
	ScrollInfo     func(text string) string
	NoMatch        func(text string) string
}

// GetSelectListTheme is the list styling taken from the active theme (theme.ts getSelectListTheme): the selection and
// prefix are accent, everything else muted. The colors are read when each part is drawn.
func GetSelectListTheme() SelectListTheme {
	accent := func(text string) string {
		return fg(selectListColor(ActiveTheme().Accent, "\x1b[38;2;138;190;183m"), text)
	}
	muted := func(text string) string {
		return fg(selectListColor(ActiveTheme().Muted, "\x1b[38;2;128;128;128m"), text)
	}
	return SelectListTheme{SelectedPrefix: accent, SelectedText: accent, Description: muted, ScrollInfo: muted, NoMatch: muted}
}

// selectListColor is the theme color, or the dark theme's value while the active theme leaves it unset.
func selectListColor(color, fallback string) string {
	if color == "" {
		return fallback
	}
	return color
}

// SelectListLayoutOptions sizes the primary column of a [SelectList]. A zero width is unset.
//
// upstream: select-list.ts:36 (SelectListLayoutOptions)
type SelectListLayoutOptions struct {
	MinPrimaryColumnWidth int
	MaxPrimaryColumnWidth int
	// TruncatePrimary shortens the primary text; the result is clipped to the available width.
	TruncatePrimary func(context SelectListTruncatePrimaryContext) string
}

// SelectList is a scrolling list of [SelectItem]s with a primary column and optional descriptions. The visible window is centered on the selection.
//
// upstream: select-list.ts:42 (SelectList)
type SelectList struct {
	invalidatable
	items             []SelectItem
	filteredItems     []SelectItem
	selectedIndex     int
	mousePressedIndex int // -1 when no press is pending
	maxVisible        int
	theme             SelectListTheme
	layout            SelectListLayoutOptions

	// OnSelect runs when the selected item is confirmed (Enter or a click).
	OnSelect func(item SelectItem)
	// OnCancel runs on the cancel key.
	OnCancel func()
	// OnSelectionChange runs when the selection moves.
	OnSelectionChange func(item SelectItem)
}

const (
	selectListDefaultPrimaryColumnWidth = 32
	selectListPrimaryColumnGap          = 2
	// upstream: packages/tui/src/components/select-list.ts:MIN_DESCRIPTION_WIDTH
	selectListMinDescriptionWidth = 10
)

// NewSelectList creates a list of items showing up to maxVisible rows.
func NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme, layout ...SelectListLayoutOptions) *SelectList {
	list := &SelectList{items: items, filteredItems: items, maxVisible: maxVisible, theme: theme, mousePressedIndex: -1}
	if len(layout) > 0 {
		list.layout = layout[0]
	}
	return list
}

// SetFilter keeps the items whose value starts with filter, ignoring case, and resets the selection to the first.
func (l *SelectList) SetFilter(filter string) {
	prefix := strings.ToLower(filter)
	l.filteredItems = l.filteredItems[:0:0]
	for _, item := range l.items {
		if strings.HasPrefix(strings.ToLower(item.Value), prefix) {
			l.filteredItems = append(l.filteredItems, item)
		}
	}
	l.selectedIndex = 0
	l.Invalidate()
}

// SetSelectedIndex selects an item of the filtered list, clamping the index into range.
func (l *SelectList) SetSelectedIndex(index int) {
	l.selectedIndex = max(0, min(index, len(l.filteredItems)-1))
	l.Invalidate()
}

// SelectedItem returns the selected item of the filtered list.
func (l *SelectList) SelectedItem() (SelectItem, bool) {
	if l.selectedIndex < 0 || l.selectedIndex >= len(l.filteredItems) {
		return SelectItem{}, false
	}
	return l.filteredItems[l.selectedIndex], true
}

// pig additive (D107): a component kit host keeps one list across a theme switch and reads the
// list's state to report it; upstream constructs a new list and has no accessors.

// SetTheme restyles the list.
func (l *SelectList) SetTheme(theme SelectListTheme) {
	l.theme = theme
	l.Invalidate()
}

// FilteredItems returns the items passing the current filter, in order.
func (l *SelectList) FilteredItems() []SelectItem { return l.filteredItems }

// SelectedIndex returns the selection's index into FilteredItems.
func (l *SelectList) SelectedIndex() int { return l.selectedIndex }

// MaxVisible returns the number of rows rendered at once.
func (l *SelectList) MaxVisible() int { return l.maxVisible }

func styled(fn func(string) string, text string) string {
	if fn == nil {
		return text
	}
	return fn(text)
}

// Render draws the visible rows, then a (selected/total) line when the list scrolls.
func (l *SelectList) Render(width int) []string {
	if len(l.filteredItems) == 0 {
		return []string{styled(l.theme.NoMatch, "  No matching commands")}
	}
	primaryColumnWidth := l.primaryColumnWidth()
	startIndex, endIndex := l.visibleRange()
	var lines []string
	for i := startIndex; i < endIndex; i++ {
		item := l.filteredItems[i]
		description := ""
		if item.Description != "" {
			description = normalizeToSingleLine(item.Description)
		}
		lines = append(lines, l.renderItem(item, i == l.selectedIndex, width, description, primaryColumnWidth))
	}
	if startIndex > 0 || endIndex < len(l.filteredItems) {
		scrollText := "  (" + strconv.Itoa(l.selectedIndex+1) + "/" + strconv.Itoa(len(l.filteredItems)) + ")"
		lines = append(lines, styled(l.theme.ScrollInfo, widthx.TruncateToWidth(scrollText, width-2, "", false)))
	}
	return lines
}

func (l *SelectList) notifySelectionChange() {
	if item, ok := l.SelectedItem(); ok && l.OnSelectionChange != nil {
		l.OnSelectionChange(item)
	}
}

// HandleMouse scrolls on the wheel, selects on press and confirms on click; hover never changes the selection.
func (l *SelectList) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if len(l.filteredItems) == 0 {
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != 0 {
		delta := 1
		if event.WheelDelta < 0 {
			delta = -1
		}
		previous := l.selectedIndex
		l.selectedIndex = max(0, min(len(l.filteredItems)-1, l.selectedIndex+delta))
		changed := l.selectedIndex != previous
		if changed {
			l.notifySelectionChange()
			l.Invalidate()
		}
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Render: new(changed)}}
	}
	if event.Button != MouseButtonLeft || (event.Type != MousePress && event.Type != MouseClick) {
		return nil
	}
	startIndex, endIndex := l.visibleRange()
	itemIndex := startIndex + event.Y
	if itemIndex < startIndex || itemIndex >= endIndex {
		return nil
	}
	if event.Type == MousePress {
		l.mousePressedIndex = itemIndex
		if l.selectedIndex != itemIndex {
			l.selectedIndex = itemIndex
			l.notifySelectionChange()
			l.Invalidate()
		}
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
	}
	clicked := itemIndex
	if l.mousePressedIndex >= 0 {
		clicked = l.mousePressedIndex
	}
	l.mousePressedIndex = -1
	if l.selectedIndex != clicked {
		l.selectedIndex = clicked
		l.notifySelectionChange()
		l.Invalidate()
	}
	if item, ok := l.SelectedItem(); ok && l.OnSelect != nil {
		l.OnSelect(item)
	}
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true}}
}

// HandleInput moves the selection (wrapping at both ends), confirms or cancels through the tui.select.* keybindings.
func (l *SelectList) HandleInput(data string) {
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectUp):
		if l.selectedIndex == 0 {
			l.selectedIndex = len(l.filteredItems) - 1
		} else {
			l.selectedIndex--
		}
		l.notifySelectionChange()
		l.Invalidate()
	case kb.Matches(data, KBSelectDown):
		if l.selectedIndex == len(l.filteredItems)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
		l.notifySelectionChange()
		l.Invalidate()
	case kb.Matches(data, KBSelectConfirm):
		if item, ok := l.SelectedItem(); ok && l.OnSelect != nil {
			l.OnSelect(item)
		}
	case kb.Matches(data, KBSelectCancel):
		if l.OnCancel != nil {
			l.OnCancel()
		}
	}
}

func (l *SelectList) visibleRange() (start, end int) {
	start = max(0, min(l.selectedIndex-l.maxVisible/2, len(l.filteredItems)-l.maxVisible))
	return start, min(start+l.maxVisible, len(l.filteredItems))
}

func (l *SelectList) renderItem(item SelectItem, selected bool, width int, description string, primaryColumnWidth int) string {
	prefix := "  "
	if selected {
		prefix = "→ "
	}
	prefixWidth := widthx.VisibleWidth(prefix)
	if description != "" && width > 40 {
		effective := max(1, min(primaryColumnWidth, width-prefixWidth-4))
		maxPrimary := max(1, effective-selectListPrimaryColumnGap)
		value := l.truncatePrimary(item, selected, maxPrimary, effective)
		valueWidth := widthx.VisibleWidth(value)
		spacing := strings.Repeat(" ", max(1, effective-valueWidth))
		remaining := width - (prefixWidth + valueWidth + len(spacing)) - 2
		if remaining > selectListMinDescriptionWidth {
			desc := widthx.TruncateToWidth(description, remaining, "", false)
			if selected {
				return styled(l.theme.SelectedText, prefix+value+spacing+desc)
			}
			return prefix + value + styled(l.theme.Description, spacing+desc)
		}
	}
	maxWidth := width - prefixWidth - 2
	value := l.truncatePrimary(item, selected, maxWidth, maxWidth)
	if selected {
		return styled(l.theme.SelectedText, prefix+value)
	}
	return prefix + value
}

func (l *SelectList) displayValue(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

func (l *SelectList) truncatePrimary(item SelectItem, selected bool, maxWidth, columnWidth int) string {
	text := l.displayValue(item)
	if l.layout.TruncatePrimary != nil {
		text = l.layout.TruncatePrimary(SelectListTruncatePrimaryContext{Text: text, MaxWidth: maxWidth, ColumnWidth: columnWidth, Item: item, IsSelected: selected})
	} else {
		text = widthx.TruncateToWidth(text, maxWidth, "", false)
	}
	return widthx.TruncateToWidth(text, maxWidth, "", false)
}

func (l *SelectList) primaryColumnWidth() int {
	rawMin, rawMax := l.layout.MinPrimaryColumnWidth, l.layout.MaxPrimaryColumnWidth
	switch {
	case rawMin == 0 && rawMax == 0:
		rawMin, rawMax = selectListDefaultPrimaryColumnWidth, selectListDefaultPrimaryColumnWidth
	case rawMin == 0:
		rawMin = rawMax
	case rawMax == 0:
		rawMax = rawMin
	}
	lo, hi := max(1, min(rawMin, rawMax)), max(1, max(rawMin, rawMax))
	widest := 0
	for _, item := range l.filteredItems {
		widest = max(widest, widthx.VisibleWidth(l.displayValue(item))+selectListPrimaryColumnGap)
	}
	return clampInt(widest, lo, hi)
}
