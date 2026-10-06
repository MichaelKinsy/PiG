package pilock

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// markBeingRemoved puts dir in the state another process's RemoveDirectoryW holds it in between marking it for deletion and closing its handle: the name is delete-pending, and opening or creating it fails with ERROR_ACCESS_DENIED (STATUS_DELETE_PENDING). The returned func closes the handle, which completes the removal.
func markBeingRemoved(t *testing.T, dir string) func() {
	t.Helper()
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.DELETE|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	// RemoveDirectoryW asks for POSIX semantics and falls back where the file system lacks them; both leave the name delete-pending until this handle closes.
	var flags [4]byte // FILE_DISPOSITION_INFO_EX.Flags, a little-endian ULONG.
	binary.LittleEndian.PutUint32(flags[:], windows.FILE_DISPOSITION_DELETE|windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfoEx, &flags[0], uint32(len(flags))); err != nil {
		deleteFile := byte(1)
		if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &deleteFile, 1); err != nil {
			_ = windows.CloseHandle(handle)
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(dir); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		_ = windows.CloseHandle(handle)
		t.Fatalf("setup: os.Lstat of a delete-pending directory = %v, want ERROR_ACCESS_DENIED", err)
	}
	closed := false
	finish := func() {
		if !closed {
			closed = true
			if err := windows.CloseHandle(handle); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(finish)
	return finish
}

// proper-lockfile 4.1.2 lib/lockfile.js:56-69 stats the held directory after mkdir reports EEXIST. When the holder's removal is in flight, Node's fs.stat still reads the directory (libuv src/win/fs.c fs__stat_impl_from_path falls back to the parent directory entry on ERROR_ACCESS_DENIED), so proper-lockfile reports ELOCKED and Pi's auth-storage.ts retry loops wait for the removal to finish. PiG reports the same contention and acquires on the next attempt.
func TestAcquireTreatsLockBeingRemovedAtStatAsHeld(t *testing.T) {
	for _, a := range acquirers {
		t.Run(a.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.Mkdir(path+".lock", 0o777); err != nil {
				t.Fatal(err)
			}
			var finishRemoval func()
			calls := stubMkdir(t, func(call int, real func() error) error {
				switch call {
				case 1:
					err := real()
					if !isEEXIST(err) {
						t.Errorf("mkdir of the held directory = %v, want EEXIST", err)
					}
					// The holder starts removing its directory between our mkdir and stat.
					finishRemoval = markBeingRemoved(t, path+".lock")
					return err
				case 2:
					finishRemoval()
				}
				return real()
			})
			lock, err := a.run(path)
			if err != nil {
				t.Fatalf("err = %v, want the lock after the in-flight removal", err)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("mkdir calls = %d, want 2 (EEXIST with the directory being removed, then success)", got)
			}
			if err := lock.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// When the removal is in flight at mkdir itself, Node's fs.mkdir fails with EPERM (libuv maps ERROR_ACCESS_DENIED to UV_EPERM), proper-lockfile passes it through (lib/lockfile.js:47-48) and Pi's retry loops throw it. PiG surfaces the same failure after one attempt.
func TestAcquireSurfacesLockBeingRemovedAtMkdir(t *testing.T) {
	for _, a := range acquirers {
		t.Run(a.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.Mkdir(path+".lock", 0o777); err != nil {
				t.Fatal(err)
			}
			markBeingRemoved(t, path+".lock")
			calls := stubMkdir(t, func(_ int, real func() error) error { return real() })
			lock, err := a.run(path)
			if err == nil {
				_ = lock.Release()
				t.Fatal("acquired a directory that is being removed")
			}
			if errors.Is(err, ErrLocked) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("err = %v, want ERROR_ACCESS_DENIED and not ErrLocked", err)
			}
			if want := "EPERM: operation not permitted, mkdir '" + path + ".lock'"; err.Error() != want {
				t.Fatalf("message = %q, want proper-lockfile's %q", err.Error(), want)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("mkdir calls = %d, want 1", got)
			}
		})
	}
}

// The MCP OAuth refresh lock and the other locks that pass proper-lockfile a retries option retry every failed attempt (TestAcquireWithOptionsRetriesEveryAttemptErrorUpstream), so a mkdir that a real delete-pending lock directory refuses waits for the removal to finish and then takes the lock.
func TestAcquireWithOptionsWaitsOutLockBeingRemovedAtMkdir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-auth-refresh")
	if err := os.Mkdir(path+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	finishRemoval := markBeingRemoved(t, path+".lock")
	calls := stubMkdir(t, func(call int, real func() error) error {
		if call == 1 {
			err := real()
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Errorf("mkdir of a delete-pending directory = %v, want ERROR_ACCESS_DENIED", err)
			}
			// The other process's removal completes before the retry.
			finishRemoval()
			return err
		}
		return real()
	})
	lock, err := AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: time.Minute, Update: 30 * time.Second, Retry: time.Millisecond, Wait: 10 * time.Second, OnCompromised: func(error) {}})
	if err != nil {
		t.Fatalf("AcquireWithOptions = %v, want the lock once the removal finished", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("mkdir calls = %d, want 2", got)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

// makeHeldLockPath creates a held lock path: a fresh directory, a directory older than every stale threshold, or a directory link to a fresh directory.
func makeHeldLockPath(t *testing.T, lockPath, kind string) {
	t.Helper()
	switch kind {
	case "link":
		target := lockPath + ".target"
		if err := os.Mkdir(target, 0o777); err != nil {
			t.Fatal(err)
		}
		testenv.RequireDirectoryLink(t, target, lockPath)
		return
	case "stale", "fresh":
	default:
		t.Fatalf("unknown lock path kind %q", kind)
	}
	if err := os.Mkdir(lockPath, 0o777); err != nil {
		t.Fatal(err)
	}
	if kind == "stale" {
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(lockPath, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

// Two entries read from the parent directory still fail the attempt, as in Pi (see TestProperLockfileOnLockBeingRemovedUpstream):
//   - A stale directory: proper-lockfile removes it (lib/lockfile.js:71-79) and its rmdir fails with EPERM because the name is delete-pending. PiG's syscall.Rmdir fails with ERROR_ACCESS_DENIED.
//   - A directory link: fs.stat follows links, and libuv's fs__stat_directory keeps the original ERROR_ACCESS_DENIED (EPERM) for a reparse point when it does not lstat (src/win/fs.c).
//
// Neither is contention, so neither attempt is retried.
func TestAcquireSurfacesLockBeingRemovedAtStatThatIsStaleOrALink(t *testing.T) {
	for _, kind := range []string{"stale", "link"} {
		for _, a := range acquirers {
			t.Run(kind+"/"+a.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "auth.json")
				makeHeldLockPath(t, path+".lock", kind)
				calls := stubMkdir(t, func(call int, real func() error) error {
					if call > 1 {
						t.Errorf("mkdir call %d: the lock being removed was retried as held", call)
						return real()
					}
					err := real()
					if !isEEXIST(err) {
						t.Errorf("mkdir of the held lock path = %v, want EEXIST", err)
					}
					markBeingRemoved(t, path+".lock")
					return err
				})
				lock, err := a.run(path)
				if err == nil {
					_ = lock.Release()
					t.Fatal("acquired a lock path that is being removed")
				}
				if errors.Is(err, ErrLocked) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
					t.Fatalf("err = %v, want ERROR_ACCESS_DENIED and not ErrLocked", err)
				}
				call := map[string]string{"stale": "rmdir", "link": "stat"}[kind]
				if want := "EPERM: operation not permitted, " + call + " '" + path + ".lock'"; err.Error() != want {
					t.Fatalf("message = %q, want proper-lockfile's %q", err.Error(), want)
				}
				if got := calls.Load(); got != 1 {
					t.Fatalf("mkdir calls = %d, want 1", got)
				}
			})
		}
	}
}

// The oracle for the tests above: Pi's pinned proper-lockfile on this Windows file system, with the holder's removal in flight at mkdir or at the stat that follows EEXIST.
func TestProperLockfileOnLockBeingRemovedUpstream(t *testing.T) {
	module, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "proper-lockfile"))
	if err != nil {
		t.Fatal(err)
	}
	// The injected fs pauses after mkdir reports EEXIST so the test can start the removal before proper-lockfile stats the directory.
	const script = `
const fs = require('fs');
const path = require('path');
const [module, file, api, markers] = process.argv.slice(1);
const lockfile = require(module);
const pause = (err) => {
  if (!markers || !err || err.code !== 'EEXIST') return;
  fs.writeFileSync(path.join(markers, 'eexist'), '');
  const sleeper = new Int32Array(new SharedArrayBuffer(4));
  while (!fs.existsSync(path.join(markers, 'removing'))) Atomics.wait(sleeper, 0, 0, 1);
};
const hooked = { ...fs,
  mkdir(p, callback) { fs.mkdir(p, (err) => { pause(err); callback(err); }); },
  mkdirSync(p) { try { return fs.mkdirSync(p); } catch (err) { pause(err); throw err; } },
};
const report = (err) => console.log(err ? 'code=' + err.code + (err.code === 'ELOCKED' ? '' : ' message=' + err.message) : 'acquired');
if (api === 'sync') {
  try { lockfile.lockSync(file, { realpath: false, fs: hooked }); report(); } catch (err) { report(err); }
} else {
  lockfile.lock(file, { realpath: false, fs: hooked }).then(() => report(), report);
}
`
	// want is the report; %s stands for the lock path in Node's fs error message.
	for _, test := range []struct {
		step, kind string
		want       string
	}{
		{"mkdir", "fresh", "code=EPERM message=EPERM: operation not permitted, mkdir '%s'"},
		{"stat", "fresh", "code=ELOCKED"},
		{"stat", "stale", "code=EPERM message=EPERM: operation not permitted, rmdir '%s'"},
		{"stat", "link", "code=EPERM message=EPERM: operation not permitted, stat '%s'"},
	} {
		for _, api := range []string{"sync", "async"} {
			t.Run(test.step+"/"+test.kind+"/"+api, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "auth.json")
				makeHeldLockPath(t, path+".lock", test.kind)
				markers := ""
				if test.step == "mkdir" {
					markBeingRemoved(t, path+".lock")
				} else {
					markers = filepath.Join(root, "markers")
					if err := os.Mkdir(markers, 0o777); err != nil {
						t.Fatal(err)
					}
				}
				cmd := exec.CommandContext(t.Context(), "node", "-e", script, module, path, api, markers)
				var out strings.Builder
				cmd.Stdout, cmd.Stderr = &out, &out
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				if markers != "" {
					deadline := time.Now().Add(30 * time.Second)
					for {
						if _, err := os.Stat(filepath.Join(markers, "eexist")); err == nil {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("proper-lockfile did not reach mkdir EEXIST")
						}
						time.Sleep(time.Millisecond)
					}
					markBeingRemoved(t, path+".lock")
					if err := os.WriteFile(filepath.Join(markers, "removing"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := cmd.Wait(); err != nil {
					t.Fatalf("node: %v\n%s", err, out.String())
				}
				want := strings.ReplaceAll(test.want, "%s", path+".lock")
				if got := strings.TrimSpace(out.String()); got != want {
					t.Fatalf("proper-lockfile %s with the removal of a %s lock path in flight at %s: %q, want %q", api, test.kind, test.step, got, want)
				}
			})
		}
	}
}

// A link whose target directory is being removed: fs.stat follows the link, the target is delete-pending, and libuv's fs__stat_directory reads the link's own entry, a reparse point, so it keeps ERROR_ACCESS_DENIED (src/win/fs.c fs__stat_impl_from_path; the do_lstat=0 branch of fs__stat_directory). proper-lockfile passes the stat error through (lib/lockfile.js:56-65) as EPERM, and PiG surfaces ERROR_ACCESS_DENIED.
func TestAcquireSurfacesLinkToDirectoryBeingRemovedUpstream(t *testing.T) {
	for _, api := range []string{"sync", "async"} {
		t.Run(api, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			target := linkLockPath(t, path, "fresh-directory")
			markBeingRemoved(t, target)
			message := "EPERM: operation not permitted, stat '" + path + ".lock'"
			if got := runProperLockfile(t, path, api); got != "code=EPERM message="+message {
				t.Fatalf("proper-lockfile %s: %q, want EPERM with %q", api, got, message)
			}
			run := AcquireSync
			if api == "async" {
				run = func(path string) (*Lock, error) { return Acquire(t.Context(), path) }
			}
			lock, err := run(path)
			if err == nil {
				_ = lock.Release()
				t.Fatal("acquired through a link whose target is being removed")
			}
			if errors.Is(err, ErrLocked) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("err = %v, want ERROR_ACCESS_DENIED and not ErrLocked", err)
			}
			if err.Error() != message {
				t.Fatalf("message = %q, want proper-lockfile's %q", err.Error(), message)
			}
		})
	}
}

