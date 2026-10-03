package pilock

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// v0.2.0 (c318b771) settings.go:2343 and trust_manager.go:210 use
// gofrs/flock v0.13.0. Auth/model stores use the same nonblocking flock or
// one-byte LockFileEx, closing each failed attempt and leaving an empty file.
func legacyFixture(t *testing.T, path string) *flock.Flock {
	t.Helper()
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ageLegacyFixture(t, path+".lock")
	lock := flock.New(path + ".lock")
	ok, err := lock.TryLock()
	if err != nil || !ok {
		t.Fatalf("legacy lock: %v, %v", ok, err)
	}
	t.Cleanup(func() {
		if err := lock.Unlock(); err != nil {
			t.Error(err)
		}
	})
	return lock
}

// An old writer can still own a stale sidecar because its OS lock has no mtime heartbeat.
func ageLegacyFixture(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-2 * asyncStale)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySidecarUpgrade(t *testing.T) {
	for _, name := range []string{"auth.json", "models-store.json", "settings.json", "trust.json"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			old := legacyFixture(t, path)
			if err := old.Unlock(); err != nil {
				t.Fatal(err)
			}
			lease, err := AcquireSync(path)
			if err != nil {
				t.Fatal(err)
			}
			if info, err := os.Lstat(path + ".lock"); err != nil || !info.IsDir() {
				t.Fatalf("upgraded lock: %v, %v", info, err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("released lock remains: %v", err)
			}
		})
	}
}

func TestHeldLegacySidecarUsesNormalTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	legacyFixture(t, path)
	before, err := os.Lstat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	lease, err := AcquireSync(path)
	if lease != nil {
		_ = lease.Release()
	}
	if !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "older PiG") {
		t.Fatalf("held legacy lock: %v", err)
	}
	if time.Since(start) < (SyncAttempts-1)*SyncDelay {
		t.Fatal("legacy contention bypassed the normal retry budget")
	}
	after, err := os.Lstat(path + ".lock")
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("live legacy lock replaced: %v", err)
	}
}

func TestLegacySidecarCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	legacyFixture(t, path)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	lease, err := Acquire(ctx, path)
	if lease != nil {
		_ = lease.Release()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled legacy wait: %v", err)
	}
}

func TestLegacyUpgradePreservesOtherPaths(t *testing.T) {
	for _, kind := range []string{"nonempty", "directory", "symlink-file", "symlink-directory", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.json")
			target := filepath.Join(t.TempDir(), "target")
			switch kind {
			case "nonempty":
				if err := os.WriteFile(path+".lock", []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path+".lock", 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink-file", "symlink-directory", "dangling-symlink":
				switch kind {
				case "symlink-file":
					if err := os.WriteFile(target, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				case "symlink-directory":
					if err := os.Mkdir(target, 0o700); err != nil {
						t.Fatal(err)
					}
					old := time.Now().Add(-2 * SyncStale)
					if err := os.Chtimes(target, old, old); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "symlink-directory" {
					testenv.RequireDirectoryLink(t, target, path+".lock")
				} else {
					testenv.Symlink(t, target, path+".lock")
				}
			}
			before, err := os.Lstat(path + ".lock")
			if err != nil {
				t.Fatal(err)
			}
			lease, err := AcquireSync(path)
			if kind == "symlink-directory" && runtime.GOOS == "windows" {
				// Pi's protocol, not the legacy path: proper-lockfile 4.1.2 acquireLock stats the lock path with fs.stat, which follows the link (lib/lockfile.js:56), finds the target stale (lib/lockfile.js:67-69,84-86), and removes the link itself with rmdir (lib/lockfile.js:71-79, removeLock at 88-96). On Windows that removes the directory link, and the retried mkdir takes the lock (lib/lockfile.js:78). The oracle row is TestProperLockfileOnLinkLockPathUpstream/stale-directory/{sync,async}, which runs Pi's pinned proper-lockfile on a directory link to a stale target. The target stays. Unix rmdir(2) of the link fails with ENOTDIR, so the link is kept below.
				if err != nil {
					t.Fatalf("stale directory link: %v, want proper-lockfile's takeover", err)
				}
				if err := lease.Release(); err != nil {
					t.Fatal(err)
				}
				if info, err := os.Stat(target); err != nil || !info.IsDir() {
					t.Fatalf("link target after takeover: %v, %v", info, err)
				}
				return
			}
			if lease != nil {
				_ = lease.Release()
			}
			if err == nil {
				t.Fatal("unexpected takeover")
			}
			after, err := os.Lstat(path + ".lock")
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("replaced protected path: %v", err)
			}
		})
	}
}

