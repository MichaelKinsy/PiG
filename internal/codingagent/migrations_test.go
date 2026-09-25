package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationDocsURLsUseEarendilWorksRepo(t *testing.T) {
	if !strings.Contains(migrationGuideURL, "github.com/earendil-works/pi") {
		t.Fatalf("migrationGuideURL = %q, want earendil-works repo", migrationGuideURL)
	}
	if !strings.Contains(extensionsDocURL, "github.com/earendil-works/pi") {
		t.Fatalf("extensionsDocURL = %q, want earendil-works repo", extensionsDocURL)
	}
}

func TestMigrateSessionsFromAgentRootMovesSessionIntoEncodedDir(t *testing.T) {
	agentDir := t.TempDir()
	sessionName := "session.jsonl"
	sessionPath := filepath.Join(agentDir, sessionName)
	content := `{"type":"session","cwd":"/tmp/demo/project"}` + "\n" + `{"type":"user","message":"hi"}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateSessionsFromAgentRoot(agentDir)

	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("original session path still exists or unexpected error: %v", err)
	}

	migratedPath := filepath.Join(agentDir, "sessions", "--tmp-demo-project--", sessionName)
	data, err := os.ReadFile(migratedPath)
	if err != nil {
		t.Fatalf("read migrated session: %v", err)
	}
	if string(data) != content {
		t.Fatalf("migrated content changed:\n got: %q\nwant: %q", string(data), content)
	}
}

// Upstream migrateExtensionSystem targets join(cwd, CONFIG_DIR_NAME). PiG's
// CONFIG_DIR_NAME is ".pig", so the project migration must rename
// .pig/commands and must never touch Pi's own .pi project folder.
func TestRunMigrationsProjectCommandsTargetPigConfigDirNotPi(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	piCommands := filepath.Join(cwd, ".pi", "commands")
	pigCommands := filepath.Join(cwd, ".pig", "commands")
	for _, dir := range []string{piCommands, pigCommands, filepath.Join(cwd, ".pi", "hooks"), filepath.Join(cwd, ".pi", "tools", "custom")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pig", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, warnings, err := RunMigrations(cwd, agentDir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(piCommands); err != nil {
		t.Fatalf("Pi project .pi/commands was modified: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".pi", "prompts")); !os.IsNotExist(err) {
		t.Fatalf(".pi/prompts must not be created, stat err = %v", err)
	}
	if _, err := os.Stat(pigCommands); !os.IsNotExist(err) {
		t.Fatalf(".pig/commands still exists, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".pig", "prompts")); err != nil {
		t.Fatalf(".pig/prompts missing after migration: %v", err)
	}
	want := []string{"Project hooks/ directory found. Hooks have been renamed to extensions."}
	if strings.Join(warnings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("warnings = %q, want %q (from .pig only)", warnings, want)
	}
}

// Upstream strips exactly one leading separator (/^[/\\]/), the same encoding
// as session-manager.ts getDefaultSessionDir.
func TestMigrateSessionsFromAgentRootStripsOneLeadingSeparator(t *testing.T) {
	agentDir := t.TempDir()
	content := `{"type":"session","cwd":"//srv/share"}` + "\n"
	if err := os.WriteFile(filepath.Join(agentDir, "s.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateSessionsFromAgentRoot(agentDir)

	if _, err := os.Stat(filepath.Join(agentDir, "sessions", "---srv-share--", "s.jsonl")); err != nil {
		t.Fatalf("session not moved into upstream-encoded dir: %v", err)
	}
}
