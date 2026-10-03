package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/resource-loader-theme.test.ts (upstream 0.99.1). PiG's theme resources load in InteractiveMode.loadThemes, the counterpart of DefaultResourceLoader.loadThemes (resource-loader.ts:881-890).

func resourceLoaderThemeFixture(t *testing.T) (agentDir, cwd, themePath string) {
	t.Helper()
	root := t.TempDir()
	agentDir, cwd = filepath.Join(root, "agent"), filepath.Join(root, "project")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dark, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(dark, &theme); err != nil {
		t.Fatal(err)
	}
	theme["name"] = "capability-test"
	theme["colors"].(map[string]any)["userMessageBg"] = "#3c3544"
	encoded, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	themePath = filepath.Join(root, "capability-test.json")
	if err := os.WriteFile(themePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tui.SetCapabilityOverrides(tui.CapabilityOverrides{}); tui.ResetCapabilitiesCache() })
	return agentDir, cwd, themePath
}

func resourceLoaderThemeBg(t *testing.T, m *InteractiveMode) string {
	t.Helper()
	for _, loaded := range m.loadedThemes {
		if loaded.theme.Name == "capability-test" {
			return loaded.theme.BgText("userMessageBg", "x")
		}
	}
	t.Fatalf("theme capability-test not loaded: %+v %+v", m.loadedThemes, m.themeDiagnostics)
	return ""
}

func TestDefaultResourceLoaderThemeColorModeUpstream(t *testing.T) {
	// resource-loader-theme.test.ts:39 (regression test for #9973).
	for _, tc := range []struct {
		name, environmentOverride, setting, expected string
	}{
		{"uses the true setting over a 256-color environment", "0", "true", "\x1b[48;2;60;53;68mx\x1b[49m"},
		{"uses the false setting over a truecolor environment", "1", "false", "\x1b[48;5;59mx\x1b[49m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir, cwd, themePath := resourceLoaderThemeFixture(t)
			t.Setenv("PI_TRUE_COLOR", tc.environmentOverride)
			tui.SetCapabilityOverrides(tui.CapabilityOverrides{})
			tui.ResetCapabilitiesCache()
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"terminal":{"trueColor":`+tc.setting+`}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			manager := NewSettingsManager(cwd, agentDir)
			m := &InteractiveMode{opts: InteractiveOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: manager, Settings: manager.Get(), ThemePaths: []string{themePath}}}
			m.loadThemes()
			if got := resourceLoaderThemeBg(t, m); got != tc.expected {
				t.Errorf("userMessageBg = %q, want %q", got, tc.expected)
			}
		})
	}

	// resource-loader-theme.test.ts:76. DefaultResourceLoader.reload() reloads the settings first (resource-loader.ts reload); PiG's reload path does the same before loadThemes, so the case reloads the manager explicitly.
	t.Run("returns to automatic detection after an explicit setting is removed", func(t *testing.T) {
		agentDir, cwd, themePath := resourceLoaderThemeFixture(t)
		t.Setenv("PI_TRUE_COLOR", "1")
		tui.SetCapabilityOverrides(tui.CapabilityOverrides{})
		tui.ResetCapabilitiesCache()

		settingsPath := filepath.Join(agentDir, "settings.json")
		if err := os.WriteFile(settingsPath, []byte(`{"terminal":{"trueColor":false}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		manager := NewSettingsManager(cwd, agentDir)
		m := &InteractiveMode{opts: InteractiveOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: manager, Settings: manager.Get(), ThemePaths: []string{themePath}}}
		m.loadThemes()
		tui.SetCapabilityOverrides(manager.Get().GetTerminalCapabilityOverrides())

		if err := os.WriteFile(settingsPath, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		manager.Reload()
		m.opts.Settings = manager.Get()
		m.loadThemes()

		if got := resourceLoaderThemeBg(t, m); got != "\x1b[48;2;60;53;68mx\x1b[49m" {
			t.Errorf("userMessageBg = %q, want the truecolor form", got)
		}
	})
}
