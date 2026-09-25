package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMigrateAuthToAuthJSONReportsWriteFailure mirrors upstream
// migrateAuthToAuthJson, whose auth.json write is outside any try/catch, so
// a failed write aborts startup instead of losing the migrated credentials
// silently (GUARD-18).
func TestMigrateAuthToAuthJSONReportsWriteFailure(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"apiKeys":{"openai":"sk-legacy"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A read-only agent directory makes the auth.json write fail.
	testenv.ReadOnlyDir(t, agentDir)
	if _, err := os.Stat(filepath.Join(agentDir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json precondition: %v", err)
	}
	_, _, err := RunMigrations(t.TempDir(), agentDir)
	if err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Fatalf("RunMigrations error = %v, want the auth.json write failure", err)
	}
}

// TestMigrateCommandsToPromptsWarnsOnRenameFailure mirrors upstream
// migrateCommandsToPrompts, which prints a warning when the rename fails.
func TestMigrateCommandsToPromptsWarnsOnRenameFailure(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(baseDir, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.ReadOnlyDir(t, baseDir)
	out := captureStdout(t, func() { migrateCommandsToPrompts(baseDir, "Global") })
	if !strings.Contains(out, "Warning: Could not migrate Global commands/ to prompts/: ") {
		t.Fatalf("stdout = %q, want the rename warning", out)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	fn()
	os.Stdout = previous
	_ = writer.Close()
	data, _ := io.ReadAll(reader)
	return string(data)
}
