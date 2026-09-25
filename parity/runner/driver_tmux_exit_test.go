//go:build parity

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An asserted exit is observed only once the launch shell has published a
// complete status. An absent, empty, or partially written status is still
// awaited (never read as 0), and a process that never publishes one is
// reported as not having exited.
func TestAwaitProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process-exit-status")
	err := awaitProcessExit(context.Background(), path, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("missing status: err = %v, want did-not-exit", err)
	}

	// Partial publications: the file exists but the status is incomplete.
	for _, partial := range []string{"", "1"} {
		if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
			t.Fatal(err)
		}
		if code, exited, err := readProcessExitStatus(path); exited || err != nil || code != 0 {
			t.Fatalf("partial %q read as code=%d exited=%v err=%v", partial, code, exited, err)
		}
		err := awaitProcessExit(context.Background(), path, 50*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "did not exit") {
			t.Fatalf("partial %q: err = %v, want did-not-exit", partial, err)
		}
	}

	// A waiter started on a partial status completes when the status is
	// published (atomically, as the launch shell does: temp file + rename).
	done := make(chan error, 1)
	go func() { done <- awaitProcessExit(context.Background(), path, 10*time.Second) }()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("published status: err = %v", err)
	}
	if code, exited, err := readProcessExitStatus(path); code != 1 || !exited || err != nil {
		t.Fatalf("status = %d exited=%v err=%v", code, exited, err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := awaitProcessExit(context.Background(), path, time.Second); err == nil {
		t.Fatal("a complete but malformed status was accepted")
	}
}

func TestExpectedExitCodePrefersPerBinaryAssertion(t *testing.T) {
	one, two := 1, 2
	sc := &Scenario{}
	sc.Assert.ExitCode = &one
	sc.Assert.PigExitCode = &two
	if got := expectedExitCode(sc, "pig"); got == nil || *got != 2 {
		t.Fatalf("pig = %v", got)
	}
	if got := expectedExitCode(sc, "pi"); got == nil || *got != 1 {
		t.Fatalf("pi = %v", got)
	}
	if got := expectedExitCode(&Scenario{}, "pi"); got != nil {
		t.Fatalf("no assertion = %v", *got)
	}
}

func TestCapturePaneArgsJoinWrappedRows(t *testing.T) {
	if got := strings.Join(capturePaneArgs("s", true, TmuxDriverConfig{}), " "); got != "capture-pane -e -t s -p" {
		t.Fatalf("default escaped capture = %q", got)
	}
	if got := strings.Join(capturePaneArgs("s", false, TmuxDriverConfig{CaptureJoinWrapped: true}), " "); got != "capture-pane -J -t s -p" {
		t.Fatalf("joined plain capture = %q", got)
	}
	if got := strings.Join(capturePaneArgs("s", true, TmuxDriverConfig{CaptureJoinWrapped: true}), " "); got != "capture-pane -e -J -t s -p" {
		t.Fatalf("joined escaped capture = %q", got)
	}
}
