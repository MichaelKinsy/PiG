package tui

// dynamic_border.go: horizontal rule border component.
//
// Ports packages/coding-agent/src/modes/interactive/components/dynamic-border.ts.

// DynamicBorder renders a full-width horizontal rule using "─".
type DynamicBorder struct {
	invalidatable
	color func(string) string
}

// NewDynamicBorder creates a border drawn through color, which styles the whole rule. Without a color function the rule uses the active theme's border color, read at render time.
func NewDynamicBorder(color ...func(string) string) *DynamicBorder {
	if len(color) > 0 && color[0] != nil {
		return &DynamicBorder{color: color[0]}
	}
	return &DynamicBorder{color: func(text string) string { return ActiveTheme().Fg("border", text) }}
}

// NewDynamicBorderToken creates a border colored with a theme token at render time (dynamic-border.ts takes a `(str) => theme.fg(token, str)` callback), so it follows theme changes.
func NewDynamicBorderToken(token string) *DynamicBorder {
	return NewDynamicBorder(func(text string) string { return ActiveTheme().Fg(token, text) })
}

// Render produces a full-width rule styled by the border's color function.
func (d *DynamicBorder) Render(width int) []string {
	return []string{d.color(repeatRune('─', max(1, width)))}
}

// repeatRune repeats a rune n times.
func repeatRune(r rune, n int) string {
	b := make([]byte, 0, n*3) // "─" is 3 bytes UTF-8
	for range n {
		b = append(b, string(r)...)
	}
	return string(b)
}
