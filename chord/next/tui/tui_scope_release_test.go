package tui

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// A holder releasing its own reference before the scope's watch of its state started: the watch has ended, so there is nothing to
// follow. Pi's `watch.start` throws "Watch is stopped" there, an unhandled rejection that ends a Node process (run against c23b9ec1);
// D452 records the difference.
func TestAHoldersOwnReleaseBeforeTheScopesWatchStartsEndsQuietly(t *testing.T) {
	h := newHost(t)
	for range 100 {
		scope := NewTuiScope(bg, h, func() {})
		if !isType[*TuiScope](scope) {
			t.Fatalf("NewTuiScope returned %T", scope)
		}
		held, err := scope.Hold(Notes, delta.NewJsonObject(0))
		if err != nil {
			t.Fatal(err)
		}
		held.Release()
		noTuiGoroutines(t)
		scope.Dispose()
	}
}

// noTuiGoroutines waits until no goroutine of this package is left running a follow: a goroutine that panics instead takes the test
// binary down, where a test that returns at once would never see it.
func noTuiGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		var left []string
		for stack := range strings.SplitSeq(string(buf), "\n\n") {
			if strings.Contains(stack, "chord/next/tui.follow") && !strings.Contains(stack, "testing.tRunner") {
				left = append(left, stack)
			}
		}
		if len(left) == 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("a follow goroutine is left:\n%s", strings.Join(left, "\n\n"))
		case <-tick.C:
		}
	}
}
