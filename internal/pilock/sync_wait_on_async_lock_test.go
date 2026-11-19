package pilock

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// D651 part C: a process that opens a credential store while an async refresh holds the lock. Pi's synchronous acquirer (FileAuthStorageBackend.acquireLockSyncWithRetry, auth-storage.ts:81-95) makes ten attempts 20 ms apart and rethrows ELOCKED after the tenth. Pi's refresher renews its lock only every 15 s, so by 10 s the lock is older than the acquirer's 10 s stale window and its first attempt takes the lock at once; pig renews every 5 s, so the lock the acquirer finds is fresh and it waits out all nine delays (180 ms) and reports ELOCKED, leaving the holder's lock alone. The waits are recorded, not slept.
func TestSyncAcquirerWaitsNineDelaysOnAFreshAsyncLockAndLeavesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	createLockTarget(t, path)
	holder, err := Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Release() }()
	before, err := os.Lstat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	waits := recordLockWaits(t)

	taken, err := AcquireAuthSync(path)
	if taken != nil {
		_ = taken.Release()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("AcquireAuthSync on a fresh async lock = %v, want ErrLocked", err)
	}
	want := slices.Repeat([]time.Duration{20 * time.Millisecond}, 9)
	if !slices.Equal(*waits, want) {
		t.Fatalf("waits = %v, want nine of 20 ms (180 ms in all)", *waits)
	}
	after, err := os.Lstat(path + ".lock")
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the holder's lock was replaced: %v", err)
	}
	if err := holder.Compromised(); err != nil {
		t.Fatalf("the holder's lock was compromised: %v", err)
	}
}
