package codingagent

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// drainSpinnerTicks runs queued owner-loop tasks for d of fake time and
// reports how many ran.
func drainSpinnerTicks(m *InteractiveMode, d time.Duration) int {
	ran := 0
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		for {
			select {
			case fn := <-m.uiTaskCh:
				fn()
				ran++
				continue
			default:
			}
			break
		}
	}
	return ran
}

// With no status indicator and no run, the spinner ticker parks: an idle
// session sees no timer fires, like Pi, whose Loader holds a setInterval
// only while it exists. Showing an indicator resumes the frames.
func TestSpinnerTickerParksWhileIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := statusBorderMode(t, true)
		m.isIdle = true
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); m.tickSpinner(ctx) }()
		defer func() { cancel(); <-done }()

		drainSpinnerTicks(m, 200*time.Millisecond) // first tick observes idle
		if got := drainSpinnerTicks(m, 2*time.Second); got != 0 {
			t.Fatalf("idle spinner ticker fired %d times in 2s, want 0", got)
		}

		m.startWorkingLoader()
		if got := drainSpinnerTicks(m, time.Second); got < 10 {
			t.Fatalf("active indicator ticked %d times in 1s, want about 12", got)
		}

		m.stopWorkingLoader()
		drainSpinnerTicks(m, 200*time.Millisecond)
		if got := drainSpinnerTicks(m, 2*time.Second); got != 0 {
			t.Fatalf("spinner ticker fired %d times in 2s after the indicator cleared, want 0", got)
		}
	})
}

var idleCollectSink [][]byte

// A run followed by idleCollectAfter of idle returns the heap to the OS once;
// further idle time, or an idle period with no run before it, collects nothing.
func TestIdleHeapCollectionAfterRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := statusBorderMode(t, true)
		m.isIdle = true
		m.idleCollector.lastHeap = 0
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); m.tickSpinner(ctx) }()
		defer func() { cancel(); <-done }()

		drainSpinnerTicks(m, 3*time.Second)
		if m.idleCollector.collects.Load() != 0 {
			t.Fatalf("idle with no run collected %d times, want 0", m.idleCollector.collects.Load())
		}
		m.startWorkingLoader()
		drainSpinnerTicks(m, 500*time.Millisecond)
		idleCollectSink = append(idleCollectSink, make([]byte, 1<<20))
		m.stopWorkingLoader()
		drainSpinnerTicks(m, 300*time.Millisecond)
		if m.idleCollector.collects.Load() != 0 {
			t.Fatalf("collected %d times before %v of idle", m.idleCollector.collects.Load(), idleCollectAfter)
		}
		drainSpinnerTicks(m, 2*time.Second)
		if m.idleCollector.collects.Load() != 1 {
			t.Fatalf("collected %d times after a run and %v idle, want 1", m.idleCollector.collects.Load(), idleCollectAfter)
		}
		drainSpinnerTicks(m, 5*time.Second)
		if m.idleCollector.collects.Load() != 1 {
			t.Fatalf("collected %d times over longer idle, want 1", m.idleCollector.collects.Load())
		}
		idleCollectSink = nil
	})
}
