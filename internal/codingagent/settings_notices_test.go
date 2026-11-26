package codingagent

// Port of Pi main 88c6e00f7d1738039d6fd10d47d681f378443632
// (packages/coding-agent/test/settings-manager.test.ts and settings-selector.test.ts).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestSettingsManagerNotices(t *testing.T) {
	t.Run("fills in defaults for unset notices", func(t *testing.T) {
		sm := NewInMemorySettingsManager(Settings{
			Notices: &NoticeSettings{CacheMisses: true, cacheMissesSet: true},
		})
		want := NoticeSettings{
			CacheMisses:     true,
			CacheWarming:    false,
			CompactionCosts: false,
			ReasoningDrops:  false,
		}
		got := sm.GetNotices()
		if got.CacheMisses != want.CacheMisses || got.CacheWarming != want.CacheWarming || got.CompactionCosts != want.CompactionCosts || got.ReasoningDrops != want.ReasoningDrops {
			t.Fatalf("GetNotices() = %+v, want %+v", got, want)
		}
	})

	t.Run("migrates showCacheMissNotices to every notice and drops it on save", func(t *testing.T) {
		agentDir, projectDir := t.TempDir(), t.TempDir()
		settingsPath := filepath.Join(agentDir, "settings.json")
		if err := os.WriteFile(settingsPath, []byte(`{"showCacheMissNotices":false}`), 0o600); err != nil {
			t.Fatal(err)
		}
		sm := NewSettingsManager(projectDir, agentDir)
		got := sm.GetNotices()
		if got.CacheMisses || got.CacheWarming || got.CompactionCosts || got.ReasoningDrops {
			t.Fatalf("migrated GetNotices() = %+v, want all false", got)
		}
		if err := sm.SetNotice(NoticeReasoningDrops, true); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		var saved map[string]any
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"notices": map[string]any{
				"cacheMisses":     false,
				"cacheWarming":    false,
				"compactionCosts": false,
				"reasoningDrops":  true,
			},
		}
		if !reflect.DeepEqual(saved, want) {
			t.Fatalf("saved = %#v, want %#v", saved, want)
		}
	})

	t.Run("writes only the toggled notice to global settings", func(t *testing.T) {
		agentDir, projectDir := t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Join(projectDir, ConfigDirName()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, ConfigDirName(), "settings.json"), []byte(`{"notices":{"cacheMisses":true}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		sm := NewSettingsManagerWithProjectTrust(projectDir, agentDir, true)
		if err := sm.SetNotice(NoticeCacheWarming, true); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		var saved map[string]any
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		wantSaved := map[string]any{
			"notices": map[string]any{
				"cacheWarming": true,
			},
		}
		if !reflect.DeepEqual(saved, wantSaved) {
			t.Fatalf("saved = %#v, want %#v", saved, wantSaved)
		}
		got := sm.GetNotices()
		if !got.CacheMisses || !got.CacheWarming || got.CompactionCosts || got.ReasoningDrops {
			t.Fatalf("merged GetNotices() = %+v, want cacheMisses=true cacheWarming=true others=false", got)
		}
	})

	t.Run("keeps notices already set over the legacy setting", func(t *testing.T) {
		agentDir, projectDir := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"showCacheMissNotices":true,"notices":{"cacheWarming":false}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		sm := NewSettingsManager(projectDir, agentDir)
		got := sm.GetNotices()
		if !got.CacheMisses || got.CacheWarming || !got.CompactionCosts || !got.ReasoningDrops {
			t.Fatalf("GetNotices() = %+v, want cacheMisses=true cacheWarming=false compactionCosts=true reasoningDrops=true", got)
		}
	})
}

func TestSettingsSelectorNoticesSubmenu(t *testing.T) {
	restoreStartupTheme(t)
	var changes [][2]any
	selector := NewSettingsSelectorComponent(SettingsConfig{
		Notices: NoticeSettings{
			CacheMisses:     true,
			CacheWarming:    true,
			CompactionCosts: true,
			ReasoningDrops:  true,
		},
		DefaultModel: "not set",
	}, SettingsCallbacks{
		OnNoticeChange: func(key NoticeKey, shown bool) {
			changes = append(changes, [2]any{key, shown})
		},
	})
	list := selector.GetSettingsList()
	list.SelectItem("notices")
	list.HandleInput("\r")
	output := widthx.StripAnsi(strings.Join(list.Render(120), "\n"))
	if !strings.Contains(output, "Cache misses") || !strings.Contains(output, "Reasoning drops") {
		t.Fatalf("notices submenu output missing expected items:\n%s", output)
	}
	for range 3 {
		list.HandleInput("\x1b[B")
	}
	list.HandleInput("\r")
	want := [][2]any{{NoticeReasoningDrops, false}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("OnNoticeChange calls = %v, want %v", changes, want)
	}
}
