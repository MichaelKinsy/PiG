package tui

// dynamic_border.go: horizontal rule border component.
//
// Ports packages/coding-agent/src/modes/interactive/components/dynamic-border.ts.

// DynamicBorder renders a full-width horizontal rule using "─".
type DynamicBorder struct {
	invalidatable
	color string // ANSI fg escape; empty = use theme border color
	token string // theme token resolved at render time; takes precedence over color
}

// NewDynamicBorder creates a border. If color is empty, the active
// theme's border color is used at render time.
func NewDynamicBorder(color string) *DynamicBorder {
	return &DynamicBorder{color: color}
}

// NewDynamicBorderToken creates a border colored with a theme token at render time (dynamic-border.ts takes a `(str) => theme.fg(token, str)` callback), so it follows theme changes.
func NewDynamicBorderToken(token string) *DynamicBorder {
	return &DynamicBorder{token: token}
}

// Render produces a full-width rule and resets only its foreground color.
func (d *DynamicBorder) Render(width int) []string {
	if d.token != "" {
		return []string{ActiveTheme().FgText(d.token, repeatRune('─', max(1, width)))}
	}
	color := d.color
	if color == "" {
		return []string{ActiveTheme().FgText("border", repeatRune('─', max(1, width)))}
	}
	w := max(1, width)
	line := color + repeatRune('─', w) + "\x1b[39m"
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
