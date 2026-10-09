package tui

// SettingsList: a two-column settings selector with value cycling.
//
// Mirrors upstream pi-tui's SettingsList (settings-list.ts). Layout:
//
//     filter: █
//
//   › Auto-compact            true
//     Steering mode           one-at-a-time
//     Follow-up mode          one-at-a-time
//     (1/13)
//
//     Automatically compact context when it gets too large
//
//     Type to search · Enter/Space to change · Esc to cancel
//
// The selected row is highlighted, values are right-aligned in a
// second column, the description of the selected item appears below
// the list, and a hint line is always shown at the bottom.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// SettingItem describes one toggle-able setting.
type SettingItem struct {
	ID           string   // unique identifier
	Label        string   // display label (left column)
	Description  string   // shown below list when selected
	CurrentValue string   // right column
	Values       []string // Enter/Space cycles through these
	// Submenu opens when Enter activates the item. done closes it: a non-nil selectedValue becomes the item's value, and options.NavigateTo moves the cursor to that item and activates it (settings-list.ts SettingItem.submenu).
	Submenu func(currentValue string, done func(selectedValue *string, options *SubmenuDoneOptions)) Component
}

// SubmenuDoneOptions is the optional second argument of a submenu's done callback.
type SubmenuDoneOptions struct {
	// NavigateTo is the id of the item to select and activate after the submenu closes. Empty means no navigation.
	NavigateTo string
}

// SettingsList is a two-column selector that cycles setting values.
// After Done()==true, callers read ChangedID/ChangedValue for the last
// change, or Cancelled() if the user pressed Esc. A host that keeps the list
// open, as upstream's onChange does, applies the change and calls Reset.
type SettingsList struct {
	invalidatable
	items    []SettingItem
	filtered []int // indices into items
	cursor   int

	maxVisible        int
	searchEnabled     bool
	searchInput       *TextInput
	mousePressedIndex int

	done      bool
	cancelled bool
	// Last change made (for the caller loop pattern).
	ChangedID    string
	ChangedValue string
	submenu      Component

	onChange func(id, newValue string)
	onCancel func()
	// customTheme is the injected theme; nil renders with the active theme.
	customTheme *SettingsListTheme
}

// SettingsListTheme styles a SettingsList (settings-list.ts SettingsListTheme). Cursor is the selected-row prefix.
type SettingsListTheme struct {
	Label       func(text string, selected bool) string
	Value       func(text string, selected bool) string
	Description func(text string) string
	Cursor      string
	Hint        func(text string) string
}

// GetSettingsListTheme is the list styling taken from the active theme (theme.ts getSettingsListTheme): the colors are
// read when each part is drawn, the cursor prefix when the theme is requested.
func GetSettingsListTheme() SettingsListTheme {
	return SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return ActiveTheme().Fg("accent", text)
			}
			return text
		},
		Value: func(text string, selected bool) string {
			if selected {
				return ActiveTheme().Fg("accent", text)
			}
			return ActiveTheme().Fg("muted", text)
		},
		Description: func(text string) string { return ActiveTheme().Fg("dim", text) },
		Cursor:      ActiveTheme().Fg("accent", "→ "),
		Hint:        func(text string) string { return ActiveTheme().Fg("dim", text) },
	}
}

// theme is the injected theme, or the active one.
func (s *SettingsList) theme() SettingsListTheme {
	if s.customTheme != nil {
		return *s.customTheme
	}
	return GetSettingsListTheme()
}

// SettingsListOptions is upstream's SettingsListOptions (settings-list.ts:34-36). Upstream's absent enableSearch is false, as is Go's zero.
type SettingsListOptions struct {
	EnableSearch bool
}

// NewSettingsList ports upstream's SettingsList constructor, `new SettingsList(items, maxVisible, theme, onChange, onCancel, options = {})`
// (settings-list.ts:55-73): the list is drawn with theme, onChange and onCancel are its callbacks, and search is off unless
// options.EnableSearch is true. Only the first options value is read, as upstream takes one. A nil callback keeps the caller-loop behaviour
// for that event: the list records the change in ChangedID/ChangedValue, or the cancel in Cancelled, for the host to read. onChange runs after
// the row shows the new value, including a value a submenu returns, and onCancel runs when Esc is pressed on the main list. maxVisible is
// clamped to at least 1.
func NewSettingsList(items []SettingItem, maxVisible int, theme SettingsListTheme, onChange func(id, newValue string), onCancel func(), options ...SettingsListOptions) *SettingsList {
	enableSearch := len(options) > 0 && options[0].EnableSearch
	s := &SettingsList{items: items, maxVisible: max(1, maxVisible), searchEnabled: enableSearch, mousePressedIndex: -1, customTheme: &theme, onChange: onChange, onCancel: onCancel}
	if enableSearch {
		s.searchInput = NewInput(InputOptions{})
	}
	s.applyFilter()
	return s
}

