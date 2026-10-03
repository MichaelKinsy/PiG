package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// writeCustomTheme writes the built-in dark theme as `<agentDir>/themes/<file>.json` with the JSON name and accent given.
func writeCustomTheme(t *testing.T, agentDir, file, name, accent string) {
	t.Helper()
	darkJSON, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(darkJSON, &theme); err != nil {
		t.Fatal(err)
	}
	theme["name"] = name
	theme["colors"].(map[string]any)["accent"] = accent
	data, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "themes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "themes", file+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Pi 0.99.2 main.ts:898 `initTheme(settingsManager.getTheme(), appMode === "interactive")` and theme.ts:551-571, 631-640, 755-769: with no registered theme, the configured name is the system theme, a built-in theme, or `<name>.json` in the agent's themes directory by file name. No setting, an automatic slash setting, or an unloadable name select the system theme. A custom file named like a built-in theme does not replace it, and a theme's JSON name does not select its file.
func TestInitThemeAppliesTheConfiguredTheme(t *testing.T) {
	builtinDarkAccent := func() string {
		tui.SetTheme("dark")
		return tui.ActiveTheme().Colors()["accent"]
	}()
	for _, tc := range []struct {
		name       string
		setting    *string
		wantName   string
		wantAccent string
	}{
		{"unset", nil, tui.SystemThemeName, ""},
		{"built-in light", new("light"), "light", ""},
		{"built-in dark", new("dark"), "dark", builtinDarkAccent},
		{"automatic slash setting", new("light/dark"), tui.SystemThemeName, ""},
		{"unknown name", new("missing"), tui.SystemThemeName, ""},
		{"empty name", new(""), tui.SystemThemeName, ""},
		{"custom theme in the agent directory", new("mine"), "mine", "#102030"},
		{"custom theme by file name, not JSON name", new("file-name"), "json-name", "#123456"},
		{"a JSON name does not select its file", new("json-name"), tui.SystemThemeName, ""},
		{"a custom file does not replace a built-in theme", new("dark"), "dark", builtinDarkAccent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			writeCustomTheme(t, agentDir, "mine", "mine", "#102030")
			writeCustomTheme(t, agentDir, "file-name", "json-name", "#123456")
			writeCustomTheme(t, agentDir, "dark", "dark", "#abcdef")
			t.Cleanup(func() { tui.SetThemeRegistry(nil); tui.SetTheme("dark") })
			tui.SetThemeRegistry(nil)
			tui.SetTheme("dark")
			settings := codingagent.NewInMemorySettingsManager(codingagent.Settings{})
			if tc.setting != nil {
				if err := settings.SetTheme(*tc.setting); err != nil {
					t.Fatal(err)
				}
			}
			initTheme(settings, agentDir)
			if got := tui.ActiveTheme().Name; got != tc.wantName {
				t.Fatalf("active theme = %q, want %q", got, tc.wantName)
			}
			if tc.wantAccent != "" {
				if got := tui.ActiveTheme().Colors()["accent"]; got != tc.wantAccent {
					t.Fatalf("active theme accent = %q, want %q", got, tc.wantAccent)
				}
			}
		})
	}
}
