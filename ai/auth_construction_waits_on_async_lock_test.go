package ai

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// D651 part C. Pi's constructor reloads through the synchronous lock (auth-storage.ts:81-95 acquireLockSyncWithRetry: ten attempts 20 ms apart) and swallows the failure (constructor reload, auth-storage.ts), so a process that opens a credential store while an async refresh holds a fresh lock waits out the nine delays (180 ms) and then carries on with an empty store. Pi's refresher renews its lock only every 15 s, so after 10 s its first attempt takes the lock at once; pig's 5 s renewal keeps the lock fresh. The elapsed time has only a lower bound: the loop's own sleeps are the wait.
func TestAuthStorageConstructionWaitsOnAFreshAsyncLockThenCarriesOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	const stored = `{"anthropic":{"type":"api_key","key":"stored"}}`
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	holder, err := pilock.Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Release() })
	before, err := os.Lstat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	reads := countAuthFileReads(t)

	start := time.Now()
	store, err := NewAuthStorage(path)
	elapsed := time.Since(start)
	if err != nil || store == nil {
		t.Fatalf("NewAuthStorage while an async refresh holds the lock = %v, %v, want a store", store, err)
	}
	if want := 180 * time.Millisecond; elapsed < want {
		t.Fatalf("construction took %v, want at least %v: the fresh lock must be waited out", elapsed, want)
	}
	if *reads != 0 {
		t.Fatal("the constructor read auth.json without the lock")
	}
	after, err := os.Lstat(path + ".lock")
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the holder's lock was replaced: %v", err)
	}
	if err := holder.Compromised(); err != nil {
		t.Fatalf("the holder's lock was compromised: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != stored {
		t.Fatalf("auth.json = %q, %v, want it untouched", got, err)
	}
}