// Items returns a copy of the rows with their current values.
func (s *SettingsList) Items() []SettingItem { return slices.Clone(s.items) }

// SelectItem moves the selection to the item with the given id, as upstream's
// selectItem does; an id that is not displayed leaves the selection alone.
func (s *SettingsList) SelectItem(id string) {
	for position, index := range s.displayItems() {
		if s.items[index].ID == id {
			s.cursor = position
			s.Invalidate()
			return
		}
	}
}

// Done reports whether the user confirmed a change or cancelled.
func (s *SettingsList) Done() bool { return s.done }

// Cancelled reports whether the user pressed Esc.
func (s *SettingsList) Cancelled() bool { return s.cancelled }

// Reset clears done/cancelled so the list can be reused in a loop.
func (s *SettingsList) Reset() {
	s.done = false
	s.cancelled = false
	s.ChangedID = ""
	s.ChangedValue = ""
}

// UpdateValue sets a new current value for the item with the given ID.
func (s *SettingsList) UpdateValue(id, newValue string) {
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].CurrentValue = newValue
			break
		}
	}
}

// pig additive (D107): a component kit host keeps one list across theme switches and input,
// and reads and restores the list's state to report it; upstream has no accessors.

// SetTheme renders the list with theme from now on.
func (s *SettingsList) SetTheme(theme SettingsListTheme) {
	s.customTheme = &theme
	s.Invalidate()
}

// DisplayedItems returns the items passing the current search, in order.
func (s *SettingsList) DisplayedItems() []SettingItem {
	display := s.displayItems()
	items := make([]SettingItem, len(display))
	for i, index := range display {
		items[i] = s.items[index]
	}
	return items
}

// SelectedIndex returns the selection's index into DisplayedItems, -1 when
// nothing is selected.
func (s *SettingsList) SelectedIndex() int {
	if s.cursor < 0 || s.cursor >= len(s.displayItems()) {
		return -1
	}
	return s.cursor
}

// SetSelectedIndex selects DisplayedItems[index], clamped like
// SelectList.setSelectedIndex.
func (s *SettingsList) SetSelectedIndex(index int) {
	s.cursor = max(0, min(index, len(s.displayItems())-1))
	s.Invalidate()
}

// Query returns the search input value.
func (s *SettingsList) Query() string { return s.query() }

// SetQuery sets the search input value, its cursor at the end, and filters
// as typing it would; it does nothing when search is disabled.
func (s *SettingsList) SetQuery(query string) {
	if s.searchInput == nil {
		return
	}
	s.searchInput.SetValue(query)
	s.searchInput.cursor = jsstring.Length(s.searchInput.GetValue())
	s.applyFilter()
	s.Invalidate()
}

// SearchEnabled reports whether the list has a search input.
func (s *SettingsList) SearchEnabled() bool { return s.searchEnabled }

// SubmenuComponent returns the open submenu, nil when none is open.
func (s *SettingsList) SubmenuComponent() Component { return s.submenu }

// MaxVisible returns the number of item rows rendered at once.
func (s *SettingsList) MaxVisible() int { return s.maxVisible }

// Render draws the settings list. Matches upstream SettingsList.renderMainList.
func (s *SettingsList) Render(width int) []string {
	if s.submenu != nil {
		return s.submenu.Render(width)
	}
	lines := s.renderMainList(width)
	// pig divergence (D66): a row wider than the requested width is clipped to it; upstream emits the over-wide row (a wrapped description or the empty-state text in a very narrow pane).
	for i, line := range lines {
		if widthx.VisibleWidth(line) > width {
			lines[i] = widthx.TruncateToWidth(line, width, "", false)
		}
	}
	return lines
}

