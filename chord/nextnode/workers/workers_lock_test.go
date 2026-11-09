package workers

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// pi: packages/chord/src/next-node/workers/lock.ts
// Ports packages/chord/test/next-node/workers/lock.test.ts: one holder at a time, a waiter taking the lock when the
// holder releases it (told by its connection's close, no polling), a waiter's abort; socket file locks.

func TestProcessLockMakesASecondTakerWaitUntilTheHolderReleasesThenTakesIt(t *testing.T) {
	name := fmt.Sprintf("chord-test-lock-%d-%d", os.Getpid(), time.Now().UnixNano())
	first, err := ProcessLock(name, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	var taken atomic.Bool
	secondResult := make(chan *ProcessLockHandle, 1)
	go func() {
		lock, err := ProcessLock(name, chordctx.Background())
		if err != nil {
			t.Error(err)
		}
		taken.Store(true)
		secondResult <- lock
	}()
	time.Sleep(100 * time.Millisecond)
	if taken.Load() {
		t.Fatal("the second taker took a held lock")
	}
	released := time.Now()
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second := <-secondResult
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if !taken.Load() {
		t.Fatal("the second taker never took the lock")
	}
	if elapsed := time.Since(released); elapsed >= 100*time.Millisecond {
		t.Fatalf("the waiter took the lock %v after the release, want under 100ms (told by close, not polling)", elapsed)
	}
}

func TestProcessLockStopsWaitingWhenTheWaitersContextAborts(t *testing.T) {
	name := fmt.Sprintf("chord-test-lock-abort-%d-%d", os.Getpid(), time.Now().UnixNano())
	held, err := ProcessLock(name, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	abort, cancel := chordctx.WithCancel(chordctx.Background())
	var waiting sync.WaitGroup
	var failure error
	waiting.Go(func() {
		_, failure = ProcessLock(name, abort)
	})
	cancel(errors.New("gave up"))
	waiting.Wait()
	if failure == nil || failure.Error() != "gave up" {
		t.Fatalf("aborted wait = %v, want the abort's reason %q", failure, "gave up")
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := ProcessLock(name, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
}

// A holder's Release returns after a waiter's abort, every time: a Windows pipe listener's Close waited forever for the
// accepting goroutine in one of a few thousand of these (internal/chord/winpipe).
func TestProcessLockReleaseReturnsAfterAWaitersAbortEveryTime(t *testing.T) {
	for round := range 2000 {
		name := fmt.Sprintf("chord-test-lock-rounds-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), round)
		held, err := ProcessLock(name, chordctx.Background())
		if err != nil {
			t.Fatal(err)
		}
		abort, cancel := chordctx.WithCancel(chordctx.Background())
		var waiting sync.WaitGroup
		waiting.Go(func() { _, _ = ProcessLock(name, abort) })
		// The abort comes at every stage of the waiter's dial.
		time.Sleep(time.Duration(round%40) * 25 * time.Microsecond)
		cancel(errors.New("gave up"))
		waiting.Wait()
		released := make(chan error, 1)
		go func() { released <- held.Release() }()
		select {
		case err := <-released:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: Release did not return", round)
		}
	}
}

// A waiter that gives up (or dies) closes its connection: the holder forgets it then (`socket.once("close", () =>
// waiters.delete(socket))`), not at its own release.
func TestProcessLockForgetsAWaiterWhoseConnectionClosedBeforeItsOwnRelease(t *testing.T) {
	name := fmt.Sprintf("chord-test-lock-forget-%d-%d", os.Getpid(), time.Now().UnixNano())
	held, err := ProcessLock(name, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	waiters := func() int {
		held.mu.Lock()
		defer held.mu.Unlock()
		return len(held.waiters)
	}
	abort, cancel := chordctx.WithCancel(chordctx.Background())
	var waiting sync.WaitGroup
	waiting.Go(func() { _, _ = ProcessLock(name, abort) })
	until(t, func() bool { return waiters() == 1 })
	cancel(errors.New("gave up"))
	waiting.Wait()
	until(t, func() bool { return waiters() == 0 }, 2_000)
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
}

// shortLockDir is `mkdtemp(join(tmpdir(), "chord-lock-file-"))` of lock.test.ts: a directory whose socket path fits in sun_path
// (t.TempDir() carries the test's name, which a macOS TMPDIR makes too long for a socket path).
func shortLockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "chord-lock-file-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSocketFileLocksFailClosedOnAStaleLockFileNamingItTwoTakersNeverBothTakeItOver(t *testing.T) {
	address := filepath.Join(shortLockDir(t), "lock.sock")
	// What a crashed holder leaves: a path nobody accepts on.
	if err := os.WriteFile(address, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	type outcome struct{ err error }
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			_, err := lockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
			results <- outcome{err}
		}()
	}
	want := fmt.Sprintf("lock: %s is a stale lock file", address)
	for range 2 {
		result := <-results
		if result.err == nil || !strings.HasPrefix(result.err.Error(), want) {
			t.Fatalf("taker error = %v, want a message starting %q", result.err, want)
		}
	}
}

func TestSocketFileLocksAreTakenWaitedForAndReleasedLikeTheOthers(t *testing.T) {
	address := filepath.Join(shortLockDir(t), "lock.sock")
	first, err := lockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondResult := make(chan *ProcessLockHandle, 1)
	go func() {
		lock, err := lockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
		if err != nil {
			t.Error(err)
		}
		secondResult <- lock
	}()
	time.Sleep(50 * time.Millisecond)
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second := <-secondResult
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

// leaveSocketFile leaves a socket file at path as a holder killed without its exit hooks does: a listener that was
// closed without unlinking (lock.test.ts `leaveSocketFile`).
func leaveSocketFile(t *testing.T, path string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

// Ports lock.test.ts "fail closed on a stale lock file, naming it": a killed holder's socket file too, not only a regular file.
func TestSocketFileLocksFailClosedOnAKilledHoldersSocketFileAndOnARegularFile(t *testing.T) {
	dir := shortLockDir(t)
	for name, leave := range map[string]func(string){
		"killed.sock":  func(path string) { leaveSocketFile(t, path) },
		"regular.sock": func(path string) { _ = os.WriteFile(path, nil, 0o600) },
	} {
		address := filepath.Join(dir, name)
		leave(address)
		for range 2 {
			_, err := lockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
			want := fmt.Sprintf("lock: %s is a stale lock file", address)
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("%s: taker error = %v, want a message starting %q", name, err, want)
			}
		}
	}
}

// Ports lock.test.ts "clearStaleLockAt removes a socket file nobody accepts on: the lock can be taken again".
func TestClearStaleLockAtRemovesASocketFileNobodyAcceptsOnAndTheLockCanBeTakenAgain(t *testing.T) {
	address := filepath.Join(shortLockDir(t), "lock.sock")
	leaveSocketFile(t, address)
	cleared, err := clearStaleLockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
	if err != nil || !cleared {
		t.Fatalf("clearStaleLockAt = %v, %v, want true, nil", cleared, err)
	}
	again, err := lockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
}

// Ports lock.test.ts "clearStaleLockAt keeps a held lock's file, and does nothing without a file or for abstract sockets
// and pipes".
func TestClearStaleLockAtKeepsAHeldLocksFileAndDoesNothingWithoutAFileOrForAbstractSocketsAndPipes(t *testing.T) {
	address := filepath.Join(shortLockDir(t), "lock.sock")
	location := lockLocation{kind: lockFile, address: address}
	check := func(location lockLocation, want bool) {
		t.Helper()
		cleared, err := clearStaleLockAt(location, chordctx.Background())
		if err != nil || cleared != want {
			t.Fatalf("clearStaleLockAt(%v) = %v, %v, want %v, nil", location, cleared, err, want)
		}
	}
	check(location, false)
	held, err := lockAt(location, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	check(location, false)
	var taken atomic.Bool
	secondResult := make(chan *ProcessLockHandle, 1)
	go func() {
		lock, err := lockAt(location, chordctx.Background())
		if err != nil {
			t.Error(err)
		}
		taken.Store(true)
		secondResult <- lock
	}()
	time.Sleep(100 * time.Millisecond)
	if taken.Load() {
		t.Fatal("a second taker took a held lock")
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	second := <-secondResult
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	check(lockLocation{kind: lockAbstract, address: "@chord-test-unused"}, false)
	check(lockLocation{kind: lockPipe, address: `\\.\pipe\chord-test-unused`}, false)
}
