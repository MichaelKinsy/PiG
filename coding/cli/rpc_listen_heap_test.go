package cli

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// The settler frees once, a quiet period after the last build of a burst, and never after it stops (D232).
func TestHeapSettlerFreesOnceAfterTheLastTouch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const delay = 60 * time.Millisecond
		var freed atomic.Int32
		settler := newHeapSettler(delay, func() { freed.Add(1) })
		defer settler.stop()

		for i := range 5 {
			if i > 0 {
				time.Sleep(delay / 3)
				synctest.Wait()
			}
			settler.touch()
		}
		if got := freed.Load(); got != 0 {
			t.Fatalf("freed %d times during the burst, want 0", got)
		}

		time.Sleep(delay - time.Nanosecond)
		synctest.Wait()
		if got := freed.Load(); got != 0 {
			t.Fatalf("freed %d times before delay elapsed after last touch, want 0", got)
		}

		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := freed.Load(); got != 1 {
			t.Fatalf("freed %d times after delay elapsed, want 1", got)
		}

		time.Sleep(3 * delay)
		synctest.Wait()
		if got := freed.Load(); got != 1 {
			t.Fatalf("freed %d times after quiet tail, want 1", got)
		}

		settler.touch()
		time.Sleep(delay)
		synctest.Wait()
		if got := freed.Load(); got != 2 {
			t.Fatalf("freed %d times after a second burst, want 2", got)
		}
	})
}

func TestHeapSettlerStopCancelsAPendingFree(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var freed atomic.Int32
		settler := newHeapSettler(40*time.Millisecond, func() { freed.Add(1) })
		settler.touch()
		settler.stop()
		settler.touch()
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()
		if got := freed.Load(); got != 0 {
			t.Fatalf("freed %d times after stop", got)
		}
	})
}
