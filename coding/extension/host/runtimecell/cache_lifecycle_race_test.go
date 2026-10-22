package runtimecell

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// countingContext counts the collector's cancellation checks and runs fire on the one numbered fireAt, which gives a test a
// deterministic point between two steps of a collection without a sleep or a hook in the collector.
type countingContext struct {
	context.Context
	calls  atomic.Int64
	fireAt int64
	fire   func()
}

func (c *countingContext) Err() error {
	if c.calls.Add(1) == c.fireAt && c.fire != nil {
		c.fire()
	}
	return c.Context.Err()
}

// publishAgedCell publishes a Go cell under cacheRoot through the production publisher and rewrites its usage record to sixty days
// ago, which makes the collector's retention (thirty days) expire it.
func publishAgedCell(t *testing.T, cacheRoot string) (EntryIdentity, string) {
	t.Helper()
	digest := strings.Repeat("a", 64)
	finalDir := filepath.Join(cacheRoot, "cells", "go", digest)
	identity := EntryIdentity{InputDigest: digest, Artifact: "runner", Language: "go"}
	if _, err := PublishArtifact(t.Context(), finalDir, "runner", digest, "go", func(scratch string) (string, error) {
		path := filepath.Join(scratch, "runner")
		return path, os.WriteFile(path, []byte("runner"), 0o755)
	}); err != nil {
		t.Fatal(err)
	}
	usage, err := json.Marshal(UsageMetadata{LastUsed: time.Now().Add(-60 * 24 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(finalDir, usageFile), usage, 0o644); err != nil {
		t.Fatal(err)
	}
	return identity, finalDir
}

// Another pig reuses an old cell between the moment a collection classifies it and the moment the collection renames it away. The
// reuse refreshes the usage record but holds no lease until the extension process starts, so the collector must read the entry
// again under its exclusive lock. The collector's removal loop makes one cancellation check per removal candidate, immediately
// before it locks the entry, and the check numbered by an identical collection's Rename is that one.
func TestPruneLeavesAnEntryAPeerReusedAfterItWasClassified(t *testing.T) {
	calibration := &countingContext{Context: t.Context()}
	var checksBeforeRename int64
	calibrationRoot := t.TempDir()
	publishAgedCell(t, calibrationRoot)
	report, err := PruneCaches(CacheLifecycleOptions{
		CacheRoot: calibrationRoot, Context: calibration, Unsized: true,
		Rename: func(from, to string) error {
			checksBeforeRename = calibration.calls.Load()
			return os.Rename(from, to)
		},
	})
	if err != nil || report.Removed != 1 || checksBeforeRename == 0 {
		t.Fatalf("calibration collection = %+v, %v (checks before rename %d); want one old entry removed", report, err, checksBeforeRename)
	}

	root := t.TempDir()
	identity, finalDir := publishAgedCell(t, root)
	var peerReused bool
	collecting := &countingContext{Context: t.Context(), fireAt: checksBeforeRename, fire: func() {
		_, peerReused = ReusePublishedArtifact(finalDir, identity)
	}}
	report, err = PruneCaches(CacheLifecycleOptions{CacheRoot: root, Context: collecting, Unsized: true})
	if err != nil {
		t.Fatal(err)
	}
	if !peerReused {
		t.Fatal("the peer's reuse did not find the entry: the interleaving point moved")
	}
	if report.Removed != 0 || report.Skipped != 1 {
		t.Fatalf("collection removed %d and skipped %d entries; want 0 and 1, the entry a peer reused after the classification", report.Removed, report.Skipped)
	}
	if _, err := os.Stat(filepath.Join(finalDir, "runner")); err != nil {
		t.Fatalf("the reused entry lost its artifact: %v", err)
	}
}

// The collector holds an entry's usage lock exclusively while it renames the entry away. A reuse that finds the entry in that
// window must not report it as reusable: the entry is about to be gone, and the caller builds it again.
func TestReusePublishedArtifactDoesNotReuseAnEntryACollectorHasLocked(t *testing.T) {
	identity, finalDir := publishAgedCell(t, t.TempDir())
	lock := flock.New(cacheLockPath(finalDir, ".usage.lock"))
	if err := os.MkdirAll(filepath.Dir(lock.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if locked, err := lock.TryLock(); err != nil || !locked {
		t.Fatalf("exclusive usage lock = %t, %v", locked, err)
	}
	if entry, ok := ReusePublishedArtifact(finalDir, identity); ok {
		t.Fatalf("reused %+v while a collector held the entry's usage lock", entry)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReusePublishedArtifact(finalDir, identity); !ok {
		t.Fatal("the entry was not reusable after the collector released its lock without removing it")
	}
}

// A cell that another pig's collection removed after this process resolved it must not look like a binary outside the cache: a nil
// lease would let the spawn fail with a bare missing-file error and skip the install-change tracking of the pruned file.
func TestAcquireArtifactUsageLeaseReportsARemovedCacheEntry(t *testing.T) {
	_, finalDir := publishAgedCell(t, t.TempDir())
	artifact := filepath.Join(finalDir, "runner")
	lease, err := AcquireArtifactUsageLease(artifact)
	if err != nil || lease == nil {
		t.Fatalf("lease of a published entry = %v, %v; want a lease", lease, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(finalDir); err != nil {
		t.Fatal(err)
	}
	lease, err = AcquireArtifactUsageLease(artifact)
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("lease of a removed entry = %v, %v; want an error that says the entry is gone", lease, err)
	}
}

// A binary that never lived in the cache (a prebuilt cell, a source script) has no entry to lease, whether or not it exists.
func TestAcquireArtifactUsageLeaseIgnoresAFileOutsideTheCache(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "extension.py")
	if err := os.WriteFile(script, []byte("print()\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{script, filepath.Join(dir, "missing.py")} {
		if lease, err := AcquireArtifactUsageLease(path); lease != nil || err != nil {
			t.Fatalf("lease of %s outside the cache = %v, %v; want none", path, lease, err)
		}
	}
}
