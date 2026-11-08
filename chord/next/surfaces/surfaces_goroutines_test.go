package surfaces

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// noSurfacesGoroutines waits until no goroutine of this package is left: the pick watches the followers start on goroutines of their
// own end once their hold does. A goroutine that panics instead takes the test binary down, where a test that returns at once would
// never see it.
func noSurfacesGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		var left []string
		for stack := range strings.SplitSeq(string(buf), "\n\n") {
			if strings.Contains(stack, "chord/next/surfaces.") && !strings.Contains(stack, "testing.tRunner") && !strings.Contains(stack, "runtime.Stack") {
				left = append(left, stack)
			}
		}
		if len(left) == 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("goroutines of the package outlive their followers:\n%s", strings.Join(left, "\n\n"))
		case <-tick.C:
		}
	}
}

// closed fails the test when a close failed: Pi's tests await `close` and a rejection fails them.
func closed(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("close: %v", err)
	}
}
