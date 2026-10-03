package pilock

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// runProperLockfileHolder runs proper-lockfile's lock on file in Node, waits until it holds the lock, calls whileHeld, and returns what Node prints: for step "heartbeat" the compromise its update timer reports, for step "release" the result of check and then release.
func runProperLockfileHolder(t *testing.T, file, step string, whileHeld func()) string {
	t.Helper()
	const script = `
const fs = require('fs');
const path = require('path');
const [module, file, step, markers] = process.argv.slice(1);
const lockfile = require(module);
const print = (value) => { console.log(JSON.stringify(value)); process.exit(0); };
const failure = (err) => ({ code: err.code, syscall: err.syscall, message: err.message });
setTimeout(() => print({ timeout: true }), 20000);
const options = step === 'heartbeat'
  ? { realpath: false, stale: 2000, update: 1000, onCompromised: (err) => print({ compromised: failure(err) }) }
  : { realpath: false };
lockfile.lock(file, options).then((release) => {
  fs.writeFileSync(path.join(markers, 'acquired'), '');
  if (step === 'heartbeat') return;
  const sleeper = new Int32Array(new SharedArrayBuffer(4));
  while (!fs.existsSync(path.join(markers, 'removing'))) Atomics.wait(sleeper, 0, 0, 1);
  lockfile.check(file, { realpath: false }).then(
    (locked) => release().then(() => print({ locked, released: true }), (err) => print({ locked, release: failure(err) })),
    (err) => print({ check: failure(err) }));
}, (err) => print({ lock: failure(err) }));
`
	markers := filepath.Join(t.TempDir(), "markers")
	if err := os.Mkdir(markers, 0o777); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, properLockfileModule(t), file, step, markers)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(markers, "acquired")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("proper-lockfile did not acquire: %s", out.String())
		}
		time.Sleep(time.Millisecond)
	}
	whileHeld()
	if err := os.WriteFile(filepath.Join(markers, "removing"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("node: %v\n%s", err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// jsonPath is path as JSON.stringify writes it inside a string.
func jsonPath(path string) string { return strings.ReplaceAll(path, `\`, `\\`) }

// proper-lockfile's heartbeat stats its lock with fs.stat (lib/lockfile.js:114). While another process is removing the directory, Node's stat still reads it from the parent directory entry with the owner's mtime, so the heartbeat goes on to utimes (lib/lockfile.js:143), which fails with EPERM because the name is delete-pending. The update is retried after a second; past the stale threshold that utime failure is the compromise (lib/lockfile.js:153-156). PiG's heartbeat reports the same failure: its chtimes (utime) error, not a stat error.
func TestHeartbeatReadsLockBeingRemovedAsNodeStatDoesUpstream(t *testing.T) {
	t.Run("proper-lockfile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "owned")
		got := runProperLockfileHolder(t, path, "heartbeat", func() { markBeingRemoved(t, path+".lock") })
		want := `{"compromised":{"code":"ECOMPROMISED","syscall":"utime","message":"EPERM: operation not permitted, utime '` + jsonPath(path+".lock") + `'"}}`
		if got != want {
			t.Fatalf("proper-lockfile heartbeat on a lock being removed:\n got %s\nwant %s", got, want)
		}
	})
	t.Run("pig", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "owned")
		compromised := make(chan error, 1)
		lock, err := AcquireWithOptions(t.Context(), path, AcquireOptions{Stale: 200 * time.Millisecond, Update: 20 * time.Millisecond, Retry: time.Millisecond, OnCompromised: func(err error) { compromised <- err }})
		if err != nil {
			t.Fatal(err)
		}
		markBeingRemoved(t, path+".lock")
		select {
		case err := <-compromised:
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) || pathErr.Op != "chtimes" || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("compromise = %v, want the utime (chtimes) ERROR_ACCESS_DENIED", err)
			}
			if want := "EPERM: operation not permitted, utime '" + path + ".lock'"; err.Error() != want {
				t.Fatalf("compromise message = %q, want proper-lockfile's %q", err.Error(), want)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("compromise not reported")
		}
		_ = lock.Release()
	})
}

// proper-lockfile's compromise is the fs error with code ECOMPROMISED (lib/lockfile.js:121,155), whose message and errno come from libuv's translation of the Windows error (src/win/error.c uv_translate_sys_error): ERROR_ACCESS_DENIED is EPERM, ERROR_SHARING_VIOLATION is EBUSY and ERROR_WRITE_PROTECT is EROFS. Inspect's errno is libuv's for the code, as Node's util.getSystemErrorMap reports it.
func TestCompromisedErrorsCarryNodeCodesForWindowsErrors(t *testing.T) {
	inspect := func(message, code, syscall string) string {
		return "[Error: " + message + "] {\n  errno: " + strconv.Itoa(nodeSystemErrno(t, code)) + ",\n  code: 'ECOMPROMISED',\n  syscall: '" + syscall + "',\n  path: '/p'\n}"
	}
	for _, test := range []struct {
		name             string
		err              *CompromisedError
		message, inspect string
	}{
		{"access denied at utime", &CompromisedError{Cause: &fs.PathError{Op: "chtimes", Path: "/p", Err: windows.ERROR_ACCESS_DENIED}}, "EPERM: operation not permitted, utime '/p'", "[Error: EPERM: operation not permitted, utime '/p'] {\n  errno: -4048,\n  code: 'ECOMPROMISED',\n  syscall: 'utime',\n  path: '/p'\n}"},
		{"sharing violation at stat", &CompromisedError{Cause: &fs.PathError{Op: "GetFileAttributesEx", Path: "/p", Err: windows.ERROR_SHARING_VIOLATION}}, "EBUSY: resource busy or locked, stat '/p'", inspect("EBUSY: resource busy or locked, stat '/p'", "EBUSY", "stat")},
		{"write protect at utime", &CompromisedError{Cause: &fs.PathError{Op: "chtimes", Path: "/p", Err: windows.ERROR_WRITE_PROTECT}}, "EROFS: read-only file system, utime '/p'", inspect("EROFS: read-only file system, utime '/p'", "EROFS", "utime")},
		{"path not found at stat", &CompromisedError{Cause: &fs.PathError{Op: "CreateFile", Path: "/p", Err: windows.ERROR_PATH_NOT_FOUND}}, "ENOENT: no such file or directory, stat '/p'", inspect("ENOENT: no such file or directory, stat '/p'", "ENOENT", "stat")},
		// libuv has no code for ERROR_BAD_NET_NAME, so uv_translate_sys_error returns UV_UNKNOWN.
		{"bad net name at stat", &CompromisedError{Cause: &fs.PathError{Op: "CreateFile", Path: "/p", Err: windows.ERROR_BAD_NET_NAME}}, "UNKNOWN: unknown error, stat '/p'", inspect("UNKNOWN: unknown error, stat '/p'", "UNKNOWN", "stat")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.err.Error(); got != test.message {
				t.Fatalf("Error() = %q, want %q", got, test.message)
			}
			if test.inspect != "" && test.err.Inspect() != test.inspect {
				t.Fatalf("Inspect() = %q, want %q", test.err.Inspect(), test.inspect)
			}
		})
	}
}

// libuv names each of these Windows errors (src/win/error.c uv_translate_sys_error, at the line given), so Node reports its code, uv_strerror description and errno, never UNKNOWN. A lock directory on an SMB share meets the network, pipe and memory errors among them. The description and errno come from Node's util.getSystemErrorMap.
func TestCompromisedErrorsNameWindowsErrorsLibuvTranslates(t *testing.T) {
	for _, test := range []struct {
		name  string
		errno syscall.Errno
		code  string
	}{
		{"ERROR_SEM_TIMEOUT", windows.ERROR_SEM_TIMEOUT, "ETIMEDOUT"},                      // error.c:165
		{"ERROR_NETNAME_DELETED", windows.ERROR_NETNAME_DELETED, "ECONNRESET"},             // error.c:94
		{"ERROR_NOT_SUPPORTED", windows.ERROR_NOT_SUPPORTED, "ENOTSUP"},                    // error.c:156
		{"ERROR_NOT_ENOUGH_MEMORY", windows.ERROR_NOT_ENOUGH_MEMORY, "ENOMEM"},             // error.c:145
		{"ERROR_OUTOFMEMORY", windows.ERROR_OUTOFMEMORY, "ENOMEM"},                         // error.c:146
		{"ERROR_INVALID_HANDLE", windows.ERROR_INVALID_HANDLE, "EBADF"},                    // error.c:83
		{"ERROR_OPERATION_ABORTED", windows.ERROR_OPERATION_ABORTED, "ECANCELED"},          // error.c:87
		{"ERROR_NO_UNICODE_TRANSLATION", windows.ERROR_NO_UNICODE_TRANSLATION, "ECHARSET"}, // error.c:89
		{"ERROR_NETWORK_UNREACHABLE", windows.ERROR_NETWORK_UNREACHABLE, "ENETUNREACH"},    // error.c:131
		{"ERROR_HOST_UNREACHABLE", windows.ERROR_HOST_UNREACHABLE, "EHOSTUNREACH"},         // error.c:100
		{"ERROR_NO_DATA", windows.ERROR_NO_DATA, "EAGAIN"},                                 // error.c:80
		{"ERROR_BROKEN_PIPE", windows.ERROR_BROKEN_PIPE, "EOF"},                            // error.c:157
		{"ERROR_BAD_EXE_FORMAT", windows.ERROR_BAD_EXE_FORMAT, "EFTYPE"},                   // error.c:171
		{"ERROR_META_EXPANSION_TOO_LONG", windows.ERROR_META_EXPANSION_TOO_LONG, "E2BIG"},  // error.c:169
		{"ERROR_CONNECTION_REFUSED", windows.ERROR_CONNECTION_REFUSED, "ECONNREFUSED"},     // error.c:92
	} {
		t.Run(test.name, func(t *testing.T) {
			errno, description := nodeSystemError(t, test.code)
			message := test.code + ": " + description + ", stat '/p'"
			err := &CompromisedError{Cause: &fs.PathError{Op: "CreateFile", Path: "/p", Err: test.errno}}
			if got := err.Error(); got != message {
				t.Fatalf("Error() = %q, want %q", got, message)
			}
			if want := "[Error: " + message + "] {\n  errno: " + strconv.Itoa(errno) + ",\n  code: 'ECOMPROMISED',\n  syscall: 'stat',\n  path: '/p'\n}"; err.Inspect() != want {
				t.Fatalf("Inspect() = %q, want %q", err.Inspect(), want)
			}
		})
	}
}

// proper-lockfile's check (lib/lockfile.js:296-321) stats with fs.stat, so a lock whose directory another process is removing still counts as held. Releasing it runs rmdir (lib/lockfile.js:88-96,268-293), which fails with EPERM because the name is delete-pending. PiG's Check keeps the lock, and Release reports rmdir's ERROR_ACCESS_DENIED rather than a compromise.
func TestCheckAndReleaseReadLockBeingRemovedAsNodeStatDoesUpstream(t *testing.T) {
	t.Run("proper-lockfile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "owned")
		got := runProperLockfileHolder(t, path, "release", func() { markBeingRemoved(t, path+".lock") })
		want := `{"locked":true,"release":{"code":"EPERM","syscall":"rmdir","message":"EPERM: operation not permitted, rmdir '` + jsonPath(path+".lock") + `'"}}`
		if got != want {
			t.Fatalf("proper-lockfile check and release of a lock being removed:\n got %s\nwant %s", got, want)
		}
	})
	t.Run("pig", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "owned")
		lock, err := AcquireSync(path)
		if err != nil {
			t.Fatal(err)
		}
		markBeingRemoved(t, path+".lock")
		if err := lock.Check(); err != nil {
			t.Fatalf("Check = %v, want the lock still held", err)
		}
		err = lock.Release()
		if err == nil || errors.Is(err, errCompromised) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("Release = %v, want rmdir's ERROR_ACCESS_DENIED and no compromise", err)
		}
		if want := "EPERM: operation not permitted, rmdir '" + path + ".lock'"; err.Error() != want {
			t.Fatalf("Release message = %q, want proper-lockfile's %q", err.Error(), want)
		}
	})
}
