//go:build unix

package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// A failed isolated start ends its process and returns only after the process has exited and been reaped, as a stopped extension does. The fresh process's own reference is what reap consults.
func TestFailedIsolatedStartReapsItsProcessBeforeLoadAllReturns(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	pidFile := filepath.Join(root, "pid")
	entry := filepath.Join(root, "throws.mjs")
	source := fmt.Sprintf(`import {writeFileSync} from "node:fs";
writeFileSync(%q, String(process.pid));
export default function () { throw new Error("factory failed"); }
`, pidFile)
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	config := ExtConfig{Name: "throws", Source: entry, Enabled: true, Isolation: "isolated", RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	if _, errs := h.LoadAll(t.Context(), []ExtConfig{config}); len(errs) != 1 {
		t.Fatalf("errors = %v, want the factory failure", errs)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	// A process that exited but was not waited for stays a zombie and still answers signal 0.
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("process %d outlived the failed LoadAll", pid)
	}
}
