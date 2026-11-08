package sdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newDiagnosticLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ext.log")
	if err := os.WriteFile(path, []byte("host wrote this line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(extLogEnv, path)
	diagnosticBytes.Store(0)
	t.Cleanup(func() { diagnosticBytes.Store(0) })
	return path
}

// An SDK diagnostic, among them the stack of a recovered panic, goes to the log the host named and not to stderr, which the host forwards to the terminal. The host's own
// writes to the log stay where they are: the SDK appends.
func TestDiagnosticsGoToTheExtensionLogAndNotStderr(t *testing.T) {
	path := newDiagnosticLog(t)
	stderr := captureStderr(t, func() {
		reportRecoveredPanic("a probe", "probe-panic-boom")
		reportHostCallFailure("probe", os.ErrClosed, nil)
	})
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: the diagnostics belong to the extension log", stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{"host wrote this line\n", "recovered from panic in a probe: probe-panic-boom", "goroutine ", "pig: host call probe failed:"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// With no log (no host started the process) or a log that cannot be written, the diagnostic goes to stderr rather than nowhere.
func TestDiagnosticsFallBackToStderr(t *testing.T) {
	t.Setenv(extLogEnv, "")
	if out := captureStderr(t, func() { diagnosticf("pig: no host\n") }); out != "pig: no host\n" {
		t.Errorf("no log: stderr = %q", out)
	}
	t.Setenv(extLogEnv, filepath.Join(t.TempDir(), "missing", "ext.log"))
	diagnosticBytes.Store(0)
	if out := captureStderr(t, func() { diagnosticf("pig: unwritable\n") }); out != "pig: unwritable\n" {
		t.Errorf("unwritable log: stderr = %q", out)
	}
}

// A handler that panics on every event cannot fill the disk through the log: the diagnostics stop at the bound, with one line that says so.
func TestDiagnosticLogIsBounded(t *testing.T) {
	path := newDiagnosticLog(t)
	chunk := strings.Repeat("x", 1<<16) + "\n"
	stderr := captureStderr(t, func() {
		for range 2 * diagnosticLogMax / len(chunk) {
			diagnosticf("%s", chunk)
		}
	})
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if limit := int64(diagnosticLogMax + 1<<10); info.Size() > limit {
		t.Errorf("log is %d bytes, want at most %d", info.Size(), limit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "further SDK diagnostics dropped"); n != 1 {
		t.Errorf("the log says diagnostics were dropped %d times, want once", n)
	}
}
