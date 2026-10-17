package pilock

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// setLockPlatform is the Pi test's setPlatform: it replaces process.platform until the test ends.
func setLockPlatform(t *testing.T, platform string) {
	t.Helper()
	previous := lockPlatform
	lockPlatform = platform
	t.Cleanup(func() { lockPlatform = previous })
}

// recordLockWaits replaces Math.random with 0, as the Pi tests do, and records every wait of the retry loops instead of sleeping.
func recordLockWaits(t *testing.T) *[]time.Duration {
	t.Helper()
	previousRandom, previousSync, previousAsync := lockRandom, syncSleep, asyncSleep
	waits := &[]time.Duration{}
	lockRandom = func() float64 { return 0 }
	syncSleep = func(d time.Duration) { *waits = append(*waits, d) }
	asyncSleep = func(_ context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		return nil
	}
	t.Cleanup(func() { lockRandom, syncSleep, asyncSleep = previousRandom, previousSync, previousAsync })
	return waits
}

// writeAuthJSON is the Pi test's writeAuthJson({}).
func writeAuthJSON(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// epermAttempt is a lockfile.lock attempt rejected with EPERM: the platform mkdir error Node reports as EPERM (ERROR_ACCESS_DENIED on Windows, which a delete-pending lock directory returns).
func epermAttempt(t *testing.T, path string) error {
	t.Helper()
	return &fs.PathError{Op: "mkdir", Path: path + ".lock", Err: epermMkdirError(t)}
}

// acquiredAndReleased checks that the update ran under a held lock and that the lock was released once.
func acquiredAndReleased(t *testing.T, path string, lock *Lock, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("err = %v, want the lock after the retry", err)
	}
	if err := lock.Check(); err != nil {
		t.Fatalf("Check = %v, want the lock held for the update", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path + ".lock"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lock directory after release: %v", err)
	}
}

// packages/coding-agent/test/auth-storage.test.ts "Windows EPERM on lock acquisition" (Pi 1.2.0, #10801): a delete-pending lock directory on Windows makes mkdir fail with EPERM briefly. FileAuthStorageBackend retries EPERM only when process.platform is win32 (auth-storage.ts:24-29 getRetryableLockErrorCode); acquireLockAsync caps those retries at MAX_EPERM_RETRIES (5) apart from its 30 s deadline (auth-storage.ts:137-147), and acquireLockSyncWithRetry retries them within its ten attempts (auth-storage.ts:81-95). Each lockfile.lock spy call is one mkdir attempt here.
func TestAuthLockWindowsEPERMUpstream(t *testing.T) {
	t.Run("retries transient EPERM on Windows (async)", func(t *testing.T) {
		setLockPlatform(t, "windows")
		path := writeAuthJSON(t)
		waits := recordLockWaits(t)
		calls := stubMkdir(t, func(call int, real func() error) error {
			if call == 1 {
				return epermAttempt(t, path)
			}
			return real()
		})
		lock, err := Acquire(t.Context(), path)
		if got := calls.Load(); got != 2 {
			t.Fatalf("lock attempts = %d, want 2", got)
		}
		acquiredAndReleased(t, path, lock, err)
		// Math.random() is 0: the first backoff is 10 ms.
		if want := []time.Duration{10 * time.Millisecond}; !slices.Equal(*waits, want) {
			t.Fatalf("waits = %v, want %v", *waits, want)
		}
	})

	t.Run("surfaces persistent EPERM on Windows after a bounded number of retries (async)", func(t *testing.T) {
		setLockPlatform(t, "windows")
		path := writeAuthJSON(t)
		waits := recordLockWaits(t)
		calls := stubMkdir(t, func(int, func() error) error { return epermAttempt(t, path) })
		lock, err := Acquire(t.Context(), path)
		if lock != nil {
			_ = lock.Release()
			t.Fatal("acquired, want the EPERM rejection")
		}
		if code := nodeerrno.ErrorCode(err); code != "EPERM" || errors.Is(err, ErrLocked) {
			t.Fatalf("err = %v (code %q), want code EPERM", err, code)
		}
		if got := calls.Load(); got != 6 {
			t.Fatalf("lock attempts = %d, want 6", got)
		}
		want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond}
		if !slices.Equal(*waits, want) {
			t.Fatalf("waits = %v, want %v", *waits, want)
		}
	})

	t.Run("does not retry EPERM on other platforms (async)", func(t *testing.T) {
		setLockPlatform(t, "linux")
		path := writeAuthJSON(t)
		recordLockWaits(t)
		calls := stubMkdir(t, func(call int, real func() error) error {
			if call == 1 {
				return epermAttempt(t, path)
			}
			return real()
		})
		lock, err := Acquire(t.Context(), path)
		if lock != nil {
			_ = lock.Release()
			t.Fatal("acquired, want the EPERM rejection")
		}
		if code := nodeerrno.ErrorCode(err); code != "EPERM" {
			t.Fatalf("err = %v (code %q), want code EPERM", err, code)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("lock attempts = %d, want 1", got)
		}
	})

	t.Run("retries transient EPERM on Windows (sync)", func(t *testing.T) {
		setLockPlatform(t, "windows")
		path := writeAuthJSON(t)
		waits := recordLockWaits(t)
		calls := stubMkdir(t, func(call int, real func() error) error {
			if call == 1 {
				return epermAttempt(t, path)
			}
			return real()
		})
		lock, err := AcquireAuthSync(path)
		if got := calls.Load(); got != 2 {
			t.Fatalf("lock attempts = %d, want 2", got)
		}
		acquiredAndReleased(t, path, lock, err)
		if want := []time.Duration{SyncDelay}; !slices.Equal(*waits, want) {
			t.Fatalf("waits = %v, want %v", *waits, want)
		}
	})
}

// Pi 1.2.0 changed only auth-storage.ts: settings-manager.ts acquireLockSyncWithRetry (lines 195-215) and trust-manager.ts acquireTrustLockSync (lines 137-164) still throw every code but ELOCKED, so AcquireSync surfaces EPERM after one attempt even on Windows.
func TestSettingsAndTrustLockDoNotRetryWindowsEPERM(t *testing.T) {
	setLockPlatform(t, "windows")
	recordLockWaits(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	calls := stubMkdir(t, func(int, func() error) error { return epermAttempt(t, path) })
	lock, err := AcquireSync(path)
	if lock != nil {
		_ = lock.Release()
		t.Fatal("acquired, want the EPERM rejection")
	}
	if code := nodeerrno.ErrorCode(err); code != "EPERM" {
		t.Fatalf("err = %v (code %q), want code EPERM", err, code)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("lock attempts = %d, want 1", got)
	}
}
