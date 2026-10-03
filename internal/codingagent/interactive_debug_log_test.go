package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PIG_DEBUG diagnostics belong in the platform temporary directory: a literal
// /tmp names C:\tmp on Windows, where the log is silently lost.
func TestDebugLogWritesBelowTheTemporaryDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TEMP", dir)
	t.Setenv("TMP", dir)
	t.Setenv("PIG_DEBUG", "1")

	debugLog("key %q -> %d", "x", 7)

	data, err := os.ReadFile(filepath.Join(os.TempDir(), "pig-debug.log"))
	if err != nil {
		t.Fatalf("debug log is not below the temporary directory %s: %v", os.TempDir(), err)
	}
	if !strings.Contains(string(data), `key "x" -> 7`) {
		t.Fatalf("debug log = %q, want the formatted entry", data)
	}
}

func TestDebugLogIsOffWithoutPigDebug(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TEMP", dir)
	t.Setenv("TMP", dir)
	t.Setenv("PIG_DEBUG", "")

	debugLog("ignored")

	if _, err := os.Stat(filepath.Join(os.TempDir(), "pig-debug.log")); !os.IsNotExist(err) {
		t.Fatalf("debug log exists without PIG_DEBUG: %v", err)
	}
}
