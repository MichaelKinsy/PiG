//go:build !windows

package pilock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An async lock (an OAuth refresh in flight) that is held past the sync acquirers' 10 s staleness window must still be held: a process that opens a credential store takes any lock whose directory is older than SyncStale, so the holder's heartbeat has to touch it more often than that. Pi's 15 s heartbeat let the lock be taken at 10 s.
func TestAsyncLockHeldPastTheSyncStaleWindowIsNotTaken(t *testing.T) {
	if testing.Short() {
		t.Skip("holds a lock for 12 s")
	}
	file := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	time.Sleep(SyncStale + 2*time.Second)
	if taken, err := AcquireAuthSync(file); err == nil {
		_ = taken.Release()
		t.Fatal("a sync acquirer took an async lock that was still held")
	} else if !errors.Is(err, ErrLocked) {
		t.Fatalf("sync acquire = %v, want ErrLocked", err)
	}
	if err := lock.Compromised(); err != nil {
		t.Fatalf("the holder's lock was compromised: %v", err)
	}
}
