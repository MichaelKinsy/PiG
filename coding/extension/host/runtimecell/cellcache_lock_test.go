package runtimecell

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/filelock"
)

const (
	envHold    = "CELLCACHE_HOLD"
	heldLine   = "CELLCACHE-LOCK-HELD"
	lockSlot   = ".locks"
	lockSuffix = ".lock"
)

// TestCellcacheLockHolderProcess is not a real test: when CELLCACHE_HOLD names a lock file it takes that lock, says so, and holds it
// until the process is killed, standing in for a Pig process that dies in the middle of a build.
func TestCellcacheLockHolderProcess(t *testing.T) {
	path := os.Getenv(envHold)
	if path == "" {
		t.Skip("helper process entry point")
	}
	lock := filelock.New(path)
	locked, err := lock.TryLock()
	if err != nil || !locked {
		t.Fatalf("hold %s: locked=%v err=%v", path, locked, err)
	}
	_, _ = os.Stdout.WriteString(heldLine + "\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func publishQuickly(ctx context.Context, finalDir, digest string, built *bool) (PublishedEntry, error) {
	return PublishArtifact(ctx, finalDir, "runner", digest, "go", func(scratch string) (string, error) {
		*built = true
		artifact := filepath.Join(scratch, "runner")
		return artifact, os.WriteFile(artifact, []byte("artifact"), 0o755)
	})
}

// A build whose context ended before it began fails with the build lock free, because the lock is never tried once the context is done.
// The error must say that the context ended: a package run that used up its go test deadline starves every build that starts after it,
// and an error about a lock sends the reader after a holder that does not exist.
func TestPublishArtifactEndedContextIsNotReportedAsALockWait(t *testing.T) {
	finalDir := filepath.Join(t.TempDir(), "cells", "go", "ended-context")
	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(1, 0))
	defer cancel()

	var built bool
	_, err := publishQuickly(ctx, finalDir, "ended-context", &built)
	if built {
		t.Fatal("a build whose context had ended ran")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's deadline", err)
	}
	if strings.Contains(err.Error(), "lock") {
		t.Fatalf("err = %q, but no lock was waited for: the lock is free and was never tried", err)
	}
}

// Build locks are flock(2) locks: the kernel releases one with the process that took it, and the lock file it leaves is not a lock. A
// build that follows a finished or killed one takes the lock at once, without a wait that a short context would turn into a failure.
func TestPublishArtifactLockLeftByAnEndedBuildDoesNotBlock(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cells", "go")
	finalDir := filepath.Join(cache, "after-ended-build")
	lockPath := filepath.Join(cache, lockSlot, "after-ended-build"+lockSuffix)

	cmd := exec.Command(os.Args[0], "-test.run=^TestCellcacheLockHolderProcess$")
	cmd.Env = append(os.Environ(), envHold+"="+lockPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	held := false
	for scanner := bufio.NewScanner(stdout); scanner.Scan(); {
		if scanner.Text() == heldLine {
			held = true
			break
		}
	}
	if !held {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("the lock holder never took the lock")
	}
	// While the holder lives the lock is taken: this is the lock a build would wait for.
	if locked, err := filelock.New(lockPath).TryLock(); err != nil || locked {
		t.Fatalf("lock of a live holder: locked=%v err=%v, want it held", locked, err)
	}
	var built bool
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	// The holder is dead; the lock file it left stays on disk.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("the dead holder's lock file: %v", err)
	}
	live, cancelLive := context.WithCancel(t.Context())
	defer cancelLive()
	built = false
	entry, err := publishQuickly(live, finalDir, "after-ended-build", &built)
	if err != nil {
		t.Fatalf("a build after a killed holder: %v", err)
	}
	if !built || entry.Reused {
		t.Fatalf("built=%v reused=%v, want a fresh build under the dead holder's lock file", built, entry.Reused)
	}
}
