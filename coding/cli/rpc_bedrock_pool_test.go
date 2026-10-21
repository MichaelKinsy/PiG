package cli

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A call that ends its goroutine, as t.Fatal does in runBedrockRPCOnce, must not stop the pool from running the calls after it. The
// previous pool fed eight worker goroutines through an unbuffered channel; eight Fatal calls ended all of them and the feeder then
// blocked on its next send, so the test hung until the package timeout.
func TestBedrockRunPoolRunsEveryCallAfterCallsThatEndTheirGoroutine(t *testing.T) {
	const runs, workers = 40, 8
	var started, finished atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runBedrockPool(runs, workers, func(run int) {
			started.Add(1)
			if run < 2*workers {
				runtime.Goexit() // what t.FailNow does
			}
			finished.Add(1)
		})
	}()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("the pool never returned: %d of %d calls started, %d ran to the end", started.Load(), runs, finished.Load())
	}
	if started.Load() != runs || finished.Load() != runs-2*workers {
		t.Fatalf("%d of %d calls started and %d ran to the end, want all started and %d ended normally", started.Load(), runs, finished.Load(), runs-2*workers)
	}
}

// At most workers calls run at once.
func TestBedrockRunPoolBoundsConcurrency(t *testing.T) {
	const runs, workers = 64, 4
	var running, peak atomic.Int32
	runBedrockPool(runs, workers, func(int) {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		running.Add(-1)
	})
	if got := peak.Load(); got > workers || got < 1 {
		t.Fatalf("peak concurrency = %d, want between 1 and %d", got, workers)
	}
}
