package tui

import (
	"os"
	"os/exec"
	"testing"
)

// tuiNode is a Node binary of 26.5.1 or later (`CHORD_TUI_NODE`, else `node26`, else `node` on the PATH); the test is skipped
// without one, as Pi's are.
func tuiNode(t testing.TB) string {
	t.Helper()
	candidates := []string{os.Getenv("CHORD_TUI_NODE"), "node26", "node"}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil && CheckSandboxNode(path) == nil {
			return path
		}
	}
	t.Skip("no Node 26.5.1 or later (CHORD_TUI_NODE, node26 or node)")
	return ""
}
