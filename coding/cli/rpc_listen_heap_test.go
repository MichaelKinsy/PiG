package cli

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The settler frees once, a quiet period after the last build of a burst, and never after it stops (D232).
func TestHeapSettlerFreesOnceAfterTheLastTouch(t *testing.T) {
	const delay = 60 * time.Millisecond
	var freed atomic.Int32
	done := make(chan struct{}, 4)
	settler := newHeapSettler(delay, func() { freed.Add(1); done <- struct{}{} })
	defer settler.stop()
	start := time.Now()
	for range 5 {
		settler.touch()
		time.Sleep(delay / 3)
	}
	last := time.Now()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the heap was never freed")
	}
	if since := time.Since(last); since < delay/2 {
		t.Fatalf("freed %v after the last touch, want about %v: a burst of touches must not free early", since, delay)
	}
	if time.Since(start) < 5*(delay/3) {
		t.Fatal("freed during the burst")
	}
	time.Sleep(3 * delay)
	if got := freed.Load(); got != 1 {
		t.Fatalf("freed %d times, want once", got)
	}
	settler.touch()
	<-done
	if got := freed.Load(); got != 2 {
		t.Fatalf("freed %d times after a second burst, want 2", got)
	}
}

func TestHeapSettlerStopCancelsAPendingFree(t *testing.T) {
	var freed atomic.Int32
	settler := newHeapSettler(40*time.Millisecond, func() { freed.Add(1) })
	settler.touch()
	settler.stop()
	settler.touch()
	time.Sleep(150 * time.Millisecond)
	if got := freed.Load(); got != 0 {
		t.Fatalf("freed %d times after stop", got)
	}
}
