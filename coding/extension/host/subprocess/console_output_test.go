package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi redirects stdout onto stderr before extensions load in print/JSON/RPC modes (main.ts takeOverStdout). os/exec shares a child pipe only when both writer values are equal; separate copy goroutines can reorder console.log and console.error.
func TestNodeHeadlessConsoleUsesOneOrderedPipe(t *testing.T) {
	log, err := os.Create(filepath.Join(t.TempDir(), "console.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	for _, mode := range []string{"print", "json", "rpc"} {
		h := &Host{mode: mode}
		stdout, stderr := h.nodeExtensionOutput(log)
		if stdout != stderr {
			t.Errorf("%s: stdout/stderr use independent pipes and can reorder extension console writes", mode)
		}
	}
	stdout, stderr := (&Host{mode: "tui"}).nodeExtensionOutput(log)
	if stdout == stderr {
		t.Error("interactive stdout and stderr must retain distinct destinations")
	}
}
