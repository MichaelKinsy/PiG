package tui

// ShowImagesSelectorComponent renders a yes/no selector for image display.
type ShowImagesSelectorComponent struct {
	invalidatable
	list *FilterableList
}

// NewShowImagesSelector creates the selector. currentValue pre-selects
// the matching entry. Confirm invokes onSelect; cancellation invokes onCancel.
func NewShowImagesSelector(currentValue bool, onSelect func(bool), onCancel func()) *ShowImagesSelectorComponent {
	list := NewFilterableList("Show Images", []string{"Yes", "No"})
	list.EnableSearch = false
	list.Descriptions = []string{"Show images inline in terminal", "Show text placeholder instead"}
	list.MinPrimaryColumnWidth = 12 // upstream: packages/coding-agent/src/modes/interactive/components/show-images-selector.ts:SHOW_IMAGES_SELECT_LIST_LAYOUT
	list.MaxPrimaryColumnWidth = 32
	list.MaxVisible = 5
	list.onSelect = func(index int) {
		if onSelect != nil {
			onSelect(index == 0)
		}
	}
	list.onCancel = onCancel
	if currentValue {
		list.SetCursor(0)
	} else {
		list.SetCursor(1)
	}

	return &ShowImagesSelectorComponent{list: list}
}

// List returns the underlying FilterableList for input handling.
func (s *ShowImagesSelectorComponent) List() *FilterableList { return s.list }

// Render wraps the list with dynamic borders.
func (s *ShowImagesSelectorComponent) Render(width int) []string {
	border := NewDynamicBorder("")
	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, s.list.Render(width)...)
	lines = append(lines, border.Render(width)...)
	return lines
}

// HandleInput delegates to the list.
func (s *ShowImagesSelectorComponent) HandleInput(data string) {
	s.list.HandleInput(data)
}

// SelectedShowImages returns the boolean result after selection.
// Returns (value, true) if a selection was made, (false, false) otherwise.
func (s *ShowImagesSelectorComponent) SelectedShowImages() (bool, bool) {
	if !s.list.Done() {
		return false, false
	}
	if s.list.Cancelled() {
		return false, false
	}
	return s.list.SelectedIndex() == 0, true
}
