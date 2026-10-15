package tui

import "strings"

// SelectItem mirrors upstream pi-tui's SelectItem shape for submenu-style
// selectors (value + label + optional description).
type SelectItem struct {
	Value       string
	Label       string
	Description string
}

// SelectSubmenuOptions enables fuzzy search and overrides the primary-column layout.
type SelectSubmenuOptions struct {
	Searchable            bool
	MinPrimaryColumnWidth int
	MaxPrimaryColumnWidth int
}

// SelectSubmenuComponent shows a titled select list with optional fuzzy search.
type SelectSubmenuComponent struct {
	Container
	items       []SelectItem
	allOptions  []SelectItem
	options     SelectSubmenuOptions
	searchInput *TextInput
	list        *FilterableList
	listTitle   string
	// description is the text under the title; a frontend session shows it.
	description string
	// OnSelectionChange runs with the highlighted value after every up or down key that reaches a non-empty list, even when the
	// highlight lands on the same item, as upstream's SelectList onSelectionChange does.
	OnSelectionChange func(value string)
}

func NewSelectSubmenu(title, description string, items []SelectItem, currentValue string, options ...SelectSubmenuOptions) *SelectSubmenuComponent {
	s := &SelectSubmenuComponent{allOptions: items, listTitle: title, description: description}
	if len(options) > 0 {
		s.options = options[0]
	}
	if s.options.Searchable {
		s.searchInput = NewInput(InputOptions{})
		s.searchInput.OnSubmit = func(string) { s.list.HandleInput("\r") }
	}
	// upstream: settings-submenu.ts SelectSubmenu constructor children.
	th := ActiveTheme()
	s.Add(NewPaddedText(th.Bold(th.Fg("accent", title)), 0, 0, nil))
	if description != "" {
		s.Add(NewSpacer(1))
		s.Add(NewPaddedText(th.Fg("muted", description), 0, 0, nil))
	}
	if s.searchInput != nil {
		s.Add(NewSpacer(1))
		s.Add(s.searchInput)
	}
	s.Add(NewSpacer(1))
	s.buildSelectList(title, items, currentValue)
	s.Add(s.list)
	s.Add(NewSpacer(1))
	hint := "  Enter to select · Esc to go back"
	if s.searchInput != nil {
		hint = "  Type to filter · Enter to select · Esc to go back"
	}
	s.Add(NewPaddedText(th.Fg("dim", hint), 0, 0, nil))
	return s
}

func (s *SelectSubmenuComponent) buildSelectList(title string, items []SelectItem, currentValue string) {
	labels := make([]string, len(items))
	descs := make([]string, len(items))
	currentIdx := 0
	for i, item := range items {
		labels[i] = item.Label
		descs[i] = item.Description
		if item.Value == currentValue {
			currentIdx = i
		}
	}
	list := NewFilterableList(title, labels)
	list.EnableSearch = false
	list.Descriptions = descs
	list.MinPrimaryColumnWidth = 12 // upstream: coding-agent/src/modes/interactive/components/thinking-selector.ts:minPrimaryColumnWidth
	list.MaxPrimaryColumnWidth = 32 // upstream: coding-agent/src/modes/interactive/components/thinking-selector.ts:maxPrimaryColumnWidth
	if s.options.MinPrimaryColumnWidth != 0 {
		list.MinPrimaryColumnWidth = s.options.MinPrimaryColumnWidth
	}
	if s.options.MaxPrimaryColumnWidth != 0 {
		list.MaxPrimaryColumnWidth = s.options.MaxPrimaryColumnWidth
	}
	list.MaxVisible = min(len(items), 10)
	list.SetCursor(currentIdx)
	s.items = items
	if previous := s.list; previous != nil {
		s.Replace(previous, list)
	}
	s.list = list
}

func (s *SelectSubmenuComponent) HandleInput(data string) {
	// A parent Container reuses this component's lines until it is invalidated.
	defer s.Invalidate()
	kb := GetTUIKeybindings()
	if s.searchInput == nil || kb.Matches(data, KBSelectUp) || kb.Matches(data, KBSelectDown) || kb.Matches(data, KBSelectConfirm) || kb.Matches(data, KBSelectCancel) {
		s.list.HandleInput(data)
		if s.OnSelectionChange != nil && (kb.Matches(data, KBSelectUp) || kb.Matches(data, KBSelectDown)) {
			if value := s.CurrentValue(); value != "" {
				s.OnSelectionChange(value)
			}
		}
		return
	}
	s.searchInput.HandleInput(data)
	items := FuzzyFilter(s.allOptions, s.searchInput.Text(), func(item SelectItem) string { return item.Label + " " + item.Description })
	s.buildSelectList(s.listTitle, items, "")
}
func (s *SelectSubmenuComponent) Done() bool      { return s.list.Done() }
func (s *SelectSubmenuComponent) Cancelled() bool { return s.list.Cancelled() }
func (s *SelectSubmenuComponent) CurrentValue() string {
	if s.list.cursor < 0 || s.list.cursor >= len(s.list.filtered) {
		return ""
	}
	idx := s.list.filtered[s.list.cursor]
	if idx < 0 || idx >= len(s.items) {
		return ""
	}
	return s.items[idx].Value
}
func (s *SelectSubmenuComponent) SelectedValue() string {
	idx := s.list.SelectedIndex()
	if idx < 0 || idx >= len(s.items) {
		return ""
	}
	return s.items[idx].Value
}

func (s *SelectSubmenuComponent) String() string {
	return strings.Join(s.Render(80), "\n")
}
