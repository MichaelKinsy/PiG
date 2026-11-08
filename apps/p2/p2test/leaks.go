package p2test

import (
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Go-only: a goroutine leak check for the tests of long-lived goroutines (a lock worker's routine, a server's jobs):
// the goroutines that exist when it is armed are the baseline, and every other one must be gone shortly after the
// test closed what it started.

var goroutineHeader = regexp.MustCompile(`(?m)^goroutine (\d+) \[`)

func goroutines() map[string]string {
	buffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			buffer = buffer[:n]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	found := map[string]string{}
	for _, stack := range strings.Split(string(buffer), "\n\n") {
		if match := goroutineHeader.FindStringSubmatch(stack); match != nil {
			found[match[1]] = stack
		}
	}
	return found
}

// ignorable is a goroutine that is no leak of a test: the test runner's own and the runtime's.
func ignorable(stack string) bool {
	// An idle handler coroutine the host loop pools (the collector stops the ones it dropped): its stack holds no plugin code.
	if strings.Contains(stack, "host.newCoroutine") && !strings.Contains(stack, "apps/p2/plugins") {
		return true
	}
	for _, marker := range []string{"testing.(*T).Run", "testing.tRunner", "testing.(*M)", "runtime.goexit", "os/signal.", "runtime.ensureSigM", "created by testing."} {
		if strings.Contains(stack, marker) && !strings.Contains(stack, "apps/p2/") {
			return true
		}
	}
	return false
}

// CheckLeaks arms a leak check that runs when the test's cleanups are done (call it first in the test, so that it runs
// after the test host, the store and everything else the test closes): it fails the test, listing their stacks, when a
// goroutine that did not exist at arming is still there after a wait of up to timeout (10 s by default when zero).
func CheckLeaks(t testing.TB, timeout time.Duration) {
	t.Helper()
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	before := goroutines()
	t.Cleanup(func() {
		var leaked []string
		deadline := time.Now().Add(timeout)
		for {
			leaked = leaked[:0]
			for id, stack := range goroutines() {
				if _, existed := before[id]; !existed && !ignorable(stack) {
					leaked = append(leaked, stack)
				}
			}
			if len(leaked) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("%d goroutines outlived the test:\n%s", len(leaked), strings.Join(leaked, "\n\n"))
				return
			}
			// The host loop pools idle handler coroutines and lets the collector stop the ones it dropped.
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
	})
}
