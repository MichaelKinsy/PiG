package tools

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

// Pi packages/durable/src/tools/file-mutation-queue.ts: mutations of one file run one after another in the order they queued, a finished call removes its own tail from the queue only (never a later
// caller's), and the key of a file that does not exist yet is its canonical parent joined with its name, with no separator added after a parent that already ends in one.
// mutation-checked: `!=` for `==` in the tail cleanup lets a third caller run beside the second; `&&` for `||` in the separator test corrupts the key under a root parent.
func TestWithFileMutationQueueKeepsALaterCallersTailAndLeavesNoneBehind(t *testing.T) {
	executionEnv := createEnv(t)
	key, err := mutationKey(background, executionEnv, "chain.txt")
	mustDo(t, err)
	enter := func(started, release chan struct{}, done chan error) {
		go func() {
			_, err := withFileMutationQueue(background, executionEnv, "chain.txt", func() (struct{}, error) {
				close(started)
				<-release
				return struct{}{}, nil
			})
			done <- err
		}()
	}
	tails := func() int {
		mutationQueues.mu.Lock()
		defer mutationQueues.mu.Unlock()
		if _, ok := mutationQueues.tails[key]; ok {
			return 1
		}
		return 0
	}
	waitForQueued := func() {
		time.Sleep(30 * time.Millisecond)
	}
	startedA, releaseA, doneA := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	startedB, releaseB, doneB := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	startedC, releaseC, doneC := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	enter(startedA, releaseA, doneA)
	<-startedA
	enter(startedB, releaseB, doneB)
	waitForQueued()
	select {
	case <-startedB:
		t.Fatal("the second call entered while the first held the file")
	default:
	}
	close(releaseA)
	mustDo(t, <-doneA)
	<-startedB
	// The first call is finished and the second holds the file: a third call must still wait for the second.
	enter(startedC, releaseC, doneC)
	waitForQueued()
	select {
	case <-startedC:
		t.Fatal("the third call entered while the second held the file: the first call's cleanup removed the second call's tail")
	default:
	}
	close(releaseB)
	mustDo(t, <-doneB)
	<-startedC
	close(releaseC)
	mustDo(t, <-doneC)
	if tails() != 0 {
		t.Error("the queue still holds a tail for the file after every call finished")
	}
}

func TestMutationKeyOfAMissingFileUnderARootParentAddsNoSeparator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a root parent is a drive on Windows")
	}
	executionEnv := createEnv(t)
	const name = "pig-no-such-entry-9f3a1c"
	key, err := mutationKey(background, executionEnv, "/"+name)
	mustDo(t, err)
	if want := executionEnv.Id() + "\x00/" + name; key != want {
		t.Errorf("mutationKey = %q, want %q", key, want)
	}
}

// Pi edit-diff.ts getReplacementLineRange (:84-107): a replacement that starts in no line, or that ends after the last line, is "Replacement range is outside the base content."
// mutation-checked: `<=` for `<` in the end-line walk and `>` for `>=` in the end-of-lines check fail it.
func TestGetReplacementLineRangeRejectsARangeThatEndsPastTheLastLineOfTheBase(t *testing.T) {
	lines := getLineSpans("ab\ncd\nef")
	for _, tc := range []struct {
		name        string
		index, size int
		start, end  int
		wantOutside bool
	}{
		{"inside the first line", 0, 1, 0, 1, false},
		{"ends exactly at the end of a line", 0, 3, 0, 1, false},
		{"spans two lines", 1, 3, 0, 2, false},
		{"ends exactly at the end of the content", 3, 5, 1, 3, false},
		{"ends one byte past the content", 6, 3, 0, 0, true},
		{"starts past the content", 8, 1, 0, 0, true},
	} {
		start, end, err := getReplacementLineRange(lines, matchedEdit{matchIndex: tc.index, matchLength: tc.size})
		if tc.wantOutside {
			if !errors.Is(err, errReplacementOutsideBase) || err.Error() != "Replacement range is outside the base content." {
				t.Errorf("%s: err = %v, want the outside-the-base error", tc.name, err)
			}
			continue
		}
		if err != nil || start != tc.start || end != tc.end {
			t.Errorf("%s: getReplacementLineRange = %d, %d, %v, want %d, %d", tc.name, start, end, err, tc.start, tc.end)
		}
	}
}
