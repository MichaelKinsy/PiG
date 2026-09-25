package runtimecell

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestCacheLifecycleClassifiesHardRootsAndRetention(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	active := writeLifecycleEntry(t, root, "ext", "active", now.Add(-90*24*time.Hour))
	current := writeLifecycleEntry(t, root, "ext", "current", now.Add(-90*24*time.Hour))
	building := writeLifecycleEntry(t, root, "cells/go", "building", now.Add(-90*24*time.Hour))
	recent := writeLifecycleEntry(t, root, "cells/rust", "recent", now.Add(-time.Hour))
	expired := writeLifecycleEntry(t, root, "ext", "expired", now.Add(-31*24*time.Hour))
	lease, err := AcquireUsageLease(active)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	buildLock := flock.New(cacheBuildLockPath(building))
	if err := os.MkdirAll(filepath.Dir(buildLock.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := buildLock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = buildLock.Unlock() }()

	report, err := PruneCaches(CacheLifecycleOptions{
		CacheRoot: root,
		Current:   map[string]struct{}{filepath.Clean(current): {}},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	classes := lifecycleClasses(report)
	for path, want := range map[string]CacheClass{
		active: CacheActive, current: CacheCurrent, building: CacheBuilding,
		recent: CacheInactiveRetained, expired: CacheInactiveExpired,
	} {
		if classes[path] != want {
			t.Errorf("%s class = %q, want %q", path, classes[path], want)
		}
	}
	for _, path := range []string{active, current, building, recent} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("protected/recent entry removed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Errorf("expired entry remains: %v", err)
	}
}

func TestCacheLifecyclePressureRemovesRecentInactiveButNotHardRoots(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	current := writeLifecycleEntrySized(t, root, "ext", "current", now, 64)
	oldest := writeLifecycleEntrySized(t, root, "ext", "oldest", now.Add(-2*time.Hour), 32)
	newest := writeLifecycleEntrySized(t, root, "cells/go", "newest", now.Add(-time.Hour), 32)
	limit := int64(1)
	report, err := PruneCaches(CacheLifecycleOptions{
		CacheRoot: root, Current: map[string]struct{}{current: {}}, Now: func() time.Time { return now }, MaxSize: &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.LimitSatisfied || report.ProtectedBytes < 64 {
		t.Fatalf("pressure result = %#v, want truthful protected excess", report)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("pressure removed current entry: %v", err)
	}
	for _, path := range []string{oldest, newest} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("pressure retained inactive entry %s: %v", path, err)
		}
	}
}

func TestCacheLifecycleDryRunMatchesWetSelectionAndMutatesNothing(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	writeLifecycleEntry(t, root, "ext", "expired-a", now.Add(-31*24*time.Hour))
	writeLifecycleEntry(t, root, "cells/go", "expired-b", now.Add(-40*24*time.Hour))
	options := CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }, DryRun: true}
	dry, err := PruneCaches(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(lifecyclePaths(t, root)) != 2 {
		t.Fatal("dry-run mutated cache")
	}
	options.DryRun = false
	wet, err := PruneCaches(options)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Removed != wet.Removed || dry.RemovedBytes != wet.RemovedBytes {
		t.Fatalf("dry=%#v wet=%#v", dry, wet)
	}
}

func TestCacheLifecycleMalformedUsageIsConservative(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	entry := writeLifecycleEntry(t, root, "ext", "malformed-usage", now.Add(-90*24*time.Hour))
	if err := os.WriteFile(filepath.Join(entry, usageFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := PruneCaches(CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if classes := lifecycleClasses(report); classes[entry] != CacheInactiveRetained {
		t.Fatalf("malformed usage class = %q, want conservative retention", classes[entry])
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("malformed usage entry removed: %v", err)
	}
}

func TestCacheLifecycleIncompleteEntryHonorsCrashGrace(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	fresh := filepath.Join(root, "ext", "fresh-incomplete")
	old := filepath.Join(root, "ext", "old-incomplete")
	for _, path := range []string{fresh, old} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := now.Add(-25 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := PruneCaches(CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh incomplete entry removed: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old incomplete entry remains: %v", err)
	}
}

func TestCacheLifecycleDeletionFailuresAreTruthful(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	t.Run("rename leaves source intact", func(t *testing.T) {
		root := t.TempDir()
		entry := writeLifecycleEntry(t, root, "ext", "expired", now.Add(-31*24*time.Hour))
		report, err := PruneCaches(CacheLifecycleOptions{
			CacheRoot: root, Now: func() time.Time { return now }, Rename: func(string, string) error { return errors.New("rename failed") },
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Errors) != 1 || report.Removed != 0 {
			t.Fatalf("report = %#v", report)
		}
		if _, err := os.Stat(entry); err != nil {
			t.Fatalf("rename failure changed source: %v", err)
		}
	})
	t.Run("tombstone removal is reported", func(t *testing.T) {
		root := t.TempDir()
		entry := writeLifecycleEntry(t, root, "ext", "expired", now.Add(-31*24*time.Hour))
		report, err := PruneCaches(CacheLifecycleOptions{
			CacheRoot: root, Now: func() time.Time { return now }, RemoveAll: func(string) error { return errors.New("remove failed") },
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Errors) != 1 || report.Removed != 0 {
			t.Fatalf("report = %#v", report)
		}
		if _, err := os.Stat(entry); !os.IsNotExist(err) {
			t.Fatalf("source still exists after tombstone rename: %v", err)
		}
		inspection, err := InspectCache(CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }})
		if err != nil || len(inspection.Entries) != 1 || inspection.Entries[0].Reason != "managed tombstone" || inspection.TotalBytes == 0 {
			t.Fatalf("tombstone inspection = %#v, %v", inspection, err)
		}
		retry, err := PruneCaches(CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }})
		if err != nil || retry.Removed != 1 || len(lifecyclePaths(t, root)) != 0 {
			t.Fatalf("tombstone retry = %#v, %v", retry, err)
		}
	})
}

func writeLifecycleEntry(t *testing.T, root, kind, name string, used time.Time) string {
	return writeLifecycleEntrySized(t, root, kind, name, used, 16)
}

func writeLifecycleEntrySized(t *testing.T, root, kind, name string, used time.Time, size int) string {
	t.Helper()
	entry := filepath.Join(root, filepath.FromSlash(kind), name)
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := make([]byte, size)
	if err := os.WriteFile(filepath.Join(entry, "runner"), artifact, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := cellEntryMeta{InputDigest: name, ArtifactDigest: "digest", Size: int64(size), Artifact: "runner", Language: "go", Target: "test/test", Created: used.Unix()}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, cellReadyFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := TouchUsage(entry, used); err != nil {
		t.Fatal(err)
	}
	return entry
}

func lifecycleClasses(report CacheReport) map[string]CacheClass {
	classes := make(map[string]CacheClass, len(report.Entries))
	for _, entry := range report.Entries {
		classes[entry.Path] = entry.Class
	}
	return classes
}

func lifecyclePaths(t *testing.T, root string) []string {
	t.Helper()
	paths, err := cacheEntryPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

const (
	cacheLeaseHelperEnv = "PIG_CACHE_LEASE_HELPER"
	cacheLeaseEntryEnv  = "PIG_CACHE_LEASE_ENTRY"
	cacheLeaseReadyEnv  = "PIG_CACHE_LEASE_READY"
	cacheGCHelperEnv    = "PIG_CACHE_GC_LOCK_HELPER"
	cacheGCRootEnv      = "PIG_CACHE_GC_ROOT"
)

func TestCacheLifecycleLockHelper(t *testing.T) {
	if os.Getenv(cacheLeaseHelperEnv) == "1" {
		lease, err := AcquireUsageLease(os.Getenv(cacheLeaseEntryEnv))
		if err != nil {
			os.Exit(2)
		}
		defer func() { _ = lease.Release() }()
		_ = os.WriteFile(os.Getenv(cacheLeaseReadyEnv), []byte("ready"), 0o600)
		for {
			time.Sleep(time.Hour)
		}
	} else if os.Getenv(cacheGCHelperEnv) == "1" {
		root := os.Getenv(cacheGCRootEnv)
		if err := os.MkdirAll(root, 0o755); err != nil {
			os.Exit(2)
		}
		lock := flock.New(filepath.Join(root, ".gc.lock"))
		if err := lock.Lock(); err != nil {
			os.Exit(2)
		}
		defer func() { _ = lock.Unlock() }()
		_ = os.WriteFile(os.Getenv(cacheLeaseReadyEnv), []byte("ready"), 0o600)
		for {
			time.Sleep(time.Hour)
		}
	}
	t.Skip("helper process entry point")
}

func TestKilledProcessReleasesUsageLease(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	entry := writeLifecycleEntry(t, root, "ext", "leased", now.Add(-time.Hour))
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCacheLifecycleLockHelper$")
	cmd.Env = append(os.Environ(), cacheLeaseHelperEnv+"=1", cacheLeaseEntryEnv+"="+entry, cacheLeaseReadyEnv+"="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	waitForLifecycleFile(t, ready)
	report, err := InspectCache(CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleClasses(report)[entry] != CacheActive {
		t.Fatalf("live helper lease was not active: %#v", report)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	report, err = InspectCache(CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleClasses(report)[entry] == CacheActive {
		t.Fatal("killed process retained usage lease")
	}
}

func TestCacheGCWaitsForOtherProcessLock(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry=%t", dryRun), func(t *testing.T) {
			now := time.Unix(2_000_000_000, 0)
			root := t.TempDir()
			writeLifecycleEntry(t, root, "ext", "expired", now.Add(-31*24*time.Hour))
			ready := filepath.Join(t.TempDir(), "ready")
			cmd := exec.Command(os.Args[0], "-test.run=^TestCacheLifecycleLockHelper$")
			cmd.Env = append(os.Environ(), cacheGCHelperEnv+"=1", cacheGCRootEnv+"="+root, cacheLeaseReadyEnv+"="+ready)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			stop := func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}
			t.Cleanup(stop)
			waitForLifecycleFile(t, ready)
			done := make(chan error, 1)
			go func() {
				defer close(done)
				_, err := PruneCaches(CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }, DryRun: dryRun})
				done <- err
			}()
			t.Cleanup(func() {
				stop()
				for range done {
				}
			})
			select {
			case err := <-done:
				t.Fatalf("second GC did not serialize behind process lock: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(testbudget.Wait(t)):
				t.Fatal("GC did not continue after lock owner died")
			}
		})
	}
}

func waitForLifecycleFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper did not create %s", path)
}

func TestCacheLifecycleRetentionBoundaryKeepsExactlyThirtyDays(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	entry := writeLifecycleEntry(t, root, "ext", "boundary", now.Add(-30*24*time.Hour))
	report, err := PruneCaches(CacheLifecycleOptions{CacheRoot: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleClasses(report)[entry] != CacheInactiveRetained {
		t.Fatalf("retention boundary class = %q, want retained", lifecycleClasses(report)[entry])
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("entry at retention boundary was removed: %v", err)
	}
}

func TestCacheLifecycleRemovesObsoleteSDKFingerprintWithoutChangingReadyMetadata(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	entry := writeLifecycleEntry(t, root, "ext", "obsolete", now.Add(-time.Hour))
	readyPath := filepath.Join(entry, cellReadyFile)
	readyBefore, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, ".sdk-fingerprint"), []byte("old-sdk"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := InspectCache(CacheLifecycleOptions{
		CacheRoot: root, Now: func() time.Time { return now }, LiveFingerprints: map[string]struct{}{"current-sdk": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if classes := lifecycleClasses(report); classes[entry] != CacheInactiveExpired {
		t.Fatalf("obsolete fingerprint class = %q", classes[entry])
	}
	readyAfter, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(readyAfter) != string(readyBefore) {
		t.Fatal("cache inspection modified ready.json")
	}
}

func TestCacheLifecycleHoldsBuildLockThroughRename(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	root := t.TempDir()
	entry := writeLifecycleEntry(t, root, "ext", "fixture-deadbeef", now.Add(-31*24*time.Hour))
	buildLockWasAvailable := false
	usageLockWasAvailable := false
	report, err := PruneCaches(CacheLifecycleOptions{
		CacheRoot: root,
		Now:       func() time.Time { return now },
		Rename: func(source, target string) error {
			competingBuild := flock.New(cacheBuildLockPath(entry))
			locked, lockErr := competingBuild.TryLock()
			if lockErr != nil {
				return lockErr
			}
			buildLockWasAvailable = locked
			if locked {
				_ = competingBuild.Unlock()
			}
			competingUsage := flock.New(cacheLockPath(entry, ".usage.lock"))
			locked, lockErr = competingUsage.TryRLock()
			if lockErr != nil {
				return lockErr
			}
			usageLockWasAvailable = locked
			if locked {
				_ = competingUsage.Unlock()
			}
			return os.Rename(source, target)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if buildLockWasAvailable {
		t.Fatal("GC did not hold the entry build lock through rename")
	}
	if usageLockWasAvailable {
		t.Fatal("GC did not hold the entry usage lock through rename")
	}
	if report.Removed != 1 {
		t.Fatalf("report = %#v", report)
	}
}
