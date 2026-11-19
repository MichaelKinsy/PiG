package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func resourceExtensionFixture(t *testing.T) (root, cwd, agentDir string) {
	t.Helper()
	root = t.TempDir()
	cwd, agentDir = filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_HOME", filepath.Join(root, "config"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	return root, cwd, agentDir
}

// Upstream main.ts formats resource-loader extension errors, including conflicts, as `Failed to load extension "${path}": ${error}` with no escaping, so a double quote in the path is printed verbatim.
func TestExtensionConflictDiagnosticsInterpolatePathVerbatim(t *testing.T) {
	got := extensionConflictDiagnostics([]codingagent.ExtensionConflict{{Path: `/x/a"b/index.ts`, Message: "m"}})
	if len(got) != 1 || got[0].Message != `Failed to load extension "/x/a"b/index.ts": m` {
		t.Fatalf("diagnostics = %#v", got)
	}
}
