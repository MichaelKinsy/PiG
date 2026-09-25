package tui

// dynamic_border.go: horizontal rule border component.
//
// Ports upstream dynamic-border.ts (25 LOC).

// DynamicBorder renders a full-width horizontal rule using "─".
type DynamicBorder struct {
	invalidatable
	color string // ANSI fg escape; empty = use theme border color
}

// NewDynamicBorder creates a border. If color is empty, the active
// theme's border color is used at render time.
func NewDynamicBorder(color string) *DynamicBorder {
	return &DynamicBorder{color: color}
}

// Render produces a single line of "─" repeated to fill width.
func (d *DynamicBorder) Render(width int) []string {
	color := d.color
	if color == "" {
		color = ActiveTheme().Border
	}
	w := max(1, width)
	line := color + repeatRune('─', w) + "\x1b[0m"
	return []string{line}
}

// repeatRune repeats a rune n times.
func repeatRune(r rune, n int) string {
	b := make([]byte, 0, n*3) // "─" is 3 bytes UTF-8
	for range n {
		b = append(b, string(r)...)
	}
	return string(b)
}
