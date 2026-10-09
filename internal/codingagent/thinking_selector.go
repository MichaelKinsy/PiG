package codingagent

import (
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// ThinkingSelectorComponent renders the /thinking selector: a search input over
// the available levels, the current level marked with a check, the saved
// default annotated, and app.thinking.save to save the highlighted level as the
// default. Ports upstream components/thinking-selector.ts.
type ThinkingSelectorComponent struct {
	*tui.Container
	searchInput       *tui.TextInput
	selectList        *tui.SelectList
	allItems          []tui.SelectItem
	onSelect          func(level ai.ThinkingLevel)
	onCancel          func()
	onSelectAsDefault func(level ai.ThinkingLevel)
}

// NewThinkingSelectorComponent builds the selector. onSelectAsDefault may be
// nil, which disables the save binding; defaultThinkingLevel annotates that
// level's description.
func NewThinkingSelectorComponent(
	currentLevel ai.ThinkingLevel,
	availableLevels []ai.ThinkingLevel,
	onSelect func(level ai.ThinkingLevel),
	onCancel func(),
	onSelectAsDefault func(level ai.ThinkingLevel),
	defaultThinkingLevel ai.ThinkingLevel,
) *ThinkingSelectorComponent {
	s := &ThinkingSelectorComponent{
		Container:         tui.NewContainer(),
		searchInput:       tui.NewInput(tui.InputOptions{}),
		onSelect:          onSelect,
		onCancel:          onCancel,
		onSelectAsDefault: onSelectAsDefault,
	}
	s.searchInput.Focused = true
	s.searchInput.OnSubmit = func(string) { s.selectList.HandleInput("\r") }
	for _, level := range availableLevels {
		label := "  " + string(level)
		if level == currentLevel {
			label = "✓ " + string(level)
		}
		description := thinkingDescriptions[string(level)]
		if level == defaultThinkingLevel {
			description += " · default"
		}
		s.allItems = append(s.allItems, tui.SelectItem{Value: string(level), Label: label, Description: description})
	}

	th := tui.ActiveTheme()
	s.Add(tui.NewDynamicBorder())
	s.Add(tui.NewSpacer(1))
	s.Add(tui.NewText("Thinking Level"))
	s.Add(tui.NewSpacer(1))
	s.Add(tui.NewText(tui.ActionKeyDisplayText("app.thinking.cycle") + " cycles thinking levels in-session"))
	s.Add(tui.NewSpacer(1))
	s.Add(s.searchInput)
	s.Add(tui.NewSpacer(1))
	s.selectList = s.buildSelectList(s.allItems, string(currentLevel))
	s.Add(s.selectList)
	s.Add(tui.NewSpacer(1))
	s.Add(tui.NewText(th.Fg("dim", "  "+tui.ActionKeyDisplayText(tui.KBSelectConfirm)+" to select · "+
		tui.ActionKeyDisplayText("app.thinking.save")+" to set as default · "+
		tui.ActionKeyDisplayText(tui.KBSelectCancel)+" to cancel")))
	s.Add(tui.NewDynamicBorder())
	return s
}

// thinkingSelectListLayout is THINKING_SELECT_LIST_LAYOUT (thinking-selector.ts:17-20).
var thinkingSelectListLayout = tui.SelectListLayoutOptions{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 32}

func (s *ThinkingSelectorComponent) buildSelectList(items []tui.SelectItem, preselect string) *tui.SelectList {
	list := tui.NewSelectList(items, max(1, len(items)), tui.GetSelectListTheme(), thinkingSelectListLayout)
	if index := slices.IndexFunc(items, func(item tui.SelectItem) bool { return item.Value == preselect }); index != -1 {
		list.SetSelectedIndex(index)
	}
	list.OnSelect = func(item tui.SelectItem) {
		if s.onSelect != nil {
			s.onSelect(ai.ThinkingLevel(item.Value))
		}
	}
	list.OnCancel = func() {
		if s.onCancel != nil {
			s.onCancel()
		}
	}
	return list
}

func (s *ThinkingSelectorComponent) applyFilter(query string) {
	filtered := s.allItems
	if query != "" {
		filtered = tui.FuzzyFilter(s.allItems, query, func(item tui.SelectItem) string {
			return item.Value + " " + item.Description
		})
	}
	selectedValue := ""
	if item, ok := s.selectList.SelectedItem(); ok {
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
		if item, ok := s.selectList.SelectedItem(); ok {
			s.onSelectAsDefault(ai.ThinkingLevel(item.Value))
		}
		return
	}

	if kb.Matches(data, tui.KBSelectUp) || kb.Matches(data, tui.KBSelectDown) ||
		kb.Matches(data, tui.KBSelectConfirm) || kb.Matches(data, tui.KBSelectCancel) {
		s.selectList.HandleInput(data)
		s.Invalidate()
		return
	}

	s.searchInput.HandleInput(data)
	s.applyFilter(s.searchInput.Text())
	s.Invalidate()
}

// GetSelectList returns the level list, as upstream getSelectList.
func (s *ThinkingSelectorComponent) GetSelectList() *tui.SelectList {
	return s.selectList
}

// SetFocused propagates TUI focus to the search input (upstream `set focused`, thinking-selector.ts:50-53).
func (s *ThinkingSelectorComponent) SetFocused(focused bool) { s.searchInput.SetFocused(focused) }

// Focused reports whether the search input holds the TUI focus (upstream `get focused`).
func (s *ThinkingSelectorComponent) Focused() bool { return s.searchInput.Focused }
