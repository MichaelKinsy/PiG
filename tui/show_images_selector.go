package tui

// showImagesSelectListLayout is SHOW_IMAGES_SELECT_LIST_LAYOUT (show-images-selector.ts:5-8).
var showImagesSelectListLayout = SelectListLayoutOptions{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 32}

// ShowImagesSelectorComponent renders a yes/no selector for image display: a Container of border, SelectList, border
// (show-images-selector.ts:14-52).
type ShowImagesSelectorComponent struct {
	Container
	selectList *SelectList
}

// NewShowImagesSelectorComponent creates the selector. currentValue pre-selects the matching entry. Confirming an item
// invokes onSelect with whether it is "yes"; the cancel key invokes onCancel. Each callback may be nil.
func NewShowImagesSelectorComponent(currentValue bool, onSelect func(show bool), onCancel func()) *ShowImagesSelectorComponent {
	items := []SelectItem{
		{Value: "yes", Label: "Yes", Description: "Show images inline in terminal"},
		{Value: "no", Label: "No", Description: "Show text placeholder instead"},
	}
	c := &ShowImagesSelectorComponent{}
	c.Add(NewDynamicBorder())
	c.selectList = NewSelectList(items, 5, GetSelectListTheme(), showImagesSelectListLayout)
	if currentValue {
		c.selectList.SetSelectedIndex(0)
	} else {
		c.selectList.SetSelectedIndex(1)
	}
	c.selectList.OnSelect = func(item SelectItem) {
		if onSelect != nil {
			onSelect(item.Value == "yes")
		}
	}
	c.selectList.OnCancel = func() {
		if onCancel != nil {
			onCancel()
		}
	}
	c.Add(c.selectList)
	c.Add(NewDynamicBorder())
	return c
}

// GetSelectList returns the underlying SelectList (show-images-selector.ts getSelectList).
func (s *ShowImagesSelectorComponent) GetSelectList() *SelectList { return s.selectList }

// HandleInput delegates to the list. A parent Container reuses this component's lines until it is invalidated, so a list change must invalidate it.
func (s *ShowImagesSelectorComponent) HandleInput(data string) {
	s.selectList.HandleInput(data)
	s.Invalidate()
}

// Render draws the container and clips a row wider than width, as ThemeSelectorComponent.Render does for its SelectList.
func (s *ShowImagesSelectorComponent) Render(width int) []string {
	return clipSelectListRows(s.Container.Render(width), width)
}
