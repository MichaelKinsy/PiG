package experimental

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// A stolen or removed lock is fatal in Pi: proper-lockfile's default onCompromised throws from the heartbeat timer and nothing in server.ts or session-worker.ts overrides it.
// upstream: node_modules/proper-lockfile/lib/lockfile.js:213 (default onCompromised), packages/coding-agent/src/experimental/server.ts:113,201, packages/coding-agent/src/experimental/session-worker.ts:533
func TestExperimentalLocksTerminateTheProcessWhenCompromised(t *testing.T) {
	for _, test := range []struct {
		name    string
		acquire func(t *testing.T, dir string) (lockPath string, release func() error)
		update  time.Duration
	}{
		{"launcher profile", func(t *testing.T, dir string) (string, func() error) {
			profile, err := AcquireServerProfile(context.Background(), dir, new("00000000-0000-4000-8000-000000000001"))
			if err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "launcher-00000000-0000-4000-8000-000000000001.lock"), profile.Release
		}, 10 * time.Second},
		{"server activation", func(t *testing.T, dir string) (string, func() error) {
			release, err := AcquireServerActivation(context.Background(), dir, "server")
			if err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "activation-server.lock"), release
		}, activationTimeout},
		{"Session ownership", func(t *testing.T, dir string) (string, func() error) {
			session := filepath.Join(dir, "session.jsonl")
			if err := os.WriteFile(session, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			lock, err := acquireSessionOwnership(session)
			if err != nil {
				t.Fatal(err)
			}
			return session + ".lock", lock.Release
		}, time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				previous := terminateOnLockCompromise
				compromised := make(chan error, 2)
				terminateOnLockCompromise = func(err error) { compromised <- err }
				t.Cleanup(func() { terminateOnLockCompromise = previous })
				lockPath, release := test.acquire(t, t.TempDir())
				time.Sleep(test.update / 2)
				synctest.Wait()
				select {
				case err := <-compromised:
					t.Fatalf("healthy lock reported compromise: %v", err)
				default:
				}
				if err := os.Remove(lockPath); err != nil {
					t.Fatal(err)
				}
				time.Sleep(test.update)
				synctest.Wait()
				select {
				case err := <-compromised:
					if err == nil || !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("compromise = %v, want the missing lock directory", err)
					}
				default:
					t.Fatal("removing the lock directory did not terminate the process")
				}
				if err := release(); err == nil {
					t.Fatal("releasing a compromised lock succeeded")
				}
				if len(compromised) != 0 {
					t.Fatalf("compromise reported %d extra times", len(compromised))
				}
			})
		})
	}
}

// The default policy is Node's uncaught-exception exit: the error reaches stderr and the process exits with status 1 without returning to the caller, after proper-lockfile's exit hook removed every other held lock directory.
// upstream: node_modules/proper-lockfile/lib/lockfile.js:213,331-337
func TestTerminateOnLockCompromiseExitsWithStatusOne(t *testing.T) {
	if dir := os.Getenv("PIG_TEST_LOCK_COMPROMISE_CHILD"); dir != "" {
		if _, err := pilock.AcquireWithOptions(context.Background(), filepath.Join(dir, "other"), pilock.AcquireOptions{Stale: time.Minute, Update: time.Second, Retry: time.Millisecond}); err != nil {
			t.Fatal(err)
		}
		terminateOnLockCompromise(&pilock.CompromisedError{Cause: &fs.PathError{Op: "stat", Path: "/tmp/probe.lock", Err: syscall.ENOENT}})
		os.Exit(3)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestTerminateOnLockCompromiseExitsWithStatusOne$")
	directory := t.TempDir()
	command.Env = append(os.Environ(), "PIG_TEST_LOCK_COMPROMISE_CHILD="+directory)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("child error = %v, want exit status 1\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(directory, "other.lock")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the exit hook left another held lock directory behind: %v", err)
	}
	want := "[Error: ENOENT: no such file or directory, stat '/tmp/probe.lock'] {\n  errno: " + strconv.Itoa(-int(syscall.ENOENT)) + ",\n  code: 'ECOMPROMISED',\n  syscall: 'stat',\n  path: '/tmp/probe.lock'\n}\n"
	if runtime.GOOS == "windows" {
		want = strings.Replace(want, "errno: "+strconv.Itoa(-int(syscall.ENOENT)), "errno: -4058", 1)
	}
	if !strings.HasSuffix(string(output), want) || strings.Contains(string(output), "Uncaught") {
		t.Fatalf("stderr = %q, want the Node-inspected error %q without an Uncaught prefix", output, want)
	}
}

// proper-lockfile's release function rejects a second call with ERELEASED, so a launcher or activation lock has exactly one owner-side release.
// upstream: node_modules/proper-lockfile/lib/lockfile.js:255-258
func TestServerLocksRejectSecondRelease(t *testing.T) {
	dir := t.TempDir()
	profile, err := AcquireServerProfile(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.Release(); err != nil {
		t.Fatal(err)
	}
	if err := profile.Release(); err == nil || err.Error() != "Lock is already released" {
		t.Fatalf("second profile Release = %v, want Lock is already released", err)
	}
	release, err := AcquireServerActivation(t.Context(), dir, profile.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := release(); err == nil || err.Error() != "Lock is already released" {
		t.Fatalf("second activation release = %v, want Lock is already released", err)
	}
}
