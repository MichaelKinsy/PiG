package tui

import (
	"strings"
	"testing"
)

// restoreBakedThemeState restores the process-global theme state a test changes.
func restoreBakedThemeState(t *testing.T) {
	t.Helper()
	previousName := ActiveTheme().Name
	t.Cleanup(func() {
		SetTerminalColors(TerminalColors{})
		SetThemeByName(previousName)
	})
}

// reportTerminalColors is what a prompt does when the terminal answers its color query: record the colors, then
// re-resolve the theme (startup-ui.ts startStartupTui).
func reportTerminalColors() {
	black, white := RgbColor{}, RgbColor{R: 255, G: 255, B: 255}
	SetTerminalColors(TerminalColors{Foreground: &black, Background: &white})
	SetThemeByName(SystemThemeName)
}

// Pi 0.99.2 ExtensionSelectorComponent builds its title Text (extension-selector.ts:48), its description, its hint
// and the option rows (updateList, :75-85) with `theme.fg(...)` when it is constructed, and a row again only when
// the selection moves; a theme change afterwards leaves the title and the rows as they were built. The border
// (DynamicBorder) is drawn with the theme of the moment. A startup prompt is constructed while the system theme
// is grayscale and keeps that look after the terminal's colors arrive (parity 10-startup-trust-prompt-wording).
func TestExtensionSelectorKeepsTheThemeItWasBuiltUnder(t *testing.T) {
	restoreBakedThemeState(t)
	MarkTerminalColorsPending()
	SetThemeByName(SystemThemeName)
	selector := NewExtensionSelector("Trust project folder?", []string{"Trust", "Do not trust"})
	reportTerminalColors()

	lines := selector.Render(80)
	if !strings.Contains(lines[0], "\x1b[38;") {
		t.Fatalf("the border is drawn with the live theme, got %q", lines[0])
	}
	for _, line := range lines[1 : len(lines)-1] {
		if strings.Contains(line, "\x1b[38;") {
			t.Fatalf("a row built under the grayscale theme took the later colors: %q", line)
		}
	}

	// Moving the selection rebuilds the rows with the theme of that moment (updateList); the title keeps its construction-time text.
	selector.HandleInput("j")
	title, selected := "", ""
	for _, line := range selector.Render(80) {
		switch {
		case strings.Contains(line, "Trust project folder?"):
			title = line
		case strings.Contains(line, "→"):
			selected = line
		}
	}
	if strings.Contains(title, "\x1b[38;") {
		t.Fatalf("the title was rebuilt: %q", title)
	}
	if !strings.Contains(selected, "\x1b[38;") {
		t.Fatalf("the selected row was not rebuilt with the live theme: %q", selected)
	}
}

// Pi 0.99.2 ExtensionInputComponent builds its title and hint Text at construction (extension-input.ts:46-76).
func TestExtensionInputKeepsTheThemeItWasBuiltUnder(t *testing.T) {
	restoreBakedThemeState(t)
	MarkTerminalColorsPending()
	SetThemeByName(SystemThemeName)
	input := NewExtensionInputComponent("Name", "")
	reportTerminalColors()
	for _, line := range input.Render(80)[1:] {
		if strings.Contains(line, "Name") && strings.Contains(line, "\x1b[38;") {
			t.Fatalf("the title built under the grayscale theme took the later colors: %q", line)
		}
		if strings.Contains(line, "submit") && strings.Contains(line, "\x1b[38;") {
			t.Fatalf("the hint built under the grayscale theme took the later colors: %q", line)
		}
	}
}
