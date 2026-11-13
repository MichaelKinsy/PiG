package codingagent

// pi: packages/coding-agent/src/core/trust-manager.ts

import (
	"os"
	"testing"
	"time"
)

// trust-manager.ts:145-185: set() runs inside withTrustFileLock, whose lock attempt is synchronous: while another holder owns trust.json.lock it
// retries for ten attempts 20 ms apart and then throws proper-lockfile's ELOCKED error, without writing; a free lock is taken at once and released
// when the write returns, so the next call does not find it held.
func TestProjectTrustStoreSetFailsWithTheLockErrorWhileTheLockIsHeldAndReleasesIt(t *testing.T) {
	store := NewProjectTrustStore(t.TempDir())
	if err := os.Mkdir(store.trustPath+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	start := time.Now()
	err := store.Set(cwd, new(true))
	if !isProperLockfileContention(err) {
		t.Fatalf("Set with the lock held = %v, want proper-lockfile's ELOCKED error", err)
	}
	if elapsed, window := time.Since(start), 9*20*time.Millisecond; elapsed < window {
		t.Fatalf("Set gave up after %v, want the ten attempts to take about %v", elapsed, window)
	}
	if err := os.Remove(store.trustPath + ".lock"); err != nil {
		t.Fatal(err)
	}
	if decision, _ := store.Get(cwd); decision != nil {
		t.Fatalf("a Set that failed to take the lock stored %v", *decision)
	}
	if err := store.Set(cwd, new(true)); err != nil {
		t.Fatalf("Set with the lock free: %v", err)
	}
	if _, err := os.Stat(store.trustPath + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("the lock was still held after Set returned (stat err %v)", err)
	}
	if decision, _ := store.Get(cwd); decision == nil || !bool(*decision) {
		t.Fatalf("decision after the successful Set = %v", decision)
	}
}
