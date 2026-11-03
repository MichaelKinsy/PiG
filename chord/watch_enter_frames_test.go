package chord

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

var frameWorkSink atomic.Int64

// Pi's drain microtask enters the listener once per published frame before the code that awaited the commit resumes, so a caller
// that awaits the deliveries after each of four publications sees four calls, never fewer (watch.ts #drain, spec.md "Two commits
// of one storage call are published a macrotask apart"). A delivery that is already running takes the next frame itself: the
// frame must be registered for AwaitEntered when it is published, or a frame published while the previous call has returned but
// the drain has not taken the watch lock yet joins the running delivery, which is already entered, and later frames fold into one
// call (round 3 review, R2: 2-27 of 2000 rounds at -cpu 2 and 8).
func TestAwaitEnteredSeesEveryFrameOfARunningDrain(t *testing.T) {
	const rounds, frames = 2000, 4
	scheduler := NewDeliveryScheduler()
	var wrong atomic.Int32
	var first sync.Once
	for round := range rounds {
		var calls atomic.Int32
		watch := NewNativeWatch(0, func() {}, nil, func() int { return 0 }).On(scheduler)
		watch.Start(func(context.Context, NativeWatchCall[int]) error {
			calls.Add(1)
			sum := int64(0)
			for i := range 3000 {
				sum += int64(i)
			}
			frameWorkSink.Add(sum)
			return nil
		})
		for frame := range frames {
			watch.Advance(context.Background(), frame+1)
			if err := scheduler.AwaitEntered(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if got := calls.Load(); got != frames {
			wrong.Add(1)
			first.Do(func() {
				t.Errorf("round %d: %d listener calls after %d awaited publications, want %d", round, got, frames, frames)
			})
		}
		scheduler.Wait()
	}
	if wrong.Load() != 0 {
		t.Fatalf("%d of %d rounds ran another number of calls than publications", wrong.Load(), rounds)
	}
}

// A listener that awaits the deliveries (a listener that waits for the Harness) while a frame waits behind its call does not wait
// for the call its own drain makes next: that call runs on the goroutine the listener blocks.
func TestAListenerThatAwaitsDeliveriesDoesNotWaitForTheNextFrameOfItsOwnDrain(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	inCall, published := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	waited := make(chan error, 1)
	watch := startedWatch(scheduler, func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(inCall)
			<-published // the second frame is queued behind this call
			waited <- scheduler.AwaitEntered(ctx)
		}
		return nil
	})
	watch.Advance(context.Background(), 1)
	<-inCall
	watch.Advance(context.Background(), 2)
	close(published)
	if err := <-waited; err != nil {
		t.Fatal(err)
	}
	scheduler.Wait()
	if calls.Load() != 2 {
		t.Fatalf("%d calls, want 2", calls.Load())
	}
}

// A call that suspended in a wait of the host (DeliveryEntered) does not hold the awaiting code back for the frame behind it: Pi's
// drain runs that frame when the listener's await resumes, after the macrotask that awaits the deliveries. A frame published behind
// the suspended call is registered, but nothing that awaits the deliveries waits for it.
func TestAwaitEnteredDoesNotWaitForTheFrameBehindASuspendedCall(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	suspended, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	watch := startedWatch(scheduler, func(context.Context) error {
		if calls.Add(1) == 1 {
			DeliveryEntered()
			close(suspended)
			<-release
		}
		return nil
	})
	watch.Advance(context.Background(), 1)
	<-suspended
	watch.Advance(context.Background(), 2)
	if err := returns(t, scheduler, context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	scheduler.Wait()
	if calls.Load() != 2 {
		t.Fatalf("%d calls, want 2", calls.Load())
	}
}
