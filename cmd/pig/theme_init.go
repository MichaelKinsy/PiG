package main

import (
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// initTheme is theme.ts initTheme(settingsManager.getTheme()) for a process without an interactive theme controller.
// getTheme hides an automatic slash setting (settings-manager.ts:854-857), and an unset setting names the system theme.
// No theme is registered yet, so loadTheme resolves the name as loadThemeJson does (theme.ts:551-571, 631-640): the system theme, then a built-in theme, then `<name>.json` in the agent's themes directory by file name, not by the theme's JSON name.
// A built-in name therefore ignores a custom file of the same name, and any load failure selects the system theme (theme.ts:756-768).
func initTheme(settings *codingagent.SettingsManager, agentDir string) {
	name := tui.SystemThemeName
	if theme := settings.GetThemeSetting(); theme != nil && !strings.Contains(*theme, "/") {
		name = *theme
	}
	switch name {
	case tui.SystemThemeName, "dark", "light":
		tui.SetTheme(name)
		return
	}
	path := filepath.Join(agentDir, "themes", name+".json")
	theme, err := tui.LoadThemeFile(path)
	if err != nil {
		tui.SetTheme(tui.SystemThemeName)
		return
	}
	// The registry keys a theme by its JSON name; Pi's global theme is this file's theme whatever that name is.
	tui.ActiveThemeRegistry().AddFile(theme, path)
	tui.SetThemeByName(theme.Name)
}
