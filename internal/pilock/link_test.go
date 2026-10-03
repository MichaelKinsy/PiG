package pilock

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// linkLockPath makes path+".lock" a link: to a fresh or stale directory, a fresh or stale empty file, or a missing target. It returns the target.
func linkLockPath(t *testing.T, path, kind string) string {
	t.Helper()
	target := filepath.Join(filepath.Dir(path), "target")
	old := time.Now().Add(-time.Hour)
	switch kind {
	case "fresh-directory", "stale-directory", "dangling":
		if err := os.Mkdir(target, 0o777); err != nil {
			t.Fatal(err)
		}
		if kind == "stale-directory" {
			if err := os.Chtimes(target, old, old); err != nil {
				t.Fatal(err)
			}
		}
		testenv.RequireDirectoryLink(t, target, path+".lock")
		if kind == "dangling" {
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
		}
	case "fresh-file", "stale-file":
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if kind == "stale-file" {
			if err := os.Chtimes(target, old, old); err != nil {
				t.Fatal(err)
			}
		}
		testenv.Symlink(t, target, path+".lock")
	default:
		t.Fatalf("unknown link kind %q", kind)
	}
	return target
}

// linkOutcomes is what Pi's pinned proper-lockfile 4.1.2 does when the lock path is a link (TestProperLockfileOnLinkLockPathUpstream runs it). acquireLock stats the lock path with fs.stat (lib/lockfile.js:56), which follows the link, and judges only the target's mtime. A dangling link stats ENOENT, so mkdir is retried and reports EEXIST again: ELOCKED. A stale target is removed with rmdir on the link (lib/lockfile.js:71-79,88-96). Windows removes a directory link and libuv reports rmdir of a file link as ENOENT (src/win/fs.c fs__unlink_rmdir), which proper-lockfile ignores before mkdir reports EEXIST; Unix rmdir(2) of any symbolic link fails with ENOTDIR.
func linkOutcome(kind string) string {
	switch kind {
	case "stale-directory":
		// TestProperLockfileOnLinkLockPathUpstream/stale-directory/{sync,async}: fs.stat follows the link to the stale target (lib/lockfile.js:56,67-69), and removeLock's rmdir removes the link (lib/lockfile.js:71-79,88-96). Windows removes a directory link and the retried mkdir takes the lock (lib/lockfile.js:78); Unix rmdir(2) of a symbolic link fails with ENOTDIR.
		if runtime.GOOS == "windows" {
			return "acquired"
		}
		return "ENOTDIR"
	case "stale-file":
		if runtime.GOOS == "windows" {
			return "ELOCKED"
		}
		return "ENOTDIR"
	}
	return "ELOCKED"
}

var linkKinds = []string{"fresh-directory", "stale-directory", "dangling", "fresh-file", "stale-file"}

// linkAcquirers bound contention quickly: Pi's synchronous retry loop, and one attempt of the asynchronous path, which shares acquireLock.
var linkAcquirers = []acquirer{
	{"sync", AcquireSync},
	{"single-attempt", func(path string) (*Lock, error) {
		return AcquireWithOptions(context.Background(), path, AcquireOptions{Stale: asyncStale, Update: asyncStale / 2, Retry: time.Millisecond})
	}},
}

func TestAcquireFollowsALinkLockPathAsProperLockfileDoes(t *testing.T) {
	for _, kind := range linkKinds {
		for _, a := range linkAcquirers {
			t.Run(kind+"/"+a.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "auth.json")
				target := linkLockPath(t, path, kind)
				targetBefore, targetErr := os.Lstat(target)
				lock, err := a.run(path)
				want := linkOutcome(kind)
				switch want {
				case "acquired":
					if err != nil {
						t.Fatalf("err = %v, want the lock after removing the stale link, as TestProperLockfileOnLinkLockPathUpstream/%s does", err, kind)
					}
					if info, err := os.Lstat(path + ".lock"); err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
						t.Fatalf("lock path after takeover = %v, %v; want a real directory", info, err)
					}
					if err := lock.Release(); err != nil {
						t.Fatal(err)
					}
				case "ELOCKED":
					if lock != nil {
						_ = lock.Release()
					}
					if !errors.Is(err, ErrLocked) {
						t.Fatalf("err = %v, want ErrLocked", err)
					}
				case "ENOTDIR":
					if lock != nil {
						_ = lock.Release()
					}
					if !errors.Is(err, syscall.ENOTDIR) || errors.Is(err, ErrLocked) {
						t.Fatalf("err = %v, want rmdir's ENOTDIR and not ErrLocked", err)
					}
				}
				if want != "acquired" {
					if info, err := os.Lstat(path + ".lock"); err != nil || info.Mode()&fs.ModeSymlink == 0 {
						t.Fatalf("link lock path after %s = %v, %v; want the link kept", want, info, err)
					}
				}
				// The link's target is never removed.
				if targetErr == nil {
					if after, err := os.Lstat(target); err != nil || !os.SameFile(targetBefore, after) {
						t.Fatalf("link target changed: %v", err)
					}
				}
			})
		}
	}
}

// properLockfileModule is Pi's pinned proper-lockfile package.
func properLockfileModule(t *testing.T) string {
	t.Helper()
	module, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "proper-lockfile"))
	if err != nil {
		t.Fatal(err)
	}
	return module
}

// runProperLockfile runs proper-lockfile's lockSync or lock on file and returns "acquired" or the rejection's "code=<code> message=<message>".
func runProperLockfile(t *testing.T, file, api string) string {
	t.Helper()
	const script = `
const [module, file, api] = process.argv.slice(1);
const lockfile = require(module);
const report = (err) => console.log(err ? 'code=' + err.code + ' message=' + err.message : 'acquired');
if (api === 'sync') {
  try { lockfile.lockSync(file, { realpath: false })(); report(); } catch (err) { report(err); }
} else {
  lockfile.lock(file, { realpath: false }).then((release) => release().then(() => report()), report);
}
`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, properLockfileModule(t), file, api).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// The oracle for TestAcquireFollowsALinkLockPathAsProperLockfileDoes, on the same fixtures.
func TestProperLockfileOnLinkLockPathUpstream(t *testing.T) {
	for _, kind := range linkKinds {
		for _, api := range []string{"sync", "async"} {
			t.Run(kind+"/"+api, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "auth.json")
				linkLockPath(t, path, kind)
				got := runProperLockfile(t, path, api)
				want := linkOutcome(kind)
				if want != "acquired" {
					want = "code=" + want
				}
				if !strings.HasPrefix(got, want) || (want != "acquired" && !strings.HasPrefix(got, want+" ")) {
					t.Fatalf("proper-lockfile %s on a %s link: %q, want %s", api, kind, got, want)
				}
			})
		}
	}
}
