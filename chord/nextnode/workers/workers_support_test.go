package workers

import (
	"testing"
	"time"
)

// until waits until ready() holds, polling every 10 ms, for at most ms (test/next-node/workers/support.ts `until`).
func until(t testing.TB, ready func() bool, ms ...int) {
	t.Helper()
	limit := 10_000
	if len(ms) > 0 {
		limit = ms[0]
	}
	deadline := time.Now().Add(time.Duration(limit) * time.Millisecond)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("until: timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
