package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A host process that is about to exit calls TerminateProcesses and then exits without Shutdown (RPC mode ends with os.Exit, as Pi's process.exit does, so its deferred Runtime.Close never runs). TerminateProcesses therefore removes the Host's private runtime directory itself: nothing is left in the socket directory after a normal exit. The directory belongs to the Host and not to a cell, so this holds for every placement of an extension.
func TestTerminateProcessesRemovesTheRuntimeDirectory(t *testing.T) {
	socketDir := testenv.ShortTempDir(t, "pig-term-")
	h := NewHost(t.TempDir())
	h.socketDir = socketDir
	t.Cleanup(func() { h.Shutdown("test complete") })
	path, err := h.newSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(path)) != socketDir {
		t.Fatalf("the socket path %q is not in a runtime directory below %q", path, socketDir)
	}
	leftSocketFile(t, path)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "harness-argv.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.TerminateProcesses()
	if entries, err := os.ReadDir(socketDir); err != nil || len(entries) != 0 {
		t.Fatalf("the socket directory holds %v (%v) after TerminateProcesses, want nothing", entries, err)
	}
	h.TerminateProcesses()
}

// A Host that made no socket path has no directory to remove, and neither TerminateProcesses nor a generation that asks for a socket path after it makes one: the process is exiting, and a directory made then would be left.
func TestTerminateProcessesMakesNoRuntimeDirectoryAndNoneAfterIt(t *testing.T) {
	socketDir := testenv.ShortTempDir(t, "pig-term-")
	h := NewHost(t.TempDir())
	h.socketDir = socketDir
	t.Cleanup(func() { h.Shutdown("test complete") })
	h.TerminateProcesses()
	if entries, err := os.ReadDir(socketDir); err != nil || len(entries) != 0 {
		t.Fatalf("the socket directory holds %v (%v), want nothing", entries, err)
	}
	if path, err := h.newSocketPath(); err == nil {
		t.Fatalf("a terminated Host that had no directory made the socket path %q", path)
	}
	if entries, err := os.ReadDir(socketDir); err != nil || len(entries) != 0 {
		t.Fatalf("the socket directory holds %v (%v) after a socket path was asked of a terminated Host, want nothing", entries, err)
	}
}
