package chord

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// parkedGoroutines starts n goroutines that wait until the returned release runs, so the process holds more goroutines than the
// dump-based wait could afford to read (it was bounded at 128).
func parkedGoroutines(n int) (release func()) {
	gate := make(chan struct{})
	var started sync.WaitGroup
	for range n {
		started.Add(1)
		go func() {
			started.Done()
			<-gate
		}()
	}
	started.Wait()
	return func() { close(gate) }
}

// awaitEntered returns once every delivery scheduled before it started, at any number of goroutines in the process. With one
// processor a goroutine a delivery started does not run before the caller waits, so a wait that returned without the
// handshake would find the delivery not entered.
func TestAwaitEnteredWaitsForEveryDeliveryToStartAtAnyNumberOfGoroutines(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	release := parkedGoroutines(300)
	defer release()
	tracker := newPending()
	hold := make(chan struct{})
	for range 3 {
		tracker.schedule(func() { <-hold })
	}
	if err := tracker.awaitEntered(context.Background()); err != nil {
		t.Fatal(err)
	}
	for index, d := range tracker.deliveries {
		select {
		case <-d.entered:
		default:
			t.Fatalf("delivery %d had not started when awaitEntered returned", index)
		}
	}
	close(hold)
	tracker.wait()
}

// A delivery that waits for a lock the caller holds has entered: the caller does not wait for it to finish.
func TestAwaitEnteredDoesNotWaitForAListenerBlockedBehindTheCallersLock(t *testing.T) {
	tracker := newPending()
	var mu sync.Mutex
	mu.Lock()
	var acquired atomic.Bool
	tracker.schedule(func() {
		mu.Lock()
		defer mu.Unlock()
		acquired.Store(true)
	})
	if err := tracker.awaitEntered(context.Background()); err != nil {
		t.Fatal(err)
	}
	if acquired.Load() {
		t.Fatal("the delivery got the lock the caller holds")
	}
	mu.Unlock()
	tracker.wait()
	if !acquired.Load() {
		t.Fatal("the delivery never got the lock")
	}
}

func TestAwaitEnteredReturnsTheCauseOfACancelledContext(t *testing.T) {
	tracker := newPending()
	never := &delivery{entered: make(chan struct{}), done: make(chan struct{})}
	tracker.deliveries = append(tracker.deliveries, never)
	cause := errors.New("cancelled")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := tracker.awaitEntered(ctx); !errors.Is(err, cause) {
		t.Fatalf("awaitEntered = %v, want %v", err, cause)
	}
}
