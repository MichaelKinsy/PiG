package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// extensionsTypescriptRuntime is unset by default, is read from the global file only, and a rewrite of the settings file keeps it.
func TestExtensionsTypescriptRuntimeSetting(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	if got := NewSettingsManager(cwd, agentDir).GetExtensionsTypescriptRuntime(); got != "" {
		t.Fatalf("default = %q", got)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pig", "settings.json"), []byte(`{"extensionsTypescriptRuntime":"aot"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := NewSettingsManager(cwd, agentDir).GetExtensionsTypescriptRuntime(); got != "" {
		t.Fatalf("a project file selected %q", got)
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"extensions":["./a"],"extensionsTypescriptRuntime":"bun"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(cwd, agentDir)
	if got := sm.GetExtensionsTypescriptRuntime(); got != "bun" {
		t.Fatalf("global setting = %q", got)
	}
	if err := sm.UpdateGlobal(func(s *Settings) { s.Theme = "light" }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := json.Unmarshal(data, &written); err != nil || written["extensionsTypescriptRuntime"] != "bun" || written["theme"] != "light" || written["extensions"] == nil {
		t.Fatalf("settings file = %s (%v)", data, err)
	}
}