// A regular file with content at the lock path is never a PiG legacy sidecar (D73 covers only an empty one), so it follows proper-lockfile 4.1.2: mkdir reports EEXIST, fs.stat reads the file (lib/lockfile.js:56), and a fresh file is ELOCKED (lib/lockfile.js:67-69). A stale file goes to removeLock's rmdir (lib/lockfile.js:71-79,88-96): libuv on Windows reports rmdir of a file as ENOENT (src/win/fs.c fs__unlink_rmdir), which proper-lockfile ignores before the repeated mkdir reports EEXIST, so the result is ELOCKED again; Unix rmdir(2) fails with ENOTDIR, which is thrown. The file is never removed. Each row runs Pi's pinned proper-lockfile on the file first, then PiG on the same path.
func TestAcquireOnNonEmptyFileFollowsProperLockfileUpstream(t *testing.T) {
	for _, stale := range []bool{false, true} {
		for i, api := range []string{"sync", "async"} {
			name := "fresh/" + api
			if stale {
				name = "stale/" + api
			}
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "auth.json")
				if err := os.WriteFile(path+".lock", []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if stale {
					old := time.Now().Add(-time.Hour)
					if err := os.Chtimes(path+".lock", old, old); err != nil {
						t.Fatal(err)
					}
				}
				want := "ELOCKED"
				if stale && runtime.GOOS != "windows" {
					want = "ENOTDIR"
				}
				pi := runProperLockfile(t, path, api)
				message, ok := strings.CutPrefix(pi, "code="+want+" message=")
				if !ok {
					t.Fatalf("proper-lockfile %s on a %s non-empty file: %q, want %s", api, name, pi, want)
				}
				if data, err := os.ReadFile(path + ".lock"); err != nil || string(data) != "keep" {
					t.Fatalf("proper-lockfile changed the file: %q, %v", data, err)
				}

				lock, err := linkAcquirers[i].run(path)
				if lock != nil {
					_ = lock.Release()
					t.Fatal("acquired a lock path holding a non-empty file")
				}
				switch want {
				case "ELOCKED":
					if !errors.Is(err, ErrLocked) || errors.Is(err, ErrLegacyLocked) {
						t.Fatalf("err = %v, want ErrLocked", err)
					}
				case "ENOTDIR":
					if !errors.Is(err, syscall.ENOTDIR) || errors.Is(err, ErrLocked) {
						t.Fatalf("err = %v, want rmdir's ENOTDIR", err)
					}
				}
				if err.Error() != message {
					t.Fatalf("message = %q, want proper-lockfile's %q", err.Error(), message)
				}
				if data, err := os.ReadFile(path + ".lock"); err != nil || string(data) != "keep" {
					t.Fatalf("PiG changed the file: %q, %v", data, err)
				}
			})
		}
	}
}

func TestLegacyLockProcessHelper(t *testing.T) {
	path := os.Getenv("PIG_TEST_LEGACY_LOCK_PATH")
	if path == "" {
		return
	}
	old := legacyFixture(t, path)
	if _, err := os.Stdout.WriteString("locked\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old-committed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := old.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySubprocessWriterUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestLegacyLockProcessHelper$")
	cmd.Env = append(os.Environ(), "PIG_TEST_LEGACY_LOCK_PATH="+path)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = cmd.Wait() })
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("holder: %q, %v", line, err)
	}
	if _, err := acquire(path, SyncStale); !errors.Is(err, ErrLegacyLocked) {
		t.Fatalf("holder not observed: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		lease, err := Acquire(t.Context(), path)
		if err != nil {
			done <- err
			return
		}
		data, err := os.ReadFile(path)
		if err == nil && string(data) != "old-committed" {
			err = errors.New("new process read before the legacy writer committed")
		}
		done <- errors.Join(err, lease.Release())
	}()
	if _, err := input.Write([]byte("commit\n")); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyReplacementRefusesChangedIdentity(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.json.lock")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			ageLegacyFixture(t, path)
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			// Observe as mkdir does, before the replacement.
			observed, err := observeLegacy(path, info)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, path+".saved"); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				ageLegacyFixture(t, path)
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				testenv.Symlink(t, path+".saved", path)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := replaceLegacy(path, observed, SyncStale); err == nil {
				t.Fatal("replaced another owner's path")
			} else if kind != "symlink" && !errors.Is(err, ErrLocked) {
				t.Fatalf("another upgrader's replacement must follow normal contention: %v", err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("changed replacement: %v", err)
			}
		})
	}
}

// Other upgraders stat, open and mkdir the lock name while one removes the sidecar. They must see the file or no name, never a delete-pending name that Windows reports as access denied.
func TestLegacyRemovalFreesLockNameBeforeClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openLegacy(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := lockLegacy(file); err != nil {
		t.Fatal(err)
	}
	if err := removeLegacy(file, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock name after removal, before close: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("mkdir after removal, before close: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "store.json.lock" || !entries[0].IsDir() {
		t.Fatalf("directory after close: %v, %v", entries, err)
	}
}

func TestLegacyWriterThenConcurrentDirectoryWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	old := legacyFixture(t, path)
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Prove contention before releasing the old writer; no scheduler sleep is
	// used as a proxy for ownership. Both new writers must preserve its update.
	if _, err := acquire(path, SyncStale); !errors.Is(err, ErrLocked) {
		t.Fatalf("held: %v", err)
	}
	const writers = 8
	done := make(chan error, writers)
	for range writers {
		go func() {
			lease, err := Acquire(t.Context(), path)
			if err != nil {
				done <- err
				return
			}
			data, readErr := os.ReadFile(path)
			writeErr := os.WriteFile(path, append(data, 'x'), 0o600)
			done <- errors.Join(readErr, writeErr, lease.Release())
		}()
	}
	if err := old.Unlock(); err != nil {
		t.Fatal(err)
	}
	for range writers {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old"+strings.Repeat("x", writers) {
		t.Fatalf("lost update: %s, %v", data, err)
	}
}