// runProperLockfileAfterMkdir runs proper-lockfile's lockSync or lock on file with an fs whose mkdir, once it has created the lock directory, calls whileMade in this process before proper-lockfile continues. It returns "acquired" or the rejection's "code=<code> message=<message>".
func runProperLockfileAfterMkdir(t *testing.T, file, api string, whileMade func(lock string)) string {
	t.Helper()
	const script = `
const fs = require('fs');
const path = require('path');
const [module, file, api, markers] = process.argv.slice(1);
const lockfile = require(module);
const sleeper = new Int32Array(new SharedArrayBuffer(4));
const handshake = (lock) => {
  fs.writeFileSync(path.join(markers, 'made.tmp'), lock); fs.renameSync(path.join(markers, 'made.tmp'), path.join(markers, 'made'));
  while (!fs.existsSync(path.join(markers, 'go'))) Atomics.wait(sleeper, 0, 0, 1);
};
const custom = {
  ...fs,
  mkdirSync: (lock, ...rest) => { const made = fs.mkdirSync(lock, ...rest); handshake(lock); return made; },
  mkdir: (lock, ...rest) => {
    const callback = rest.pop();
    fs.mkdir(lock, ...rest, (err) => { if (!err) handshake(lock); callback(err); });
  },
};
const report = (err) => console.log(err ? 'code=' + err.code + ' message=' + err.message : 'acquired');
if (api === 'sync') {
  try { lockfile.lockSync(file, { realpath: false, fs: custom })(); report(); } catch (err) { report(err); }
} else {
  lockfile.lock(file, { realpath: false, fs: custom }).then((release) => release().then(() => report()), report);
}
`
	markers := filepath.Join(t.TempDir(), "markers")
	if err := os.Mkdir(markers, 0o777); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, properLockfileModule(t), file, api, markers)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if lock, err := os.ReadFile(filepath.Join(markers, "made")); err == nil {
			whileMade(string(lock))
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("proper-lockfile did not create its lock: %s", out.String())
		}
		time.Sleep(time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(markers, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("node: %v\n%s", err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// When the mtime probe fails right after mkdir, proper-lockfile removes the new directory with rmdir, ignores that rmdir's result, and rejects with the probe's error (lib/lockfile.js:29-43, lib/mtime-precision.js). A directory that another process starts removing then makes both the utime and the rmdir fail with EPERM. Pi reports only the utime error; so does PiG. Each row runs Pi's pinned proper-lockfile on the same state first.
func TestAcquireIgnoresRmdirAfterFailedProbeUpstream(t *testing.T) {
	for i, api := range []string{"sync", "async"} {
		t.Run(api, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			var finish func()
			pi := runProperLockfileAfterMkdir(t, path, api, func(lock string) { finish = markBeingRemoved(t, lock) })
			message, ok := strings.CutPrefix(pi, "code=EPERM message=")
			if !ok {
				t.Fatalf("proper-lockfile %s with its new lock being removed: %q, want the EPERM utime error", api, pi)
			}
			finish()
			if _, err := os.Lstat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("lock directory after Pi's run: %v", err)
			}

			// Pi ran in a fresh process, whose fs has not probed the mtime precision yet.
			freshMtimeProbe(t)
			stubMkdir(t, func(_ int, real func() error) error {
				if err := real(); err != nil {
					return err
				}
				markBeingRemoved(t, path+".lock")
				return nil
			})
			lock, err := linkAcquirers[i].run(path)
			if lock != nil {
				_ = lock.Release()
				t.Fatal("acquired a lock whose directory is being removed")
			}
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, ErrLocked) {
				t.Fatalf("err = %v, want the utime's ERROR_ACCESS_DENIED", err)
			}
			if err.Error() != message {
				t.Fatalf("message = %q, want proper-lockfile's %q", err.Error(), message)
			}
		})
	}
}

