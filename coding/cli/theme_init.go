package cli

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// themeWatcher is the process-wide theme watcher theme.ts keeps in a module variable (startThemeWatcher). initTheme replaces it.
var themeWatcher *tui.ThemeWatcher

// configuredThemeName is settingsManager.getTheme(): the theme setting, with an automatic slash setting hidden (settings-manager.ts:854-857).
// An unset setting is the empty name, which initTheme resolves to the system theme as `themeName ?? SYSTEM_THEME_NAME` does.
func configuredThemeName(settings *codingagent.SettingsManager) string {
	if theme := settings.GetThemeSetting(); theme != nil && !strings.Contains(*theme, "/") {
		return *theme
	}
	return ""
}

// initTheme is theme.ts:756 initTheme(themeName?, enableWatcher = false) for a process without an interactive theme controller.
// An empty name is the system theme (`themeName ?? SYSTEM_THEME_NAME`; an explicitly empty name fails to load and falls back to the same theme).
// No theme is registered yet, so loadTheme resolves the name as loadThemeJson does (theme.ts:551-571, 631-640): the system theme, then a built-in theme, then `<name>.json` in the agent's themes directory by file name, not by the theme's JSON name.
// A built-in name therefore ignores a custom file of the same name, and any load failure selects the system theme without starting the watcher (theme.ts:756-770).
// With enableWatcher the loaded theme's directory is watched for edits (theme.ts startThemeWatcher), as the config selector does (config-selector.ts:22).
func initTheme(themeName string, enableWatcher bool) {
	agentDir := codingagent.AgentDir()
	name := themeName
	if name == "" {
		name = tui.SystemThemeName
	}
	switch name {
	case tui.SystemThemeName, "dark", "light":
		tui.SetTheme(name)
	default:
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
	if enableWatcher {
		closeThemeWatcher()
		themeWatcher = tui.StartThemeWatcher(context.Background(), filepath.Join(agentDir, "themes"), nil)
	}
}

// closeThemeWatcher stops the theme watcher initTheme started.
func closeThemeWatcher() {
	if themeWatcher != nil {
		themeWatcher.Close()
		themeWatcher = nil
	}
}
