package main

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1 config-selector.ts:22 calls initTheme(settingsManager.getTheme(), true). getTheme (settings-manager.ts:788-791) hides an automatic slash setting, so it and an unset theme select the environment theme (theme.ts:731-733,774-788). An explicitly empty or unknown name falls back to dark.
func TestConfigSelectorThemeMatchesPiInitTheme(t *testing.T) {
	previousRegistry, previousName := tui.ActiveThemeRegistry(), tui.ActiveTheme().Name
	t.Cleanup(func() {
		tui.SetThemeRegistry(previousRegistry)
		tui.SetThemeByName(previousName)
	})
	t.Setenv("COLORFGBG", "0;15")
	for _, tc := range []struct{ name, global, want string }{
		{"unset follows the environment", `{}`, "light"},
		{"automatic pair follows the environment, not the pair", `{"theme":"dark/dark"}`, "light"},
		{"empty name falls back to dark", `{"theme":""}`, "dark"},
		{"unknown name falls back to dark", `{"theme":"missing-theme"}`, "dark"},
		{"fixed name", `{"theme":"dark"}`, "dark"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui.SetThemeRegistry(tui.NewThemeRegistry())
			tui.SetTheme("light")
			cwd, agentDir := t.TempDir(), t.TempDir()
			writeStartupSettings(t, filepath.Join(agentDir, "settings.json"), tc.global)
			settings := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
			selector := tui.NewScopedConfigSelector(nil, nil, 0, "global", false)
			newConfigSelectorUI(selector, settings, agentDir)
			if got := tui.ActiveTheme().Name; got != tc.want {
				t.Fatalf("theme = %q, want %q", got, tc.want)
			}
		})
	}
}
