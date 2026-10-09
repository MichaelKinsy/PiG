package tui

// pi: packages/coding-agent/src/modes/interactive/components/dynamic-border.ts

import "testing"

// Upstream dynamic-border.ts render(width) is [color("─".repeat(Math.max(1, width)))]; the default color is theme.fg("border").
func TestDynamicBorderRenderRepeatsTheRuleAtLeastOnce(t *testing.T) {
	for _, tc := range []struct{ width, cells int }{{10, 10}, {1, 1}, {0, 1}, {-4, 1}} {
		lines := NewDynamicBorder(func(text string) string { return "\x1b[31m" + text + "\x1b[39m" }).Render(tc.width)
		want := "\x1b[31m" + repeatRune('─', tc.cells) + "\x1b[39m"
		if len(lines) != 1 || lines[0] != want {
			t.Errorf("width %d: %q, want [%q]", tc.width, lines, want)
		}
	}
	themed := NewDynamicBorder().Render(5)
	if len(themed) != 1 || themed[0] != ActiveTheme().Fg("border", "─────") {
		t.Errorf("default color: %q", themed)
	}
	token := NewDynamicBorderToken("accent").Render(3)
	if len(token) != 1 || token[0] != ActiveTheme().Fg("accent", "───") {
		t.Errorf("token color: %q", token)
	}
}
