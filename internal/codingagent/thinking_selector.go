package codingagent

import (
	"github.com/MichaelKinsy/PiG/tui"
)

// ThinkingSelectorComponent renders the /thinking selector: a search input over
// the available levels, the current level marked with a check, the saved
// default annotated, and app.thinking.save to save the highlighted level as the
// default. Ports upstream components/thinking-selector.ts.
type ThinkingSelectorComponent struct {
	*tui.Container
	searchInput       *tui.TextInput
	selectList        *tui.FilterableList
	allItems          []tui.SelectItem
	items             []tui.SelectItem
	onSelect          func(level string)
	onCancel          func()
	onSelectAsDefault func(level string)
}

// NewThinkingSelectorComponent builds the selector. onSelectAsDefault may be
// nil, which disables the save binding; defaultThinkingLevel annotates that
// level's description.
func NewThinkingSelectorComponent(
	currentLevel string,
	availableLevels []string,
	onSelect func(level string),
	onCancel func(),
	onSelectAsDefault func(level string),
	defaultThinkingLevel string,
) *ThinkingSelectorComponent {
	s := &ThinkingSelectorComponent{
		Container:         tui.NewContainer(),
		searchInput:       tui.NewTextInput(""),
		onSelect:          onSelect,
		onCancel:          onCancel,
		onSelectAsDefault: onSelectAsDefault,
	}
	for _, level := range availableLevels {
		label := "  " + level
		if level == currentLevel {
			label = "✓ " + level
		}
		description := thinkingDescriptions[level]
		if level == defaultThinkingLevel {
			description += " · default"
		}
		s.allItems = append(s.allItems, tui.SelectItem{Value: level, Label: label, Description: description})
	}

	th := tui.ActiveTheme()
	s.Add(tui.NewDynamicBorder(""))
	s.Add(tui.NewSpacer(1))
	s.Add(tui.NewText("Thinking Level"))
	s.Add(tui.NewSpacer(1))
	s.Add(tui.NewText(tui.ActionKeyDisplayText("app.thinking.cycle") + " cycles thinking levels in-session"))
	s.Add(tui.NewSpacer(1))
	s.Add(s.searchInput)
	s.Add(tui.NewSpacer(1))
	s.selectList = s.buildSelectList(s.allItems, currentLevel)
	s.Add(s.selectList)
	s.Add(tui.NewSpacer(1))
	s.Add(tui.NewText(th.Dim + "  " + tui.ActionKeyDisplayText(tui.KBSelectConfirm) + " to select · " +
		tui.ActionKeyDisplayText("app.thinking.save") + " to set as default · " +
		tui.ActionKeyDisplayText(tui.KBSelectCancel) + " to cancel" + th.Reset))
	s.Add(tui.NewDynamicBorder(""))
	return s
}

// thinkingSelectListLayout mirrors THINKING_SELECT_LIST_LAYOUT.
const (
	thinkingSelectMinPrimaryColumnWidth = 12
	thinkingSelectMaxPrimaryColumnWidth = 32
)

func (s *ThinkingSelectorComponent) buildSelectList(items []tui.SelectItem, preselect string) *tui.FilterableList {
	labels := make([]string, len(items))
	descriptions := make([]string, len(items))
	currentIndex := -1
	for i, item := range items {
		labels[i] = item.Label
		descriptions[i] = item.Description
		if item.Value == preselect {
			currentIndex = i
		}
	}
	list := tui.NewFilterableList("", labels)
	list.EnableSearch = false
	list.Descriptions = descriptions
	list.MinPrimaryColumnWidth = thinkingSelectMinPrimaryColumnWidth
	list.MaxPrimaryColumnWidth = thinkingSelectMaxPrimaryColumnWidth
	list.MaxVisible = max(1, len(items))
	if currentIndex != -1 {
		list.SetCursor(currentIndex)
	}
	s.items = items
	return list
}

// selectedItem returns the highlighted item, as SelectList.getSelectedItem.
func (s *ThinkingSelectorComponent) selectedItem() (tui.SelectItem, bool) {
	index := s.selectList.CursorIndex()
	if index < 0 || index >= len(s.items) {
		return tui.SelectItem{}, false
	}
	return s.items[index], true
}

func (s *ThinkingSelectorComponent) applyFilter(query string) {
	filtered := s.allItems
	if query != "" {
		filtered = tui.FuzzyFilter(s.allItems, query, func(item tui.SelectItem) string {
			return item.Value + " " + item.Description
		})
	}
	selectedValue := ""
	if item, ok := s.selectedItem(); ok {
		selectedValue = item.Value
	}
	previous := s.selectList
	s.selectList = s.buildSelectList(filtered, selectedValue)
	s.Replace(previous, s.selectList)
}

// HandleInput routes a key: the save binding saves the highlighted level as
// the default, navigation keys drive the list, and everything else edits the
// search query.
func (s *ThinkingSelectorComponent) HandleInput(data string) {
	kb := tui.GetTUIKeybindings()
	if kb.Matches(data, "app.thinking.save") && s.onSelectAsDefault != nil {
		if item, ok := s.selectedItem(); ok {
			s.onSelectAsDefault(item.Value)
		}
		return
	}

	if kb.Matches(data, tui.KBSelectUp) || kb.Matches(data, tui.KBSelectDown) ||
		kb.Matches(data, tui.KBSelectConfirm) || kb.Matches(data, tui.KBSelectCancel) {
		s.selectList.HandleInput(data)
		s.dispatchListResult()
		s.Invalidate()
		return
	}

	s.searchInput.HandleInput(data)
	s.applyFilter(s.searchInput.Text())
	s.Invalidate()
}

// dispatchListResult turns the list's confirm or cancel into the upstream
// onSelect/onCancel callbacks.
func (s *ThinkingSelectorComponent) dispatchListResult() {
	if !s.selectList.Done() {
		return
	}
	if s.selectList.Cancelled() {
		if s.onCancel != nil {
			s.onCancel()
		}
		return
	}
	index := s.selectList.SelectedIndex()
	if index >= 0 && index < len(s.items) && s.onSelect != nil {
		s.onSelect(s.items[index].Value)
	}
}

// GetSelectList returns the level list, as upstream getSelectList.
func (s *ThinkingSelectorComponent) GetSelectList() *tui.FilterableList {
	return s.selectList
}
