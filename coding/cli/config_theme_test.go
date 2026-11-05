package cli

import (
	"os"
	"path/filepath"
	"slices"
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

// Pi config-selector.ts:25-30 builds the UI with settingsManager.getShowHardwareCursor() and applies getClearOnShrink().
func TestConfigSelectorUIAppliesTheCursorAndClearOnShrinkSettings(t *testing.T) {
	previousRegistry, previousName := tui.ActiveThemeRegistry(), tui.ActiveTheme().Name
	t.Cleanup(func() {
		tui.SetThemeRegistry(previousRegistry)
		tui.SetThemeByName(previousName)
	})
	t.Setenv("PI_HARDWARE_CURSOR", "")
	for _, enabled := range []bool{true, false} {
		cwd, agentDir := t.TempDir(), t.TempDir()
		value := "false"
		if enabled {
			value = "true"
		}
		writeStartupSettings(t, filepath.Join(agentDir, "settings.json"), `{"showHardwareCursor":`+value+`,"terminal":{"clearOnShrink":`+value+`}}`)
		settings := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
		ui := newConfigSelectorUI(tui.NewScopedConfigSelector(nil, nil, 0, "global", false), settings, agentDir)
		if ui.GetShowHardwareCursor() != enabled || ui.GetClearOnShrink() != settings.GetClearOnShrink() || settings.GetClearOnShrink() != enabled {
			t.Fatalf("settings %s: cursor=%v clearOnShrink=%v (setting %v)", value, ui.GetShowHardwareCursor(), ui.GetClearOnShrink(), settings.GetClearOnShrink())
		}
	}
}

// Ports packages/coding-agent/test/config-selector-keybindings.test.ts (Pi b0c64f33c): selectConfig installs KeybindingsManager.create(agentDir),
// so the selector resolves the user's keybindings.json remaps.
func TestConfigSelectorUIUsesTheUsersKeybindings(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	previousRegistry, previousName := tui.ActiveThemeRegistry(), tui.ActiveTheme().Name
	t.Cleanup(func() {
		tui.SetThemeRegistry(previousRegistry)
		tui.SetThemeByName(previousName)
	})
	cwd, agentDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"), []byte(`{"tui.select.up":["up","ctrl+p"],"tui.select.down":["down","ctrl+n"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Cleanup(closeThemeWatcher)
	tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil))
	newConfigSelectorUI(tui.NewScopedConfigSelector(nil, nil, 0, "global", false), settings, agentDir)
	kb := tui.GetTUIKeybindings()
	if got := kb.GetKeys(tui.KBSelectDown); !slices.Contains(got, "ctrl+n") {
		t.Errorf("tui.select.down keys = %v, want ctrl+n among them", got)
	}
	if got := kb.GetKeys(tui.KBSelectUp); !slices.Contains(got, "ctrl+p") {
		t.Errorf("tui.select.up keys = %v, want ctrl+p among them", got)
	}
}
