package cli

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.99.1 config-selector.ts:22 calls initTheme(settingsManager.getTheme(), true). getTheme (settings-manager.ts:854-857) hides an automatic slash setting, so it and an unset theme select the system theme (theme.ts initTheme: themeName ?? SYSTEM_THEME_NAME); an explicitly empty or unknown name fails to load and falls back to the system theme. COLORFGBG no longer selects a built-in theme. Node on the pinned 0.99.1 dist: initTheme over these settings gives ["system","system","system","system","dark"].
func TestConfigSelectorThemeMatchesPiInitTheme(t *testing.T) {
	previousRegistry, previousName := tui.ActiveThemeRegistry(), tui.ActiveTheme().Name
	t.Cleanup(func() {
		tui.SetThemeRegistry(previousRegistry)
		tui.SetThemeByName(previousName)
	})
	t.Setenv("COLORFGBG", "0;15")
	for _, tc := range []struct{ name, global, want string }{
		{"unset selects the system theme", `{}`, "system"},
		{"automatic pair selects the system theme, not the pair", `{"theme":"dark/dark"}`, "system"},
		{"empty name falls back to the system theme", `{"theme":""}`, "system"},
		{"unknown name falls back to the system theme", `{"theme":"missing-theme"}`, "system"},
		{"fixed name", `{"theme":"dark"}`, "dark"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui.SetThemeRegistry(tui.NewThemeRegistry())
			tui.SetTheme("light")
			cwd, agentDir := t.TempDir(), t.TempDir()
			writeStartupSettings(t, filepath.Join(agentDir, "settings.json"), tc.global)
			settings := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
			selector := tui.NewScopedConfigSelector(nil, nil, 0, "global", false)
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Setenv("PI_CODING_AGENT_DIR", agentDir)
			t.Cleanup(closeThemeWatcher)
			newConfigSelectorUI(selector, settings, agentDir)
			if got := tui.ActiveTheme().Name; got != tc.want {
				t.Fatalf("theme = %q, want %q", got, tc.want)
			}
		})
	}
}
