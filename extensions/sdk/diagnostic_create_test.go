package sdk

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The SDK creates the log on its first write only when the host allowed it (PIG_EXT_LOG_CREATE): the fused extensions' log is named by the host and made by
// the first diagnostic, with a mode that keeps it to the user. Without the permission a log that does not exist is not made, and the diagnostic goes to stderr.
func TestDiagnosticCreatesTheLogOnlyWithThePermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fused.log")
	t.Setenv(extLogEnv, path)
	t.Setenv(extLogCreateEnv, "")
	diagnosticBytes.Store(0)
	t.Cleanup(func() { diagnosticBytes.Store(0) })

	if out := captureStderr(t, func() { diagnosticf("pig: no permission\n") }); out != "pig: no permission\n" {
		t.Errorf("without the permission: stderr = %q, want the diagnostic", out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the SDK created the log without the permission: %v", err)
	}

	t.Setenv(extLogCreateEnv, "1")
	if out := captureStderr(t, func() { diagnosticf("pig: first\n") }); out != "" {
		t.Errorf("with the permission: stderr = %q, want nothing", out)
	}
	diagnosticf("pig: second\n")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the SDK did not create the log: %v", err)
	}
	if got, want := string(data), "pig: first\npig: second\n"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("the created log has mode %v, want no access for group or others", mode)
		}
	}
}

// A symbolic link planted at the log's path is not written through where the platform can refuse one: the diagnostic goes to stderr and the link's target stays as it was.
func TestDiagnosticDoesNotCreateThroughASymbolicLink(t *testing.T) {
	if !strings.Contains("linux darwin freebsd netbsd openbsd dragonfly", runtime.GOOS) {
		t.Skip("the platform has no open flag that refuses a symbolic link")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fused.log")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv(extLogEnv, path)
	t.Setenv(extLogCreateEnv, "1")
	diagnosticBytes.Store(0)
	t.Cleanup(func() { diagnosticBytes.Store(0) })

	if out := captureStderr(t, func() { diagnosticf("pig: planted\n") }); out != "pig: planted\n" {
		t.Errorf("stderr = %q, want the diagnostic: the link must not be written through", out)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "untouched\n" {
		t.Errorf("the link's target = %q, want it untouched", data)
	}
}
