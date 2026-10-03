package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func memorySettingsJSON(t *testing.T, raw string) *SettingsManager {
	t.Helper()
	var initial Settings
	if err := json.Unmarshal([]byte(raw), &initial); err != nil {
		t.Fatal(err)
	}
	return NewInMemorySettingsManager(initial)
}

func printMemorySettingsProbe(t *testing.T, sm *SettingsManager) {
	t.Helper()
	raw, err := json.Marshal(sm.GetGlobalSettings())
	settingsOK(t, err)
	var value any
	settingsOK(t, json.Unmarshal(raw, &value))
	out, err := json.Marshal([]any{sm.GetDefaultThinkingLevel(), sm.GetTheme(), sm.GetImageAutoResize(), sm.GetCompactionEnabled(), value})
	settingsOK(t, err)
	fmt.Printf("SETTINGS_MEMORY %s\n", out)
}

func assertMemorySettings3616(t *testing.T, sm *SettingsManager, want string) {
	t.Helper()
	if sm.GetImageAutoResize() || sm.GetCompactionEnabled() {
		t.Fatal("reload lost explicit false settings")
	}
	got, err := json.Marshal(sm.GetGlobalSettings())
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("global=%s, want %s", got, want)
	}
}

func TestInMemorySettingsReloadUpstream(t *testing.T) {
	restoreStartupTheme(t) // Reload applies the theme setting, which selects the process-wide active theme.
	t.Chdir(t.TempDir())
	const initial = `{"defaultThinkingLevel":"high","images":{"autoResize":false},"compaction":{"enabled":false}}`
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3616-settings-inmemory-reload.test.ts:24
	t.Run("preserves initial settings after direct reload", func(t *testing.T) {
		sm := memorySettingsJSON(t, initial)
		sm.Reload()
		if sm.GetDefaultThinkingLevel() != "high" {
			t.Fatalf("thinking=%q", sm.GetDefaultThinkingLevel())
		}
		assertMemorySettings3616(t, sm, initial)
		printMemorySettingsProbe(t, sm)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3616-settings-inmemory-reload.test.ts:43
	t.Run("preserves initial settings when DefaultResourceLoader reloads", func(t *testing.T) {
		sm := memorySettingsJSON(t, initial)
		m := newSwitchTuiProbe(t)
		m.agent = agent.NewAgent(agent.AgentOptions{})
		m.opts.CWD = t.TempDir()
		m.opts.AgentDir = t.TempDir()
		m.opts.SettingsManager = sm
		m.opts.Settings = sm.Get()
		m.opts.NoPromptTemplates = true
		m.opts.NoSkills = true
		m.opts.NoThemes = true
		// The Go resource loader is split across this production reload action and its snapshot provider.
		m.opts.ReloadResourceProvider = func() ReloadResourceSnapshot { return ReloadResourceSnapshot{} }
		m.buildSlashContext(t.Context()).Reload()
		if sm.GetDefaultThinkingLevel() != "high" {
			t.Fatalf("thinking=%q", sm.GetDefaultThinkingLevel())
		}
		if sm.GetImageAutoResize() || sm.GetCompactionEnabled() {
			t.Fatal("resource reload lost explicit false settings")
		}
		printMemorySettingsProbe(t, sm)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3616-settings-inmemory-reload.test.ts:67
	t.Run("preserves initial settings after an unrelated setter, flush, and reload", func(t *testing.T) {
		sm := memorySettingsJSON(t, `{"images":{"autoResize":false},"compaction":{"enabled":false}}`)
		if err := sm.SetTheme("dark"); err != nil {
			t.Fatal(err)
		}
		if err := sm.Flush(); err != nil {
			t.Fatal(err)
		}
		sm.Reload()
		if sm.GetTheme() != "dark" {
			t.Fatalf("theme=%q", sm.GetTheme())
		}
		assertMemorySettings3616(t, sm, `{"images":{"autoResize":false},"compaction":{"enabled":false},"theme":"dark"}`)
		printMemorySettingsProbe(t, sm)
		if _, err := os.Stat(filepath.Join("settings.json")); !os.IsNotExist(err) {
			t.Fatalf("memory setter touched disk: %v", err)
		}
	})
}
