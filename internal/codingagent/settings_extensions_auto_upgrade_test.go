package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// extensionsAutoUpgrade is off by default, is read from the global file, keeps
// Pi's extensions array beside it, and an explicit false keeps it off. A
// rewrite of the settings file keeps the key.
func TestExtensionsAutoUpgradeSetting(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	if NewSettingsManager(cwd, agentDir).GetExtensionsAutoUpgrade() {
		t.Fatal("the upgrade is on by default")
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark","extensions":["./a"],"extensionsAutoUpgrade":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(cwd, agentDir)
	if !sm.GetExtensionsAutoUpgrade() {
		t.Fatal("the global setting was not read")
	}
	if extensions := sm.GetGlobalSettings().Extensions; len(extensions) != 1 || extensions[0] != "./a" {
		t.Fatalf("Pi's extensions array = %v", extensions)
	}
	if err := sm.UpdateGlobal(func(s *Settings) { s.Theme = "light" }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := json.Unmarshal(data, &written); err != nil || written["extensionsAutoUpgrade"] != true || written["theme"] != "light" {
		t.Fatalf("settings file = %s (%v)", data, err)
	}
	if !strings.Contains(string(data), `"extensions"`) {
		t.Fatalf("Pi's extensions array was dropped: %s", data)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"extensionsAutoUpgrade":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if NewSettingsManager(cwd, agentDir).GetExtensionsAutoUpgrade() {
		t.Fatal("an explicit false turned the upgrade on")
	}
}

func TestExtensionsAutoUpgradeIgnoresAProjectFile(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pig", "settings.json"), []byte(`{"extensionsAutoUpgrade":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if NewSettingsManager(cwd, agentDir).GetExtensionsAutoUpgrade() {
		t.Fatal("a project file turned the upgrade on")
	}
}
