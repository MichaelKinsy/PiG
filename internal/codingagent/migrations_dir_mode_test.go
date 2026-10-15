//go:build !windows

package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// pig divergence (D132): the directory the agent-root session migration creates for a moved session is owner-only, like every session directory.
func TestMigrateSessionsFromAgentRootCreatesOwnerOnlyDirectories(t *testing.T) {
	agentDir := t.TempDir()
	content := `{"type":"session","cwd":"/tmp/demo/project"}` + "\n"
	if err := os.WriteFile(filepath.Join(agentDir, "session.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateSessionsFromAgentRoot(agentDir)
	for _, dir := range []string{filepath.Join(agentDir, "sessions"), filepath.Join(agentDir, "sessions", "--tmp-demo-project--")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, mode)
		}
	}
}
