package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Reload returns only after the packed processes it replaced have exited. A
// Reload that returns while a replaced process is still exiting leaves two
// processes for one Node cell and keeps the old process's cache usage lease.
func TestReloadWaitsForReplacedPackedProcesses(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) {
		return []ExtConfig{{Name: "ctx-mode", Source: fixture, Enabled: true}}, nil
	})
	t.Cleanup(func() { h.Shutdown("test done") })
	if loaded, err := h.Reload(t.Context()); err != nil || len(loaded) != 1 {
		t.Fatalf("initial reload = %d extensions, %v", len(loaded), err)
	}
	h.mu.Lock()
	replaced := make([]*packedProcessState, 0, len(h.packedProcesses))
	for _, process := range h.packedProcesses {
		replaced = append(replaced, process)
	}
	h.mu.Unlock()
	if len(replaced) == 0 {
		t.Fatal("the extension did not run in a packed process")
	}

	if loaded, err := h.Reload(t.Context()); err != nil || len(loaded) != 1 {
		t.Fatalf("reload = %d extensions, %v", len(loaded), err)
	}
	for _, process := range replaced {
		select {
		case <-process.startWait():
		default:
			t.Errorf("Reload returned before the replaced packed process %s exited", process.key)
		}
	}
}
