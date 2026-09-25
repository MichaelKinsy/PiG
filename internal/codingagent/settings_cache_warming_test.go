package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Port of settings-manager.test.ts "cacheWarming".
func TestCacheWarmingModeDefaultsToStreamingAndIgnoresProjectSettings(t *testing.T) {
	projectDir, agentDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, CONFIG_DIR_NAME), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mode := func() CacheWarmingMode { return NewSettingsManager(projectDir, agentDir).GetCacheWarmingMode() }
	if got := mode(); got != "streaming" {
		t.Fatalf("default mode = %q, want streaming", got)
	}
	write(filepath.Join(projectDir, CONFIG_DIR_NAME, "settings.json"), `{"cacheWarming":"idle"}`)
	if got := mode(); got != "streaming" {
		t.Fatalf("project setting leaked: mode = %q, want streaming", got)
	}
	write(filepath.Join(agentDir, "settings.json"), `{"cacheWarming":"idle"}`)
	if got := mode(); got != "idle" {
		t.Fatalf("global mode = %q, want idle", got)
	}
	write(filepath.Join(agentDir, "settings.json"), `{"cacheWarming":"bogus"}`)
	if got := mode(); got != "streaming" {
		t.Fatalf("invalid mode = %q, want streaming", got)
	}
}

func TestCacheWarmingModePersistsGlobally(t *testing.T) {
	projectDir, agentDir := t.TempDir(), t.TempDir()
	if err := NewSettingsManager(projectDir, agentDir).SetCacheWarmingMode("off"); err != nil {
		t.Fatal(err)
	}
	if got := NewSettingsManager(projectDir, agentDir).GetCacheWarmingMode(); got != "off" {
		t.Fatalf("reloaded mode = %q, want off", got)
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "{\n  \"cacheWarming\": \"off\"\n}" && got != "{\n  \"cacheWarming\": \"off\"\n}\n" {
		t.Fatalf("settings.json = %q, want only cacheWarming", got)
	}
}