// renderMainList is upstream renderMainList (settings-list.ts): the search box, the visible rows, the scroll counter, the selected row's description and the hint.
func (s *SettingsList) renderMainList(width int) []string {
	var lines []string

	// Search input row: mirrors upstream Input component + Spacer(1).
	if s.searchEnabled {
		lines = append(lines, s.searchInput.Render(width)...)
		lines = append(lines, "")
	}

	display := s.displayItems()
	if len(s.items) == 0 {
		lines = append(lines, clipOverflow(s.theme().Hint("  No settings available"), width))
		if s.searchEnabled {
			lines = append(lines, s.hintLines(width)...)
		}
		return lines
	}
	if len(display) == 0 {
		lines = append(lines, clipOverflow(widthx.TruncateToWidth(s.theme().Hint("  No matching settings"), width, "...", false), width))
		return append(lines, s.hintLines(width)...)
	}

	// Calculate max label width for alignment.
	// Mirrors upstream: Math.min(36, Math.max(...items.map(visibleWidth(label))))
	maxLabelW := 0
	for _, item := range s.items {
		maxLabelW = max(maxLabelW, widthx.VisibleWidth(item.Label))
	}
	maxLabelW = min(maxLabelW, 36)

	// Visible window.
	start, end := s.visibleRange(display)

	const noCursor = "  "
	theme := s.theme()

	for i := start; i < end; i++ {
		item := s.items[display[i]]
		selected := i == s.cursor

		var prefix string
		if selected {
			prefix = theme.Cursor
		} else {
			prefix = noCursor
		}

		// Pad label to maxLabelW for column alignment.
		labelPad := max(maxLabelW-widthx.VisibleWidth(item.Label), 0)
		labelCol := item.Label + strings.Repeat(" ", labelPad)

		sep := "  "
		usedWidth := widthx.VisibleWidth(prefix) + maxLabelW + widthx.VisibleWidth(sep)
		valueMaxWidth := width - usedWidth - 2
		valueStr := widthx.TruncateToWidth(item.CurrentValue, valueMaxWidth, "", false)
		labelText := theme.Label(labelCol, selected)
		valueText := theme.Value(valueStr, selected)
		row := prefix + labelText + sep + valueText
		lines = append(lines, clipOverflow(widthx.TruncateToWidth(row, width, "...", false), width))
	}

	// Scroll indicator: mirrors upstream "(1/13)".
	if start > 0 || end < len(display) {
		scrollText := fmt.Sprintf("  (%d/%d)", s.cursor+1, len(display))
		lines = append(lines, clipOverflow(theme.Hint(widthx.TruncateToWidth(scrollText, width-2, "", false)), width))
	}

	// Description of selected item: mirrors upstream selectedItem.description.
	if s.cursor >= 0 && s.cursor < len(display) {
		sel := s.items[display[s.cursor]]
		if sel.Description != "" {
			lines = append(lines, "")
			wrapped := widthx.WrapTextWithAnsi(sel.Description, width-4)
			for _, wl := range wrapped {
				lines = append(lines, clipOverflow(theme.Description("  "+wl), width))
			}
		}
	}

	return append(lines, s.hintLines(width)...)
}

// hintLines mirrors upstream addHintLine.
func (s *SettingsList) hintLines(width int) []string {
	hint := "  Enter/Space to change · Esc to cancel"
	if s.searchEnabled {
		hint = "  Type to search · Enter/Space to change · Esc to cancel"
	}
	return []string{"", clipOverflow(widthx.TruncateToWidth(s.theme().Hint(hint), width, "...", false), width)}
}

// clipOverflow returns line as Pi draws it when it fits width. A line Pi
// would draw wider than the width (its TUI throws on one, at widths below
// the list's fixed text) is cut to the width instead.
func clipOverflow(line string, width int) string {
	if widthx.VisibleWidth(line) <= width {
		return line
	}
	return widthx.TruncateToWidth(line, max(width, 0), "", false)
}

// HandleMouse moves selection by wheel, selects a visible row on press, and
// activates the pressed row on click. Pointer motion does not change selection.
// Mirrors upstream SettingsList.handleMouse.
func (s *SettingsList) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if s.submenu != nil {
		// The submenu owns the pointer. The list keeps keyboard focus, because its HandleInput routes keys to the submenu and it stays mounted after the submenu closes. Like settings-list.ts handleMouse, the submenu's own result is returned rather than a dispatched one, so the list stays the mouse target of a plain result and any result focuses the list.
		handler, ok := s.submenu.(MouseHandler)
		if !ok {
			return nil
		}
		result := handler.HandleMouse(event)
		if result == nil {
			return nil
		}
		focused := *result
		focused.Focus = true
		return &focused
	}
	if s.searchEnabled {
		if event.Y == 0 {
			result := s.searchInput.HandleMouse(event)
			if result == nil {
				return nil
			}
			focused := *result
			focused.Focus = true
			return &focused
		}
		if event.Y == 1 {
			return nil
		}
	}
	display := s.displayItems()
	if len(display) == 0 {
		return nil
	}
	if event.Type == MouseWheel && event.WheelDelta != 0 {
		previous := s.cursor
		if event.WheelDelta < 0 {
			s.cursor--
		} else {
			s.cursor++
		}
		s.cursor = max(0, min(s.cursor, len(display)-1))
		changed := s.cursor != previous
		if changed {
			s.Invalidate()
		}
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Render: new(changed)}}
	}
	if event.Button != MouseButtonLeft || (event.Type != MousePress && event.Type != MouseClick) {
		return nil
	}
	rowOffset := 0
	if s.searchEnabled {
		rowOffset = 2
	}
	start, end := s.visibleRange(display)
	itemIndex := start + event.Y - rowOffset
	if itemIndex < start || itemIndex >= end {
		return nil
	}
	if event.Type == MousePress {
		s.mousePressedIndex = itemIndex
		s.cursor = itemIndex
		s.Invalidate()
		return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}}
	}
	if s.mousePressedIndex >= 0 {
		s.cursor = s.mousePressedIndex
	} else {
		s.cursor = itemIndex
	}
	s.mousePressedIndex = -1
	s.activateItem()
	s.Invalidate()
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true}}
}

