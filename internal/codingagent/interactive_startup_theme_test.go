package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// upstream 0.99.1 interactive-mode.ts:631-637 and theme-controller.ts constructor: Run registers the resource themes, marks the terminal colors pending, and applies the initial theme, so the system theme renders in grayscale until the terminal reports its colors.
func TestInteractiveStartupThemeStartsTheSystemThemeInGrayscale(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "")
	tui.SetTerminalColors(tui.TerminalColors{})
	cwd, agentDir := writeThemeLayers(t, `{}`, "")
	manager := NewSettingsManager(cwd, agentDir)
	m := NewInteractiveMode(InteractiveOptions{CWD: cwd, AgentDir: agentDir, Settings: manager.Get(), SettingsManager: manager})
	m.initStartupTheme()
	theme := tui.ActiveTheme()
	if theme.Name != tui.SystemThemeName {
		t.Fatalf("startup theme = %q, want %q", theme.Name, tui.SystemThemeName)
	}
	// system-theme.ts indexedColors: saturation 0 while pending leaves every token on the terminal default.
	if got := theme.Fg("accent"); got != "\x1b[39m" {
		t.Fatalf("pending system accent = %q, want the default foreground", got)
	}
	if active := m.themeState.activeThemeName.Load(); active == nil || *active != tui.SystemThemeName {
		t.Fatalf("active theme name = %v, want %q", active, tui.SystemThemeName)
	}
	tui.SetTerminalColors(tui.TerminalColors{})
	tui.SetThemeByName(tui.SystemThemeName)
	if got := tui.ActiveTheme().Fg("accent"); got == "\x1b[39m" {
		t.Fatalf("system accent after the terminal answered = %q, want a palette color", got)
	}
}

// Run applies a custom theme named by the settings: the resource themes are registered before the initial theme resolves.
func TestInteractiveStartupThemeResolvesACustomThemeAfterLoadingThemes(t *testing.T) {
	restoreStartupTheme(t)
	cwd, agentDir := writeThemeLayers(t, `{"theme":"aurora"}`, "")
	dark, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	custom := strings.Replace(string(dark), `"name": "dark"`, `"name": "aurora"`, 1)
	if custom == string(dark) {
		t.Fatal("dark theme fixture has no name field")
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "themes", "aurora.json"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewSettingsManager(cwd, agentDir)
	m := NewInteractiveMode(InteractiveOptions{CWD: cwd, AgentDir: agentDir, Settings: manager.Get(), SettingsManager: manager})
	m.initStartupTheme()
	if got := tui.ActiveTheme().Name; got != "aurora" {
		t.Fatalf("startup theme = %q, want aurora", got)
	}
	if active := m.themeState.activeThemeName.Load(); active == nil || *active != "aurora" {
		t.Fatalf("active theme name = %v, want aurora", active)
	}
}
