//go:build windows

package host

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

// manager.ts processLock: a released name is acquirable again, in the same process; a held name is not.
func TestProcessLockReleaseFreesTheNameOnWindows(t *testing.T) {
	name := fmt.Sprintf("chord2-test-%d-reacquire", os.Getpid())
	for i := 0; i < 5; i++ {
		release, err := ProcessLock(name)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		_, again := ProcessLock(name)
		var chordErr *ChordError
		if again == nil {
			t.Fatalf("acquire %d: a held lock was acquired twice", i)
		}
		if !errors.As(again, &chordErr) || chordErr.Reason != "locked" {
			t.Fatalf("acquire %d: second error = %v", i, again)
		}
		if err := release(); err != nil {
			t.Fatal(err)
		}
		if err := release(); err != nil {
			t.Fatalf("second release: %v", err)
		}
	}
}
