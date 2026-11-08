package p2test

import (
	"bytes"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

// liveGoroutines is the number of goroutines that are not a host loop's idle handler coroutine: those are kept for the
// next call and stopped by the garbage collection of their loop (host.newCoroutine), so a closed host's still count
// until a collection nothing prevents (a test's own cleanups keep the host reachable).
func liveGoroutines() (int, string) {
	var profile bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 2); err != nil {
		return runtime.NumGoroutine(), ""
	}
	count := 0
	var kept []string
	for _, block := range strings.Split(profile.String(), "\n\n") {
		if strings.TrimSpace(block) == "" || strings.Contains(block, "host.newCoroutine") {
			continue
		}
		count++
		kept = append(kept, block)
	}
	return count, strings.Join(kept, "\n\n")
}

// NoLeakedGoroutines fails the test when goroutines its body started outlive its cleanups: call it first, so its
// cleanup runs last, after the hosts and fixtures the test registered closed. The count is polled (goroutines of a
// closed host end shortly after Close returns), 10 s at most.
func NoLeakedGoroutines(t testing.TB) {
	t.Helper()
	baseline, _ := liveGoroutines()
	t.Cleanup(func() {
		deadline := time.Now().Add(10 * time.Second)
		for {
			count, stacks := liveGoroutines()
			if count <= baseline {
				return
			}
			if time.Now().After(deadline) {
				os.Stderr.WriteString(stacks)
				t.Errorf("goroutines leaked: %d now, %d before", count, baseline)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}
