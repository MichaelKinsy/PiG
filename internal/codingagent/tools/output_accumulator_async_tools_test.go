package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Port of Pi main 943a99e3c2e1f119fcd633741363a200e0677999 & 8209929fb553e7a8c91dc0894569f9ec43dd201f:
// CloseTempFile never errors; a failed file is dropped and reported as FullOutputError on later snapshots.
func TestOutputAccumulatorCloseTempFileReportsAFailedFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
	accumulator := newOutputAccumulator(1<<30, 1<<10, "pi-async-test")
	if err := accumulator.Append([]byte(strings.Repeat("z", 2<<10))); err != nil {
		t.Fatal(err)
	}
	accumulator.Finish()
	if err := accumulator.CloseTempFile(); err != nil {
		t.Fatalf("CloseTempFile returned error %v, want nil (failure reported via snapshot)", err)
	}
	snap := accumulator.Snapshot(true)
	if snap.FullOutputPath != "" {
		t.Fatalf("FullOutputPath = %q, want empty on failure", snap.FullOutputPath)
	}
	if !strings.Contains(snap.FullOutputError, "ENOENT") {
		t.Fatalf("FullOutputError = %q, want ENOENT", snap.FullOutputError)
	}
	if err := accumulator.CloseTempFile(); err != nil {
		t.Fatalf("a second CloseTempFile returned %v, want nil", err)
	}
	_ = os.Remove(snap.FullOutputPath)
}
