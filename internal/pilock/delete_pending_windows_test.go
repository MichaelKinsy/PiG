package pilock

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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
			if got := calls.Load(); got != 1 {
				t.Fatalf("mkdir calls = %d, want 1", got)
			}
		})
	}
}

// The oracle for both tests above: Pi's pinned proper-lockfile on this Windows file system, with the holder's removal in flight at mkdir or at the stat that follows EEXIST.
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
const report = (err) => console.log(err ? 'code=' + err.code : 'acquired');
if (api === 'sync') {
  try { lockfile.lockSync(file, { realpath: false, fs: hooked }); report(); } catch (err) { report(err); }
} else {
  lockfile.lock(file, { realpath: false, fs: hooked }).then(() => report(), report);
}
`
	for _, test := range []struct {
		step string
		want string
	}{
		{"mkdir", "code=EPERM"},
		{"stat", "code=ELOCKED"},
	} {
		for _, api := range []string{"sync", "async"} {
			t.Run(test.step+"/"+api, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "auth.json")
				if err := os.Mkdir(path+".lock", 0o777); err != nil {
					t.Fatal(err)
				}
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
				if got := strings.TrimSpace(out.String()); got != test.want {
					t.Fatalf("proper-lockfile %s with the removal in flight at %s: %q, want %q", api, test.step, got, test.want)
				}
			})
		}
	}
}
