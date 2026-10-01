package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func settingsFirstHalfEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func settingsFirstHalfOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func settingsFirstHalfMemory(t *testing.T, raw string) *SettingsManager {
	t.Helper()
	var settings Settings
	settingsFirstHalfOK(t, json.Unmarshal([]byte(raw), &settings))
	return NewInMemorySettingsManager(settings)
}

func settingsFirstHalfFileJSON(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	settingsFirstHalfOK(t, err)
	var gotValue, wantValue any
	settingsFirstHalfOK(t, json.Unmarshal(data, &gotValue))
	settingsFirstHalfOK(t, json.Unmarshal([]byte(want), &wantValue))
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("settings = %s, want %s", data, want)
	}
}

func TestSettingsManagerFirstHalfExternalChanges(t *testing.T) {
	t.Setenv("PIG_USE_PI_DIRS", "1") // D2 shared-directory mode preserves the original .pi fixtures.
	for _, tc := range []struct {
		name, initial, external, want string
		change                        func(*SettingsManager) error
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:29
		{"should preserve enabledModels when changing thinking level", `{"theme":"dark","defaultModel":"claude-sonnet"}`, `{"theme":"dark","defaultModel":"claude-sonnet","enabledModels":["claude-opus-4-5","gpt-5.2-codex"]}`, `{"theme":"dark","defaultModel":"claude-sonnet","enabledModels":["claude-opus-4-5","gpt-5.2-codex"],"defaultThinkingLevel":"high"}`, func(sm *SettingsManager) error { return sm.SetDefaultThinkingLevel("high") }},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:60
		{"should preserve custom settings when changing theme", `{"defaultModel":"claude-sonnet"}`, `{"defaultModel":"claude-sonnet","shellPath":"/bin/zsh","extensions":["/path/to/extension.ts"]}`, `{"defaultModel":"claude-sonnet","shellPath":"/bin/zsh","extensions":["/path/to/extension.ts"],"theme":"light"}`, func(sm *SettingsManager) error { return sm.SetTheme("light") }},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:88
		{"should let in-memory changes override file changes for same key", `{"theme":"dark"}`, `{"theme":"dark","defaultThinkingLevel":"low"}`, `{"theme":"dark","defaultThinkingLevel":"high"}`, func(sm *SettingsManager) error { return sm.SetDefaultThinkingLevel("high") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := writeSettingsLayers(t, tc.initial, `{}`)
			writeSettingsFixture(t, sm.GlobalPath(), tc.external)
			settingsFirstHalfOK(t, tc.change(sm))
			settingsFirstHalfOK(t, sm.Flush())
			settingsFirstHalfFileJSON(t, sm.GlobalPath(), tc.want)
		})
	}
}

