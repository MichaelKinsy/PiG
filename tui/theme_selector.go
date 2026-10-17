package tui

import (
	"slices"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// theme_selector.go: theme picker component.
//
// Ports upstream theme-selector.ts (67 LOC).

// themeSelectListLayout is THEME_SELECT_LIST_LAYOUT (theme-selector.ts:5-8).
var themeSelectListLayout = SelectListLayoutOptions{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 32}

// ThemeSelectorComponent renders a theme picker with live preview: a Container of border, SelectList, border
// (theme-selector.ts:14-67).
type ThemeSelectorComponent struct {
	Container
	selectList *SelectList
}

// NewThemeSelectorComponent creates a theme selector over the available themes. currentTheme is pre-selected and
// marked "(current)"; onPreview runs when the selection moves. Each callback may be nil.
func NewThemeSelectorComponent(currentTheme string, onSelect func(themeName string), onCancel func(), onPreview func(themeName string)) *ThemeSelectorComponent {
	themes := ActiveThemeRegistry().Names()
	items := make([]SelectItem, len(themes))
	currentIndex := -1
	for i, name := range themes {
		items[i] = SelectItem{Value: name, Label: name}
		if name == currentTheme {
			items[i].Description = "(current)"
			if currentIndex < 0 {
				currentIndex = i
			}
		}
	}

	ts := &ThemeSelectorComponent{}
	ts.Add(NewDynamicBorder())
	ts.selectList = NewSelectList(items, 10, GetSelectListTheme(), themeSelectListLayout)
	if currentIndex >= 0 {
		ts.selectList.SetSelectedIndex(currentIndex)
	}
	ts.selectList.OnSelect = func(item SelectItem) {
		if onSelect != nil {
			onSelect(item.Value)
		}
	}
	ts.selectList.OnCancel = func() {
		if onCancel != nil {
			onCancel()
		}
	}
	ts.selectList.OnSelectionChange = func(item SelectItem) {
		if onPreview != nil {
			onPreview(item.Value)
		}
	}
	ts.Add(ts.selectList)
	ts.Add(NewDynamicBorder())
	return ts
}

// GetSelectList returns the underlying SelectList (theme-selector.ts getSelectList).
func (ts *ThemeSelectorComponent) GetSelectList() *SelectList { return ts.selectList }

// HandleInput delegates to the list. A parent Container reuses this component's lines until it is invalidated, so a list change must invalidate it.
func (ts *ThemeSelectorComponent) HandleInput(data string) {
	ts.selectList.HandleInput(data)
	ts.Invalidate()
}

// Render draws the container and clips a row wider than width. SelectList draws its two-cell row prefix at any width, so
// at a width of one it would overflow, which upstream's TUI treats as fatal ("Rendered line N exceeds terminal width").
func (ts *ThemeSelectorComponent) Render(width int) []string {
	return clipSelectListRows(ts.Container.Render(width), width)
}

// clipSelectListRows truncates each row of a SelectList-based selector to width.
func clipSelectListRows(rendered []string, width int) []string {
	lines := slices.Clone(rendered)
	for i, line := range lines {
		if widthx.VisibleWidth(line) > width {
			lines[i] = widthx.TruncateToWidth(line, width, "", false)
		}
	}
	return lines
}