// When the release of a lock taken after the abort fails, Pi's acquireLockAsync throws the release's error in place of the abort (auth-storage.ts:149-152: await release() rejects before signal.throwIfAborted()). A lock directory that another process starts removing makes that rmdir fail with EPERM. PiG's Acquire and AcquireWithOptions return the release error alone, not joined with the cancellation.
func TestAcquireAbortedAfterAcquisitionReturnsAFailedReleaseUpstream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var finish func()
	pi := runPiAcquireThenAbort(t, path, func(lock string) { finish = markBeingRemoved(t, lock) })
	message, ok := strings.CutPrefix(pi, "Error: ")
	if !ok || !strings.HasPrefix(message, "EPERM: operation not permitted, rmdir ") {
		t.Fatalf("Pi withLockAsync aborted after acquisition with a failing release: %q, want the EPERM rmdir error", pi)
	}
	finish()
	for _, acquire := range []struct {
		name string
		run  func(context.Context, string) (*Lock, error)
	}{
		{"Acquire", Acquire},
		{"AcquireWithOptions", func(ctx context.Context, path string) (*Lock, error) {
			return AcquireWithOptions(ctx, path, AcquireOptions{Stale: asyncStale, Update: asyncStale / 2, Retry: time.Millisecond})
		}},
	} {
		t.Run(acquire.name, func(t *testing.T) {
			var finish func()
			ctx := &abortOnSecondCheck{Context: context.Background(), onAbort: func() { finish = markBeingRemoved(t, path+".lock") }}
			lock, err := acquire.run(ctx, path)
			if lock != nil {
				_ = lock.Release()
				t.Fatal("returned a lock to a cancelled caller")
			}
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want the release's ERROR_ACCESS_DENIED alone", err)
			}
			if err.Error() != message {
				t.Fatalf("message = %q, want Pi's %q", err.Error(), message)
			}
			finish()
		})
	}
}

