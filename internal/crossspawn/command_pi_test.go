//go:build !windows

package crossspawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pi: packages/coding-agent/src/utils/child-process.ts

// spawnProcess (child-process.ts:19-22): on a non-Windows platform the command spawns directly with the working directory the caller
// gave; there is no shell and no argument re-quoting, so an argument with spaces and quotes arrives as one argument.
func TestCommandSpawnsDirectlyInTheGivenDirectory(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command(context.Background(), dir, "pwd")
	if cmd.Dir != dir {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, dir)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != dir {
		t.Fatalf("pwd = %q, want %q", got, dir)
	}

	arg := `a "quoted" arg; $HOME`
	out, err = Command(context.Background(), dir, "printf", "%s", arg).Output()
	if err != nil || string(out) != arg {
		t.Fatalf("printf = %q, %v, want the argument unchanged", out, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

// A cancelled context kills the spawned process (the Go counterpart of the AbortSignal callers pass to spawn).
func TestCommandIsKilledWhenItsContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := Command(ctx, t.TempDir(), "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("sleep exited successfully after its context was cancelled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("process survived context cancellation")
	}
}