func (s *SettingsList) visibleRange(display []int) (start, end int) {
	start = max(0, min(s.cursor-s.maxVisible/2, len(display)-s.maxVisible))
	end = min(start+s.maxVisible, len(display))
	return start, end
}

// HandleInput processes keystrokes.
func (s *SettingsList) HandleInput(data string) {
	if s.submenu != nil {
		if handler, ok := s.submenu.(InputHandler); ok {
			handler.HandleInput(data)
		}
		s.Invalidate()
		return
	}
	kb := GetTUIKeybindings()
	display := s.displayItems()
	switch {
	case kb.Matches(data, KBSelectUp):
		if len(display) > 0 {
			s.cursor = (s.cursor - 1 + len(display)) % len(display)
		}
	case kb.Matches(data, KBSelectDown):
		if len(display) > 0 {
			s.cursor = (s.cursor + 1) % len(display)
		}
	case kb.Matches(data, KBSelectConfirm) || (data == " " && (!s.searchEnabled || s.searchInput.GetValue() == "")):
		// Space changes the value unless it types into a non-empty search.
		s.activateItem()
	case kb.Matches(data, KBSelectCancel):
		if s.onCancel != nil {
			s.onCancel()
			break
		}
		s.cancelled = true
		s.done = true
	case s.searchEnabled:
		// Every other key belongs to the search Input, and the list refilters after it even when the value is unchanged.
		s.searchInput.HandleInput(data)
		s.applyFilter()
	}
	s.Invalidate()
}

func (s *SettingsList) activateItem() {
	display := s.displayItems()
	if s.cursor < 0 || s.cursor >= len(display) {
		return
	}
	item := &s.items[display[s.cursor]]
	if item.Submenu != nil {
		s.submenu = item.Submenu(item.CurrentValue, func(value *string, options *SubmenuDoneOptions) {
			s.submenu = nil
			if value != nil {
				item.CurrentValue = *value
				if s.onChange != nil {
					s.onChange(item.ID, *value)
				}
			}
			if options != nil && options.NavigateTo != "" {
				s.selectItem(options.NavigateTo)
				s.activateItem()
			}
			s.Invalidate()
		})
		return
	}
	if len(item.Values) == 0 {
		return
	}
	// Cycle to next value.
	cur := -1
	for i, v := range item.Values {
		if v == item.CurrentValue {
			cur = i
			break
		}
	}
	next := (cur + 1) % len(item.Values)
	item.CurrentValue = item.Values[next]
	if s.onChange != nil {
		s.onChange(item.ID, item.CurrentValue)
		return
	}
	s.ChangedID = item.ID
	s.ChangedValue = item.CurrentValue
	s.done = true
}

// selectItem moves the cursor to the displayed item with the given id and does nothing when it is not displayed (settings-list.ts selectItem).
func (s *SettingsList) selectItem(id string) {
	for position, index := range s.displayItems() {
		if s.items[index].ID == id {
			s.cursor = position
			return
		}
	}
}

func (s *SettingsList) displayItems() []int {
	return s.filtered
}

func (s *SettingsList) applyFilter() {
	s.filtered = s.filtered[:0]
	filtered := FuzzyFilter(s.items, s.query(), func(item SettingItem) string { return item.Label })
	for _, item := range filtered {
		for i := range s.items {
			if s.items[i].ID == item.ID {
				s.filtered = append(s.filtered, i)
				break
			}
		}
	}
	// Upstream applyFilter moves the selection to the first match.
	s.cursor = 0
}

// query is the search text; a list without search filters nothing.
func (s *SettingsList) query() string {
	if s.searchInput == nil {
		return ""
	}
	return s.searchInput.GetValue()
}
