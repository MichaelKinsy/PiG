package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi FileSettingsStorage.withLock takes the same lock for reads and writes
// (settings-manager.ts:268-296). A reader cannot parse a writer's partial JSON.
func TestSettingsReadHonorsPiWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := loadSettingsFile(path)
	if err == nil || !strings.Contains(err.Error(), "failed to acquire settings lock") {
		t.Fatalf("read parsed an active writer's partial JSON: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"light"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsFile(path)
	if err != nil || settings.Theme != "light" {
		t.Fatalf("read after writer release: %+v %v", settings, err)
	}
}