func TestSettingsManagerFirstHalfStorage(t *testing.T) {
	t.Setenv("PIG_USE_PI_DIRS", "1") // D2 shared-directory mode preserves the original .pi fixtures.
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:132
	t.Run("should keep local-only extensions in extensions array", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"extensions":["/local/ext.ts","./relative/ext.ts"]}`, `{}`)
		settingsFirstHalfEqual(t, sm.GetPackages(), []PackageSource{})
		settingsFirstHalfEqual(t, sm.GetExtensionPaths(), []string{"/local/ext.ts", "./relative/ext.ts"})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:147
	t.Run("should handle packages with filtering objects", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"packages":["npm:simple-pkg",{"source":"npm:shitty-extensions","extensions":["extensions/oracle.ts"],"skills":[]}]}`, `{}`)
		settingsFirstHalfEqual(t, sm.GetPackages(), []PackageSource{{Source: "npm:simple-pkg"}, {Source: "npm:shitty-extensions", Extensions: []string{"extensions/oracle.ts"}, Skills: []string{}, WasObject: true}})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:177
	t.Run("should reload global settings from disk", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"dark","extensions":["/before.ts"]}`, `{}`)
		writeSettingsFixture(t, sm.GlobalPath(), `{"theme":"light","extensions":["/after.ts"],"defaultModel":"claude-sonnet"}`)
		sm.Reload()
		settingsFirstHalfEqual(t, sm.GetTheme(), "light")
		settingsFirstHalfEqual(t, sm.GetExtensionPaths(), []string{"/after.ts"})
		settingsFirstHalfEqual(t, sm.GetDefaultModel(), "claude-sonnet")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:205
	t.Run("should keep previous settings and report the file path when the file is invalid", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"dark"}`, `{}`)
		writeSettingsFixture(t, sm.GlobalPath(), `{ invalid json`)
		sm.Reload()
		settingsFirstHalfEqual(t, sm.GetTheme(), "dark")
		errs := sm.DrainErrors()
		if len(errs) != 1 || errs[0].Scope != "global" || errs[0].Path != sm.GlobalPath() {
			t.Fatalf("errors=%v", errs)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:238
	t.Run("should collect and clear load errors via drainErrors", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{ invalid global json`, `{ invalid project json`)
		errs := sm.DrainErrors()
		if len(errs) != 2 {
			t.Fatalf("errors=%v", errs)
		}
		settingsFirstHalfEqual(t, errs[0].Scope, "global")
		settingsFirstHalfEqual(t, errs[0].Path, sm.GlobalPath())
		settingsFirstHalfEqual(t, errs[1].Scope, "project")
		settingsFirstHalfEqual(t, errs[1].Path, filepath.Join(ProjectConfigDir(sm.CWD()), "settings.json"))
		settingsFirstHalfEqual(t, sm.DrainErrors(), []SettingsError{})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:257
	t.Run("should skip project settings when project is not trusted", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"global"}`, `{"theme":"project"}`)
		sm = NewSettingsManagerWithProjectTrust(sm.CWD(), sm.AgentDir(), false)
		settingsFirstHalfEqual(t, sm.IsProjectTrusted(), false)
		settingsFirstHalfEqual(t, sm.GetTheme(), "global")
		settingsFirstHalfEqual(t, sm.GetProjectSettings(), Settings{})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:268
	t.Run("should reload project settings after trust changes to true", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"global"}`, `{"theme":"project"}`)
		sm = NewSettingsManagerWithProjectTrust(sm.CWD(), sm.AgentDir(), false)
		sm.SetProjectTrusted(true)
		settingsFirstHalfEqual(t, sm.IsProjectTrusted(), true)
		settingsFirstHalfEqual(t, sm.GetTheme(), "project")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:279
	t.Run("should fail project settings writes when project is not trusted", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{}`, `{"packages":["npm:existing"]}`)
		sm = NewSettingsManagerWithProjectTrust(sm.CWD(), sm.AgentDir(), false)
		err := sm.SetProjectPackages([]PackageSource{{Source: "npm:new"}})
		if err == nil || !strings.Contains(err.Error(), "Project is not trusted; refusing to write project settings") {
			t.Fatalf("write error=%v", err)
		}
		settingsFirstHalfOK(t, sm.Flush())
		settingsFirstHalfEqual(t, sm.GetProjectSettings(), Settings{})
		settingsFirstHalfFileJSON(t, filepath.Join(ProjectConfigDir(sm.CWD()), "settings.json"), `{"packages":["npm:existing"]}`)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:293
	t.Run("should read default project trust from global settings only", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"defaultProjectTrust":"always"}`, `{"defaultProjectTrust":"never"}`)
		settingsFirstHalfEqual(t, sm.GetDefaultProjectTrust(), "always")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:302
	t.Run("should default invalid project trust settings to ask", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"defaultProjectTrust":"sometimes"}`, `{}`)
		settingsFirstHalfEqual(t, sm.GetDefaultProjectTrust(), "ask")
	})
	for _, tc := range []struct {
		name  string
		write bool
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:312
		{"should not create .pi folder when only reading project settings", false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:330
		{"should create .pi folder when writing project settings", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, dir := t.TempDir(), t.TempDir()
			writeSettingsFixture(t, filepath.Join(dir, "settings.json"), `{"theme":"dark"}`)
			sm := NewSettingsManager(cwd, dir)
			if _, err := os.Stat(ProjectConfigDir(cwd)); !os.IsNotExist(err) {
				t.Fatalf("reading created project dir: %v", err)
			}
			settingsFirstHalfEqual(t, sm.GetTheme(), "dark")
			if tc.write {
				settingsFirstHalfOK(t, sm.SetProjectPackages([]PackageSource{{Source: "npm:test-pkg", WasObject: true}}))
				settingsFirstHalfOK(t, sm.Flush())
				_, err := os.Stat(filepath.Join(ProjectConfigDir(cwd), "settings.json"))
				settingsFirstHalfOK(t, err)
			}
		})
	}
}

