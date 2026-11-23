//go:build unix

package workers

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/nextnode/internal/lockseam"
)

// pi: packages/chord/src/node/workers/lock.ts
// A temporary directory too deep for sun_path does not break the lock: the socket goes to a private per-user directory under
// /tmp (lock.ts `socketPath`).
func TestASocketFileLockFallsBackToAPrivateDirectoryWhenTheTempDirIsTooLongForASocketPath(t *testing.T) {
	deep := t.TempDir()
	for len(deep) <= maxSocketPathBytes {
		deep = filepath.Join(deep, "a-directory-name-that-makes-the-path-long")
	}
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", deep)
	file := fmt.Sprintf("chord-lock-test-%d-%d.sock", os.Getpid(), time.Now().UnixNano())
	address, err := socketPath(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(address) > maxSocketPathBytes || strings.HasPrefix(address, deep) {
		t.Fatalf("socketPath = %q (%d bytes), want a path under /tmp that fits in sun_path", address, len(address))
	}
	held, err := lockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
}

// pi: packages/chord/src/node/workers/lock.ts waitWhileHeld
// Nobody accepts on a socket file a crashed holder left: Linux's connect(2) to it fails with ECONNREFUSED, and macOS's and the
// BSDs' fail with ENOTSOCK when the leftover is a plain file. Either way the waiter must end with "stale lock file". The
// classifier took only ECONNREFUSED, so on macOS a plain stale file made lockAt retry its listen and connect forever (nightly
// 42368266926: both takers in connect(2) when the 1 h test timeout killed TestSocketFileLocksFailClosedOnAStaleLockFile...).
// mutation-checked: dropping ENOTSOCK from lockRefused fails it
func TestAConnectErrorMeaningNobodyAcceptsOnTheLockMarksItStale(t *testing.T) {
	dial := func(errno syscall.Errno) error {
		return &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", errno)}
	}
	for name, test := range map[string]struct {
		errno syscall.Errno
		want  bool
	}{
		"ECONNREFUSED (Linux, a socket file nobody listens on)": {syscall.ECONNREFUSED, true},
		"ENOTSOCK (macOS and the BSDs, a plain file)":           {syscall.ENOTSOCK, true},
		"ENOENT (the holder released and removed it)":           {syscall.ENOENT, false},
	} {
		if got := lockRefused(dial(test.errno)); got != test.want {
			t.Errorf("lockRefused(%s) = %v, want %v", name, got, test.want)
		}
	}
}

// The lockseam hook forces Unix process locks into socket files under a test directory even on Linux, so tests can
// exercise stale-file refusal and ClearStaleLock through ProcessLock and ClearStaleLock without macOS.
func TestLockSeamForcesSocketFileLocksOnUnix(t *testing.T) {
	dir := shortLockDir(t)
	restore := lockseam.ForceSocketFile(dir)
	t.Cleanup(restore)

	name := fmt.Sprintf("chord-seam-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	loc, err := lockAddress(name)
	if err != nil {
		t.Fatal(err)
	}
	if loc.kind != lockFile || filepath.Dir(loc.address) != dir {
		t.Fatalf("lockAddress = %+v, want lockFile in %s", loc, dir)
	}
	leaveSocketFile(t, loc.address)
	if _, err := ProcessLock(name, chordctx.Background()); err == nil || !strings.Contains(err.Error(), "is a stale lock file") {
		t.Fatalf("ProcessLock on stale file = %v, want stale lock file error", err)
	}
	cleared, err := ClearStaleLock(name, chordctx.Background())
	if err != nil || !cleared {
		t.Fatalf("ClearStaleLock = %v, %v, want true, nil", cleared, err)
	}
	held, err := ProcessLock(name, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
}
