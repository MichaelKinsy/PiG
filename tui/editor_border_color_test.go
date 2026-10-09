package tui

import "testing"

// interactive-mode.ts updateEditorBorderColor sets editor.borderColor to theme.getBashModeBorderColor() in bash mode and otherwise to
// theme.getThinkingBorderColor(level); the Editor paints each border row with that function (editor.ts:512,517), so a border row is exactly
// the theme's Fg output for the token, including its closing sequence.
// Pi: packages/coding-agent/src/modes/interactive/theme/theme.ts:412 (Theme.getThinkingBorderColor); packages/coding-agent/src/modes/interactive/theme/theme.ts:434 (Theme.getBashModeBorderColor).
func TestEditorBorderRowsAreTheThemesBorderColorFunction(t *testing.T) {
	theme := ActiveTheme()
	row := "──────────"
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh"} {
		e := NewEditor()
		e.SetText("x")
		e.ThinkingLevel = level
		if got, want := e.Render(10)[0], theme.GetThinkingBorderColor(level)(row); got != want {
			t.Errorf("thinking %q border = %q, want %q", level, got, want)
		}
	}
	bash := NewEditor()
	bash.SetText("!ls")
	if got, want := bash.Render(10)[0], theme.GetBashModeBorderColor()(row); got != want {
		t.Errorf("bash border = %q, want %q", got, want)
	}
	plain := NewEditor()
	plain.SetText("x")
	if got, want := plain.Render(10)[0], theme.Fg("borderMuted", row); got != want {
		t.Errorf("default border = %q, want %q", got, want)
	}
}
