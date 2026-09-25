package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Shutdown drains the extension processes it stops: when it returns, each
// process has exited and been reaped, so its cache usage lease and handles
// are released.
func TestShutdownReapsExtensionProcesses(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	if _, err := h.Load(t.Context(), ExtConfig{Name: "ctx-mode", Source: fixture, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	me := h.exts["ctx-mode"]
	h.mu.Unlock()
	if me == nil || me.exitedCh == nil {
		t.Fatalf("loaded extension has no process: %+v", me)
	}

	h.Shutdown("test done")

	select {
	case <-me.exitedCh:
	default:
		t.Fatal("Shutdown returned before the extension process was reaped")
	}
}
