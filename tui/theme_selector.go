package tui

// theme_selector.go: theme picker component.
//
// Ports upstream theme-selector.ts (67 LOC).

// ThemeSelectorComponent renders a theme picker with live preview.
type ThemeSelectorComponent struct {
	invalidatable
	list      *FilterableList
	onSelect  func(themeName string)
	onPreview func(themeName string)
	themes    []string
}

// NewThemeSelector creates a theme selector. currentTheme is
// pre-selected; onPreview fires on cursor movement for live preview.
func NewThemeSelector(
	currentTheme string,
	onSelect func(string),
	onCancel func(),
	onPreview func(string),
) *ThemeSelectorComponent {
	reg := ActiveThemeRegistry()
	themes := reg.Names()

	labels := make([]string, len(themes))
	for i, name := range themes {
		label := name
		if name == currentTheme {
			label += " (current)"
		}
		labels[i] = label
	}

	list := NewFilterableList("Theme", labels)

	// Pre-select current theme.
	for i, name := range themes {
		if name == currentTheme {
			list.SetCursor(i)
			break
		}
	}

	return &ThemeSelectorComponent{
		list:      list,
		onSelect:  onSelect,
		onPreview: onPreview,
		themes:    themes,
	}
}

// List returns the underlying FilterableList for input handling.
func (ts *ThemeSelectorComponent) List() *FilterableList { return ts.list }

// Render wraps the list with borders.
func (ts *ThemeSelectorComponent) Render(width int) []string {
	border := NewDynamicBorder("")
	var lines []string
	lines = append(lines, border.Render(width)...)
	lines = append(lines, ts.list.Render(width)...)
	lines = append(lines, border.Render(width)...)
	return lines
}

// HandleInput delegates to the list.
func (ts *ThemeSelectorComponent) HandleInput(data string) {
	ts.list.HandleInput(data)
}
