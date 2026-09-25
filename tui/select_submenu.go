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
	invalidatable
	title       string
	description string
	items       []SelectItem
	allOptions  []SelectItem
	options     SelectSubmenuOptions
	searchInput *TextInput
	list        *FilterableList
}

func NewSelectSubmenu(title, description string, items []SelectItem, currentValue string, options ...SelectSubmenuOptions) *SelectSubmenuComponent {
	s := &SelectSubmenuComponent{title: title, description: description, allOptions: items}
	if len(options) > 0 {
		s.options = options[0]
	}
	if s.options.Searchable {
		s.searchInput = NewInput(InputOptions{})
		s.searchInput.OnSubmit = func(string) { s.list.HandleInput("\r") }
	}
	s.buildSelectList(items, currentValue)
	return s
}

func (s *SelectSubmenuComponent) buildSelectList(items []SelectItem, currentValue string) {
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
	list := NewFilterableList(s.title, labels)
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
	s.list = list
}

func (s *SelectSubmenuComponent) Render(width int) []string {
	th := ActiveTheme()
	muted := th.Muted
	if muted == "" {
		muted = "\x1b[38;2;128;128;128m"
	}
	dim := th.Dim
	if dim == "" {
		dim = "\x1b[38;2;102;102;102m"
	}
	accent := th.Accent
	if accent == "" {
		accent = "\x1b[38;2;138;190;183m"
	}
	// Upstream settings-submenu.ts renders the title, description and hint as
	// Text(..., 0, 0), so each wraps to the render width.
	text := func(content string) []string { return NewPaddedText(content, 0, 0, nil).Render(width) }
	lines := text(accent + "\x1b[1m" + s.title + th.Reset)
	if s.description != "" {
		lines = append(lines, "")
		lines = append(lines, text(muted+s.description+th.Reset)...)
	}
	if s.searchInput != nil {
		lines = append(lines, "")
		lines = append(lines, s.searchInput.Render(width)...)
	}
	lines = append(lines, "")
	lines = append(lines, s.list.Render(width)...)
	lines = append(lines, "")
	hint := "  Enter to select · Esc to go back"
	if s.searchInput != nil {
		hint = "  Type to filter · Enter to select · Esc to go back"
	}
	lines = append(lines, text(dim+hint+th.Reset)...)
	return lines
}

func (s *SelectSubmenuComponent) HandleInput(data string) {
	kb := GetTUIKeybindings()
	if s.searchInput == nil || kb.Matches(data, KBSelectUp) || kb.Matches(data, KBSelectDown) || kb.Matches(data, KBSelectConfirm) || kb.Matches(data, KBSelectCancel) {
		s.list.HandleInput(data)
		return
	}
	s.searchInput.HandleInput(data)
	items := FuzzyFilter(s.allOptions, s.searchInput.Text(), func(item SelectItem) string { return item.Label + " " + item.Description })
	s.buildSelectList(items, "")
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
