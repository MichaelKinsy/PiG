package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// seedFirstRunDone writes an empty settings.json into agentDir unless one exists.
func seedFirstRunDone(t *testing.T, agentDir string) {
	t.Helper()
	path := filepath.Join(agentDir, "settings.json")
	if _, err := os.Stat(path); err == nil {
		return
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}
