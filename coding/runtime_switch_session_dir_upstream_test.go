package coding

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Pi's switchSession opens the destination with SessionManager.open(sessionPath), whose constructor creates the file's
// parent directory before the switch (agent-session-runtime.ts:210, session-manager.ts:1011-1013,1784-1785). A
// directory that cannot be created fails the switch with Node's mkdir error and keeps the current session.
func TestSwitchSessionCreatesTheDestinationDirectoryLikePi(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	destination := filepath.Join(t.TempDir(), "nd", "sub", "s.jsonl")
	if result, err := h.runtime.SwitchSession(t.Context(), destination); err != nil || result.Cancelled {
		t.Fatalf("switch = %+v, %v", result, err)
	}
	if info, err := os.Stat(filepath.Dir(destination)); err != nil || !info.IsDir() {
		t.Fatalf("destination directory was not created: %v", err)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	before := h.runtime.Session().Path()
	_, err := h.runtime.SwitchSession(t.Context(), filepath.Join(locked, "a", "s.jsonl"))
	want := "EACCES: permission denied, mkdir '" + filepath.Join(locked, "a") + "'"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got := h.runtime.Session().Path(); got != before {
		t.Fatalf("session after a failed switch = %q, want the current %q", got, before)
	}
}
