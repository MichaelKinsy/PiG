package workers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Ports packages/chord/test/next-node/workers/lock.test.ts: one holder at a time, a waiter taking the lock when the
// holder releases it (told by its connection's close, no polling), a waiter's abort; socket file locks.

func TestProcessLockMakesASecondTakerWaitUntilTheHolderReleasesThenTakesIt(t *testing.T) {
	name := fmt.Sprintf("chord-test-lock-%d-%d", os.Getpid(), time.Now().UnixNano())
	first, err := ProcessLock(name, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	var taken atomic.Bool
	secondResult := make(chan ProcessLockHandle, 1)
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
	waiting.Add(1)
	go func() {
		defer waiting.Done()
		_, failure = ProcessLock(name, abort)
	}()
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

func TestSocketFileLocksFailClosedOnAStaleLockFileNamingItTwoTakersNeverBothTakeItOver(t *testing.T) {
	address := filepath.Join(t.TempDir(), "lock.sock")
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
	address := filepath.Join(t.TempDir(), "lock.sock")
	first, err := lockAt(lockLocation{kind: lockFile, address: address}, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondResult := make(chan ProcessLockHandle, 1)
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
