package pilock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Pi 0.87.1 auth-storage.ts:69-93,119-155 and proper-lockfile 4.1.2 lib/lockfile.js:51-85 use mtime, not file type, to decide contention. Only PiG recovers stale empty regular sidecars.
func TestLockMtimeThresholds(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		for _, mode := range []struct {
			name  string
			stale time.Duration
		}{
			{"sync", SyncStale},
			{"async", asyncStale},
		} {
			for _, age := range []struct {
				name string
				age  time.Duration
			}{
				{"fresh", 0},
				{"future", -time.Minute},
				{"between-sync-and-async", 2 * SyncStale},
				{"stale", 2 * asyncStale},
			} {
				t.Run(kind+"/"+mode.name+"/"+age.name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "auth.json")
					if kind == "file" {
						if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
							t.Fatal(err)
						}
					} else if err := os.Mkdir(path+".lock", 0o700); err != nil {
						t.Fatal(err)
					}
					mtime := time.Now().Add(-age.age)
					if err := os.Chtimes(path+".lock", mtime, mtime); err != nil {
						t.Fatal(err)
					}
					before, err := os.Lstat(path + ".lock")
					if err != nil {
						t.Fatal(err)
					}
					lease, err := tryAcquire(t.Context(), path, mode.stale)
					if lease != nil {
						defer func() {
							if err := lease.Release(); err != nil {
								t.Error(err)
							}
						}()
					}
					if age.age > mode.stale {
						if err != nil {
							t.Fatal(err)
						}
						if info, err := os.Lstat(path + ".lock"); err != nil || !info.IsDir() {
							t.Fatalf("acquired lock: %v, %v", info, err)
						}
						return
					}
					if !errors.Is(err, ErrLocked) {
						t.Fatalf("fresh lock acquired: %v", err)
					}
					after, err := os.Lstat(path + ".lock")
					if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
						t.Fatalf("contended lock changed: %v", err)
					}
				})
			}
		}
	}
}

func TestFreshLegacySidecarRetriesAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	lease, err := AcquireSync(path)
	if lease != nil {
		_ = lease.Release()
	}
	if !errors.Is(err, ErrLocked) || time.Since(start) < (SyncAttempts-1)*SyncDelay {
		t.Fatalf("fresh file bypassed synchronous contention: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	lease, err = Acquire(ctx, path)
	if lease != nil {
		_ = lease.Release()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fresh file bypassed cancellable contention: %v", err)
	}
}

func TestLegacyRefreshBeforeReclaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * asyncStale)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := observeLegacy(path, info)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	if err := replaceLegacy(path, observed, SyncStale); !errors.Is(err, ErrLocked) {
		t.Fatalf("reclaimed a refreshed file: %v", err)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("refreshed file changed: %v, %v", info, err)
	}
}
