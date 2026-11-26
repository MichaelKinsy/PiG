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

	t.Run("writes explicit false for the toggled notice without writing other defaults", func(t *testing.T) {
		agentDir, projectDir := t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Join(projectDir, ConfigDirName()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, ConfigDirName(), "settings.json"), []byte(`{"notices":{"cacheMisses":false}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		sm := NewSettingsManagerWithProjectTrust(projectDir, agentDir, true)
		if err := sm.SetNotice(NoticeCacheWarming, false); err != nil {
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
				"cacheWarming": false,
			},
		}
		if !reflect.DeepEqual(saved, wantSaved) {
			t.Fatalf("saved = %#v, want %#v", saved, wantSaved)
		}
		got := sm.GetNotices()
		if got.CacheMisses || got.CacheWarming || got.CompactionCosts || got.ReasoningDrops {
			t.Fatalf("merged GetNotices() = %+v, want all false", got)
		}
	})

	t.Run("preserves unknown notice keys written via SetNotice", func(t *testing.T) {
		agentDir, projectDir := t.TempDir(), t.TempDir()
		sm := NewSettingsManager(projectDir, agentDir)
		if err := sm.SetNotice(NoticeKey("customNotice"), true); err != nil {
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
				"customNotice": true,
			},
		}
		if !reflect.DeepEqual(saved, wantSaved) {
			t.Fatalf("saved = %#v, want %#v", saved, wantSaved)
		}
	})

	t.Run("deprecated ShowCacheMissNotices field and wrappers sync all four notices", func(t *testing.T) {
		agentDir, projectDir := t.TempDir(), t.TempDir()
		sm := NewSettingsManager(projectDir, agentDir)
		if sm.GetShowCacheMissNotices() || sm.Get().ShowCacheMissNotices {
			t.Fatalf("default ShowCacheMissNotices getter=%v field=%v, want false", sm.GetShowCacheMissNotices(), sm.Get().ShowCacheMissNotices)
		}
		if err := sm.SetShowCacheMissNotices(true); err != nil {
			t.Fatal(err)
		}
		got := sm.GetNotices()
		if !sm.GetShowCacheMissNotices() || !sm.Get().ShowCacheMissNotices || !got.CacheMisses || !got.CacheWarming || !got.CompactionCosts || !got.ReasoningDrops {
			t.Fatalf("after SetShowCacheMissNotices(true): getter=%v field=%v notices=%+v", sm.GetShowCacheMissNotices(), sm.Get().ShowCacheMissNotices, got)
		}
		// Reload from disk and verify Settings.ShowCacheMissNotices is populated on load (T4).
		reloaded := NewSettingsManager(projectDir, agentDir)
		if !reloaded.Get().ShowCacheMissNotices {
			t.Fatalf("reloaded Settings.ShowCacheMissNotices = false, want true")
		}
		// Host extension graph aliases (T9).
		g := &GraphSettingsManager{manager: sm}
		val, err := g.Call(t.Context(), "getShowCacheMissNotices", json.RawMessage(`[]`))
		if err != nil || val != true {
			t.Fatalf("graph getShowCacheMissNotices = %v, %v; want true, nil", val, err)
		}
		if _, err := g.Call(t.Context(), "setShowCacheMissNotices", json.RawMessage(`[false]`)); err != nil {
			t.Fatalf("graph setShowCacheMissNotices(false): %v", err)
		}
		got = sm.GetNotices()
		if got.CacheMisses || got.CacheWarming || got.CompactionCosts || got.ReasoningDrops {
			t.Fatalf("after graph setShowCacheMissNotices(false): notices=%+v, want all false", got)
		}
	})

	t.Run("preserves non-boolean and null notice values on JSON round-trip", func(t *testing.T) {
		const rawJSON = `{"notices":{"customStr":"keep","cacheMisses":null,"cacheWarming":true,"customNum":42}}`
		var s Settings
		if err := json.Unmarshal([]byte(rawJSON), &s); err != nil {
			t.Fatal(err)
		}
		if s.Notices == nil || s.Notices.CacheMisses || s.Notices.cacheMissesSet || !s.Notices.CacheWarming {
			t.Fatalf("unmarshaled notices = %+v", s.Notices)
		}
		encoded, err := json.Marshal(s.Notices)
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"customStr":"keep","cacheMisses":null,"cacheWarming":true,"customNum":42}`
		if string(encoded) != want {
			t.Fatalf("round-trip = %s, want %s", encoded, want)
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
