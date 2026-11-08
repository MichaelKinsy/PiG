package cli

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Several exits can be in flight when a shutdown ends and a repeated signal arrives. The cleanup, the exit.log line and the process exit run once, in the exit that reaches the commit first, whichever it is.
func TestRPCExitRunsItsStepsOnce(t *testing.T) {
	for range 100 {
		var cleanups, records, ends, running atomic.Int32
		var concurrent atomic.Bool
		var code atomic.Int32
		e := newRPCExit(func() {}, func() {}, func() {
			if running.Add(1) > 1 {
				concurrent.Store(true)
			}
			cleanups.Add(1)
			// A slow cleanup keeps the owner inside it while the other exits arrive.
			time.Sleep(time.Millisecond)
			running.Add(-1)
		}, func(int, bool) { records.Add(1) }, func(c int) {
			ends.Add(1)
			code.Store(int32(c))
		})
		var wg sync.WaitGroup
		for i := range 8 {
			// Even callers are a shutdown's own exits, which never return before the owner ended; odd ones are repeated signals.
			wg.Go(func() {
				if i%2 == 0 {
					e.run(0, true, false)
				} else {
					e.run(143, false, true)
				}
			})
		}
		returned := make(chan struct{})
		go func() { wg.Wait(); close(returned) }()
		select {
		case <-returned:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("an exit did not return after the owner ended the process")
		}
		if cleanups.Load() != 1 || records.Load() != 1 || ends.Load() != 1 || concurrent.Load() {
			t.Fatalf("cleanup ran %d times (concurrently: %v), record %d, end %d, want once each", cleanups.Load(), concurrent.Load(), records.Load(), ends.Load())
		}
		if c := code.Load(); c != 0 && c != 143 {
			t.Fatalf("exit code %d, want 0 or 143", c)
		}
	}
}