func TestSettingsManagerFirstHalfValues(t *testing.T) {
	t.Setenv("PIG_USE_PI_DIRS", "1") // D2 shared-directory mode preserves the original .pi fixtures.
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:356
	t.Run("maps explicit values and omits auto values", func(t *testing.T) {
		no, yes := false, true
		disabled, kitty := tui.ImageProtocol(""), tui.ImageProtocol("kitty")
		for _, tc := range []struct {
			input string
			want  tui.CapabilityOverrides
		}{
			{`{"terminal":{"images":false,"trueColor":false,"hyperlinks":false}}`, tui.CapabilityOverrides{Images: &disabled, TrueColor: &no, Hyperlinks: &no}},
			{`{"terminal":{"images":"kitty","trueColor":true,"hyperlinks":true}}`, tui.CapabilityOverrides{Images: &kitty, TrueColor: &yes, Hyperlinks: &yes}},
			{`{"terminal":{"images":"auto","trueColor":"auto","hyperlinks":"auto"}}`, tui.CapabilityOverrides{}},
		} {
			settingsFirstHalfEqual(t, settingsFirstHalfMemory(t, tc.input).GetTerminalCapabilityOverrides(), tc.want)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:375
	t.Run("defaults and overrides agent retry delay cap", func(t *testing.T) {
		settingsFirstHalfEqual(t, settingsFirstHalfMemory(t, `{}`).GetRetrySettings(), RetryConfig{Enabled: true, MaxRetries: 3, BaseDelayMs: 2000, MaxDelayMs: 60000})
		settingsFirstHalfEqual(t, settingsFirstHalfMemory(t, `{"retry":{"enabled":true,"maxRetries":10,"baseDelayMs":500,"maxAgentDelayMs":5000}}`).GetRetrySettings(), RetryConfig{Enabled: true, MaxRetries: 10, BaseDelayMs: 500, MaxDelayMs: 5000})
	})
	for _, tc := range []struct {
		name, g, p string
		want       int
		invalid    bool
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:391
		{"should default to 5 minutes", `{}`, `{}`, ai.DefaultHTTPIdleTimeoutMs, false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:396
		{"should use merged global and project settings", `{"httpIdleTimeoutMs":300000}`, `{"httpIdleTimeoutMs":0}`, 0, false},
		// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:405
		{"should reject invalid timeout values", `{"httpIdleTimeoutMs":-1}`, `{}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := writeSettingsLayers(t, tc.g, tc.p)
			got, err := sm.GetHttpIdleTimeoutMs()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "Invalid httpIdleTimeoutMs setting") {
					t.Fatalf("timeout error=%v", err)
				}
			} else {
				settingsFirstHalfOK(t, err)
				settingsFirstHalfEqual(t, got, tc.want)
			}
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:414
	t.Run("defaults to streaming and ignores project settings", func(t *testing.T) {
		for _, tc := range []struct{ g, p, want string }{{`{}`, `{}`, "streaming"}, {`{}`, `{"cacheWarming":"idle"}`, "streaming"}, {`{"cacheWarming":"idle"}`, `{"cacheWarming":"idle"}`, "idle"}, {`{"cacheWarming":"bogus"}`, `{"cacheWarming":"idle"}`, "streaming"}} {
			settingsFirstHalfEqual(t, string(writeSettingsLayers(t, tc.g, tc.p).GetCacheWarmingMode()), tc.want)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:427
	t.Run("persists the mode globally", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{}`, `{}`)
		settingsFirstHalfOK(t, sm.SetCacheWarmingMode("off"))
		settingsFirstHalfOK(t, sm.Flush())
		settingsFirstHalfEqual(t, string(NewSettingsManager(sm.CWD(), sm.AgentDir()).GetCacheWarmingMode()), "off")
		settingsFirstHalfFileJSON(t, sm.GlobalPath(), `{"cacheWarming":"off"}`)
	})
}

func TestSettingsManagerFirstHalfTheme(t *testing.T) {
	t.Setenv("PIG_USE_PI_DIRS", "1") // D2 shared-directory mode preserves the original .pi fixtures.
	// .upstream/v0.99.1/packages/coding-agent/test/settings-manager.test.ts:220
	t.Run("stores slash-separated automatic theme settings separately from fixed theme names", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"light/dark"}`, `{}`)
		if got := sm.GetTheme(); got != "" {
			t.Fatalf("fixed theme=%q, want undefined", got)
		}
		setting := sm.GetThemeSetting()
		if setting == nil {
			t.Fatal("theme setting is omitted")
		}
		if *setting != "light/dark" {
			t.Fatalf("theme setting=%q", *setting)
		}
		if err := sm.SetTheme("solarized-light/tokyo-night"); err != nil {
			t.Fatal(err)
		}
		if err := sm.Flush(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(sm.AgentDir(), "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		var saved map[string]string
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		if len(saved) != 1 || saved["theme"] != "solarized-light/tokyo-night" {
			t.Fatalf("settings=%s", data)
		}
	})
}

func TestSettingsManagerFirstHalfContract(t *testing.T) {
	t.Chdir(t.TempDir())
	sm := settingsFirstHalfMemory(t, `{"theme":"light/dark","extensions":[],"defaultProjectTrust":"always"}`)
	settingsFirstHalfOK(t, sm.SetDefaultThinkingLevel("high"))
	settingsFirstHalfOK(t, sm.SetCacheWarmingMode("off"))
	settingsFirstHalfOK(t, sm.Flush())
	sm.ApplyOverrides(Settings{Theme: "transient"})
	sm.Reload()
	sm.SetProjectTrusted(false)
	writeErr := sm.SetProjectPackages([]PackageSource{{Source: "npm:blocked"}})
	if writeErr == nil {
		t.Fatal("untrusted write succeeded")
	}
	sm.SetProjectTrusted(true)
	settingsFirstHalfOK(t, sm.SetProjectPackages([]PackageSource{{Source: "npm:project"}}))
	settingsFirstHalfOK(t, sm.UpdateProject(func(s *Settings) { s.DefaultProjectTrust = "never" }))
	snapshot := func(settings Settings) map[string]any {
		data, err := json.Marshal(settings)
		settingsFirstHalfOK(t, err)
		var value map[string]any
		settingsFirstHalfOK(t, json.Unmarshal(data, &value))
		return value
	}
	entries, err := os.ReadDir(".")
	settingsFirstHalfOK(t, err)
	if len(entries) != 0 {
		t.Fatalf("memory settings created files: %v", entries)
	}
	themePresence := func(manager *SettingsManager) []any {
		value := manager.GetThemeSetting()
		return []any{value != nil, value}
	}
	result := map[string]any{
		"global": snapshot(sm.GetGlobalSettings()), "project": snapshot(sm.GetProjectSettings()),
		"themePresence": map[string]any{"omitted": themePresence(settingsFirstHalfMemory(t, `{}`)), "empty": themePresence(settingsFirstHalfMemory(t, `{"theme":""}`))},
		"themeSetting":  sm.GetThemeSetting(), "fixedThemeConfigured": sm.GetTheme() != "",
		"defaultTrust": sm.GetDefaultProjectTrust(), "untrustedWriteError": writeErr.Error(),
	}
	data, err := json.Marshal(result)
	settingsFirstHalfOK(t, err)
	fmt.Println("SETTINGS_CONTRACT " + string(data))
}