// runProperLockfileTwiceAfterMkdir runs two proper-lockfile lock calls on file in one Node process, sharing one fs as every lock call shares graceful-fs. The second call's mkdir hands the new lock directory to whileMade before proper-lockfile continues. It returns what each call did: "acquired" or "released: <code>" when its release rejects, or "code=<code> message=<message>".
func runProperLockfileTwiceAfterMkdir(t *testing.T, file string, whileMade func(lock string)) string {
	t.Helper()
	const script = `
const fs = require('fs');
const path = require('path');
const [module, file, markers] = process.argv.slice(1);
const lockfile = require(module);
const sleeper = new Int32Array(new SharedArrayBuffer(4));
let second = false;
const custom = {
  ...fs,
  mkdir: (lock, ...rest) => {
    const callback = rest.pop();
    fs.mkdir(lock, ...rest, (err) => {
      if (!err && second) {
        fs.writeFileSync(path.join(markers, 'made.tmp'), lock); fs.renameSync(path.join(markers, 'made.tmp'), path.join(markers, 'made'));
        while (!fs.existsSync(path.join(markers, 'go'))) Atomics.wait(sleeper, 0, 0, 1);
      }
      callback(err);
    });
  },
};
const attempt = () => lockfile.lock(file, { realpath: false, fs: custom }).then(
  (release) => release().then(() => 'acquired', (err) => 'acquired, release ' + err.code),
  (err) => 'code=' + err.code + ' message=' + err.message);
(async () => {
  const first = await attempt();
  second = true;
  console.log(JSON.stringify([first, await attempt()]));
})();
`
	markers := filepath.Join(t.TempDir(), "markers")
	if err := os.Mkdir(markers, 0o777); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, properLockfileModule(t), file, markers)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if lock, err := os.ReadFile(filepath.Join(markers, "made")); err == nil {
			whileMade(string(lock))
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("proper-lockfile did not create its second lock: %s", out.String())
		}
		time.Sleep(time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(markers, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("node: %v\n%s", err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// proper-lockfile caches the mtime precision on the fs object after its first successful probe (lib/mtime-precision.js:6-17), and every lock call shares graceful-fs, so later acquisitions in the process only stat their new directory. A directory that another process starts removing right after mkdir then still stats (libuv reads its parent entry), so the lock is taken, and its release fails with EPERM. PiG's cancellable acquisitions share one probe the same way. lockSync builds a new fs for each call (lib/adapter.js:5-25,63-68), so AcquireSync always probes (TestAcquireIgnoresRmdirAfterFailedProbeUpstream/sync).
func TestAcquireAfterTheFirstProbeOnlyStatsUpstream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	var finish func()
	pi := runProperLockfileTwiceAfterMkdir(t, path, func(lock string) { finish = markBeingRemoved(t, lock) })
	if pi != `["acquired","acquired, release EPERM"]` {
		t.Fatalf("proper-lockfile's second lock with its new directory being removed: %s, want it acquired and its release EPERM", pi)
	}
	finish()
	if _, err := os.Lstat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock directory after Pi's run: %v", err)
	}

	freshMtimeProbe(t)
	first, err := Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	stubMkdir(t, func(_ int, real func() error) error {
		if err := real(); err != nil {
			return err
		}
		markBeingRemoved(t, path+".lock")
		return nil
	})
	second, err := Acquire(t.Context(), path)
	if err != nil {
		t.Fatalf("second acquisition = %v, want the lock after a stat-only probe", err)
	}
	if err := second.Release(); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("release = %v, want rmdir's ERROR_ACCESS_DENIED", err)
	}
}

// abortOnSecondCheck is a caller context that is live when Acquire starts and cancelled when Acquire checks it again after taking the lock; onAbort runs at that second check.
type abortOnSecondCheck struct {
	context.Context
	calls   int
	onAbort func()
}

func (c *abortOnSecondCheck) Err() error {
	c.calls++
	if c.calls == 1 {
		return nil
	}
	if c.calls == 2 && c.onAbort != nil {
		c.onAbort()
	}
	return context.Canceled
}

// freshMtimeProbe makes the next cancellable acquisition probe the mtime as a new Node process's first lock call does, until the test ends.
func freshMtimeProbe(t *testing.T) {
	t.Helper()
	previous := cachedPrecision.Swap(int32(precisionUnknown))
	t.Cleanup(func() { cachedPrecision.Store(previous) })
}
