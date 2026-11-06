package pilock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestAuthLocksShareOneLockAcrossSymlinkedPaths ports Pi 081a6e87f's test (packages/coding-agent/test/auth-storage.test.ts "a symlinked auth.json path waits for the lock held on the real file", #11655): a lock held on the real file blocks the cancellable and the synchronous auth loops reached through a symlink alias of it. Without the fix the alias had its own lock directory and both loops entered.
func TestAuthLocksShareOneLockAcrossSymlinkedPaths(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "auth.json")
	alias := filepath.Join(dir, "alias.json")
	if err := os.WriteFile(real, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, real, alias)
	// Pi holds the real file with lockfile.lock(authJsonPath, { realpath: false }): the lock directory is next to the real file.
	held, err := acquire(real, asyncStale)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if lock, err := Acquire(ctx, alias); err == nil {
		_ = lock.Release()
		t.Fatal("Acquire through a symlink alias entered while the real file's lock was held")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire through a symlink alias returned %v, want the cancellation", err)
	}

	lock, err := AcquireAuthSync(alias)
	if err == nil {
		_ = lock.Release()
		t.Fatal("AcquireAuthSync through a symlink alias entered while the real file's lock was held")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("AcquireAuthSync through a symlink alias returned %v, want ELOCKED", err)
	}
	if _, statErr := os.Lstat(alias + ".lock"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a lock directory was made beside the alias: %v", statErr)
	}
}

// TestAuthLockOfASymlinkedFileInAnotherDirectoryIsTheRealFilesLock covers the shape of the report (#11655): an agent directory whose auth.json is a symlink to a shared file in another directory. Both paths lock the real file, and a release through either frees it for the other.
func TestAuthLockOfASymlinkedFileInAnotherDirectoryIsTheRealFilesLock(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "shared")
	agent := filepath.Join(root, "agent")
	for _, dir := range []string{shared, agent} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(shared, "auth.json")
	if err := os.WriteFile(real, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(agent, "auth.json")
	testenv.Symlink(t, real, link)

	viaLink, err := AcquireAuthSync(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(real + ".lock"); err != nil {
		t.Fatalf("the lock is not beside the real file: %v", err)
	}
	if lock, err := AcquireAuthSync(real); err == nil {
		_ = lock.Release()
		t.Fatal("the real path entered while the symlinked path held the lock")
	} else if !errors.Is(err, ErrLocked) {
		t.Fatalf("real path returned %v, want ELOCKED", err)
	}
	if err := viaLink.Release(); err != nil {
		t.Fatal(err)
	}
	viaReal, err := Acquire(t.Context(), real)
	if err != nil {
		t.Fatal(err)
	}
	if lock, err := AcquireAuthSync(link); err == nil {
		_ = lock.Release()
		t.Fatal("the symlinked path entered while the real path held the lock")
	}
	if err := viaReal.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestSettingsLockStaysOnTheGivenPath pins what Pi 081a6e87f left alone: settings and trust keep realpath: false, so the lock sits beside the path as given.
func TestSettingsLockStaysOnTheGivenPath(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "settings.json")
	alias := filepath.Join(dir, "alias.json")
	if err := os.WriteFile(real, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, real, alias)
	lock, err := AcquireSync(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if _, err := os.Lstat(alias + ".lock"); err != nil {
		t.Fatalf("the settings lock is not beside the path as given: %v", err)
	}
}
