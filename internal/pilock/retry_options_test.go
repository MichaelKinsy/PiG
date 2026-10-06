package pilock

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// epermMkdirError is the platform mkdir error Node reports as EPERM: on Windows, ERROR_ACCESS_DENIED, which mkdir returns while another process's release of the lock directory is still pending deletion.
func epermMkdirError(t *testing.T) error {
	t.Helper()
	for name, message := range nonEEXISTMkdirMessages {
		if message == "EPERM: operation not permitted" {
			return nonEEXISTMkdirErrors[name]
		}
	}
	t.Fatal("no mkdir error maps to EPERM")
	return nil
}

// reportAcquisition is the outcome both implementations are compared on: acquired, ELOCKED, or the message of any other error.
func reportAcquisition(err error) string {
	switch {
	case err == nil:
		return "acquired"
	case errors.Is(err, ErrLocked):
		return "code=ELOCKED"
	default:
		return "message=" + err.Error()
	}
}

// proper-lockfile 4.1.2's lock hands every failed attempt to the retry module (lib/lockfile.js:231-240, `if (operation.retry(err)) return;`), and retry 0.13.1 retries any error until the retries run out, then rejects with mainError: the most frequent message, ties going to the later error (lib/retry_operation.js). Pi's MCP OAuth refresh lock, experimental server and session-worker locks and durable session lock all pass retries, so an EPERM from mkdir, which Windows returns while another process is still deleting a just-released lock directory, waits like ELOCKED. Each row runs Pi's pinned proper-lockfile first, then AcquireWithOptions on the same sequence of mkdir results.
func TestAcquireWithOptionsRetriesEveryAttemptErrorUpstream(t *testing.T) {
	const script = `
const fs = require('fs');
const [module, file, held, eperm, retries] = process.argv.slice(1);
const lockfile = require(module);
let calls = 0;
const hooked = { ...fs, mkdir(p, callback) {
  calls++;
  if (calls > Number(held) && calls <= Number(held) + Number(eperm)) {
    const err = Object.assign(new Error("EPERM: operation not permitted, mkdir '" + p + "'"), { code: 'EPERM', syscall: 'mkdir', path: p });
    return process.nextTick(callback, err);
  }
  fs.mkdir(p, callback);
} };
const report = (err) => console.log(err ? (err.code === 'ELOCKED' ? 'code=ELOCKED' : 'message=' + err.message) : 'acquired');
lockfile.lock(file, { realpath: false, fs: hooked, onCompromised() {}, retries: { retries: Number(retries), factor: 1, minTimeout: 1, maxTimeout: 1 } })
  .then((release) => release().then(() => report()), report);
`
	eperm := epermMkdirError(t)
	for _, test := range []struct {
		name string
		// held attempts find the directory held (ELOCKED); the eperm attempts after them fail with EPERM; later attempts use the real mkdir.
		held, eperm int
		// retries is Pi's retry count; for PiG the last failing attempt outlasts Wait instead, so both stop after the same attempts.
		retries int
		want    string
	}{
		{"EPERM then free", 0, 2, 5, "acquired"},
		{"EPERM until the wait ends", 0, 4, 3, "message=EPERM: operation not permitted, mkdir '%s'"},
		{"mostly ELOCKED", 3, 1, 3, "code=ELOCKED"},
		{"a tie goes to the later error", 1, 1, 1, "message=EPERM: operation not permitted, mkdir '%s'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			lockPath := func(path string) string { return path + ".lock" }
			setup := func(path string) {
				if test.held > 0 {
					// A fresh directory: proper-lockfile stats it and reports ELOCKED.
					if err := os.Mkdir(lockPath(path), 0o777); err != nil {
						t.Fatal(err)
					}
				}
			}
			release := func(path string) {
				if test.held > 0 {
					if err := os.Remove(lockPath(path)); err != nil {
						t.Fatal(err)
					}
				}
			}

			upstream := filepath.Join(t.TempDir(), "refresh")
			setup(upstream)
			out, err := exec.CommandContext(t.Context(), "node", "-e", script, properLockfileModule(t), upstream,
				strconv.Itoa(test.held), strconv.Itoa(test.eperm), strconv.Itoa(test.retries)).CombinedOutput()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, out)
			}
			if got, want := strings.TrimSpace(string(out)), strings.ReplaceAll(test.want, "%s", lockPath(upstream)); got != want {
				t.Fatalf("proper-lockfile = %q, want %q", got, want)
			}

			path := filepath.Join(t.TempDir(), "refresh")
			setup(path)
			options := AcquireOptions{Stale: time.Minute, Update: 30 * time.Second, Retry: time.Millisecond, Wait: time.Second, OnCompromised: func(error) {}}
			attempts := test.retries + 1
			calls := stubMkdir(t, func(call int, real func() error) error {
				if call == attempts && test.want != "acquired" {
					// The last attempt Pi makes outlasts Wait, so PiG makes no further attempt.
					time.Sleep(options.Wait)
				}
				if call > test.held && call <= test.held+test.eperm {
					return &fs.PathError{Op: "mkdir", Path: lockPath(path), Err: eperm}
				}
				return real()
			})
			lock, err := AcquireWithOptions(context.Background(), path, options)
			if lock != nil {
				if releaseErr := lock.Release(); releaseErr != nil {
					t.Fatal(releaseErr)
				}
			}
			if got, want := reportAcquisition(err), strings.ReplaceAll(test.want, "%s", lockPath(path)); got != want {
				t.Fatalf("AcquireWithOptions = %q, want proper-lockfile's %q", got, want)
			}
			if test.want == "acquired" {
				if got := int(calls.Load()); got != test.eperm+1 {
					t.Fatalf("mkdir calls = %d, want %d", got, test.eperm+1)
				}
			} else if got := int(calls.Load()); got != attempts {
				t.Fatalf("mkdir calls = %d, want %d", got, attempts)
			}
			release(path)
		})
	}
}
