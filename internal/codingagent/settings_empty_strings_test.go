package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// settings-manager.ts keeps defaultProvider, defaultModel, shellPath and shellCommandPrefix as defined values when they are "": a project file holding ""
// overrides the global value (deepMergeSettings overrides on !== undefined), and the write keeps the key.
func TestEmptyStringSettingsOverrideAndPersist(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME), 0o755); err != nil {
		t.Fatal(err)
	}
	global := `{"defaultProvider":"gp","defaultModel":"gm","shellPath":"/bin/sh","shellCommandPrefix":"set -e"}`
	project := `{"defaultProvider":"","defaultModel":"","shellPath":"","shellCommandPrefix":""}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, CONFIG_DIR_NAME, "settings.json"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	merged := sm.Get()
	if merged.DefaultProvider != "" || merged.DefaultModel != "" || merged.ShellPath != "" || merged.CommandPrefix != "" {
		t.Fatalf("project empty strings did not override: %+v", merged)
	}
	if err := sm.SetDefaultProvider(""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "{\n  \"defaultProvider\": \"\",\n  \"defaultModel\": \"gm\",\n  \"shellPath\": \"/bin/sh\",\n  \"shellCommandPrefix\": \"set -e\"\n}"; got != want {
		t.Fatalf("settings.json = %q, want %q", got, want)
	}
}

// A write sends only the modified keys (settings-manager.ts persistScopedSettings): an empty string the file held at load and another process removed
// since is not written back by a setter of another key.
func TestEmptyStringSettingIsNotRewrittenByAnotherSetter(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"defaultProvider":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetShellPath("/bin/bash"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "{\n  \"shellPath\": \"/bin/bash\"\n}"; got != want {
		t.Fatalf("settings.json = %q, want %q", got, want)
	}
}
