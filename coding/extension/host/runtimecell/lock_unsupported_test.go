package runtimecell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/filelock"
)

// noLock is the lock of a file system that refuses file locks: an NFS mount without a lock manager answers every flock with ENOLCK.
type noLock struct{ path string }

func (l noLock) fail() error { return &os.PathError{Op: "flock", Path: l.path, Err: syscall.ENOLCK} }

func (l noLock) TryLock() (bool, error) { return false, l.fail() }

func (l noLock) TryRLock() (bool, error) { return false, l.fail() }

func (l noLock) TryLockContext(context.Context, time.Duration) (bool, error) {
	return false, l.fail()
}

func (l noLock) TryRLockContext(context.Context, time.Duration) (bool, error) {
	return false, l.fail()
}

func (l noLock) RLock() error { return l.fail() }

func (l noLock) Unlock() error { return nil }

// withoutFileLocks makes every cache lock fail as on a file system that has none, for the rest of the test.
func withoutFileLocks(t *testing.T) {
	t.Helper()
	previous := filelock.New
	filelock.New = func(path string) filelock.Lock { return noLock{path} }
	t.Cleanup(func() { filelock.New = previous })
}

func requireExplainsMissingFileLocks(t *testing.T, err error, dir string) {
	t.Helper()
	if err == nil {
		t.Fatal("no error on a file system without file locks")
	}
	for _, want := range []string{"does not support file locks", dir, "PIG_HOME"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if !errors.Is(err, syscall.ENOLCK) {
		t.Errorf("error %q does not wrap the lock failure (ENOLCK)", err)
	}
}

// The build of any cell takes a per-digest lock, so a cache on a file system without file locks cannot build anything. The failure
// must name the cause and the remedy, not only the errno of a lock call.
func TestPublishArtifactExplainsAFileSystemWithoutFileLocks(t *testing.T) {
	withoutFileLocks(t)
	cacheRoot := t.TempDir()
	finalDir := filepath.Join(cacheRoot, "cells", "go", strings.Repeat("b", 64))
	built := false
	_, err := PublishArtifact(t.Context(), finalDir, "runner", strings.Repeat("b", 64), "go", func(scratch string) (string, error) {
		built = true
		return "", errors.New("must not build")
	})
	requireExplainsMissingFileLocks(t, err, filepath.Dir(finalDir))
	if built {
		t.Fatal("the build ran without its lock")
	}
}

// A lease on a published entry is a shared lock; the start of its extension fails on it.
func TestAcquireArtifactUsageLeaseExplainsAFileSystemWithoutFileLocks(t *testing.T) {
	_, finalDir := publishAgedCell(t, t.TempDir())
	withoutFileLocks(t)
	lease, err := AcquireArtifactUsageLease(filepath.Join(finalDir, "runner"))
	if lease != nil {
		t.Fatalf("lease = %v on a file system without file locks", lease)
	}
	requireExplainsMissingFileLocks(t, err, filepath.Dir(finalDir))
}

// The collector serialises itself with an exclusive lock on the cache root.
func TestPruneCachesExplainsAFileSystemWithoutFileLocks(t *testing.T) {
	cacheRoot := t.TempDir()
	withoutFileLocks(t)
	_, err := PruneCaches(CacheLifecycleOptions{CacheRoot: cacheRoot})
	requireExplainsMissingFileLocks(t, err, cacheRoot)
}
