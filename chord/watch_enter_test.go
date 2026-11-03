package chord

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// startedWatch is a native watch on scheduler whose listener is listener.
func startedWatch(scheduler *DeliveryScheduler, listener func(ctx context.Context) error) *NativeWatch[int] {
	watch := NewNativeWatch(0, func() {}, nil, func() int { return 0 }).On(scheduler)
	watch.Start(func(ctx context.Context, _ NativeWatchCall[int]) error { return listener(ctx) })
	return watch
}

// returns runs awaitEntered on its own goroutine and reports its result, or fails after a bound, so a wait that should have
// returned fails the test instead of hanging it.
func returns(t *testing.T, scheduler *DeliveryScheduler, ctx context.Context) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- scheduler.AwaitEntered(ctx) }()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("AwaitEntered did not return")
		return nil
	}
}

// Pi runs a listener's synchronous part to its end before the code that awaited the commit resumes (watch.ts #drain queued as a
// microtask, spec.md "Two commits of one storage call are published a macrotask apart"). The listener here yields while it works,
// as a goroutine does where JavaScript runs on: a wait that returned when the delivery merely started would return before the flag.
func TestAwaitEnteredReturnsAfterTheSynchronousPartOfAListenerRan(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	parked := parkedGoroutines(300)
	defer parked()
	scheduler := NewDeliveryScheduler()
	var ran atomic.Bool
	watch := startedWatch(scheduler, func(context.Context) error {
		for range 20 {
			runtime.Gosched()
		}
		ran.Store(true)
		return nil
	})
	for round := range 50 {
		ran.Store(false)
		watch.Advance(context.Background(), round+1)
		if err := scheduler.AwaitEntered(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !ran.Load() { // watch.ts:207 queues #drain as a microtask: the listener's synchronous part runs before the awaiting code resumes
			t.Fatalf("round %d: AwaitEntered returned before the listener's synchronous part ran", round)
		}
		scheduler.Wait()
	}
}

// parkedGoroutines starts goroutines that wait until the returned release runs, so the process holds more than the 128 the
// dump-based wait could afford to read.
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

// A listener that goes on to a wait the host knows (DeliveryEntered, which the Session's Park, gate and line waits call) has
// entered: the caller does not wait for it to finish, and its synchronous part before the wait ran.
func TestAwaitEnteredDoesNotWaitForAListenerParkedInAHostWait(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	gate := make(chan struct{})
	var before atomic.Bool
	watch := startedWatch(scheduler, func(context.Context) error {
		before.Store(true)
		DeliveryEntered()
		<-gate
		return nil
	})
	watch.Advance(context.Background(), 1)
	if err := returns(t, scheduler, context.Background()); err != nil {
		t.Fatal(err)
	}
	if !before.Load() {
		t.Fatal("AwaitEntered returned before the part of the listener that precedes its wait ran")
	}
	close(gate)
	scheduler.Wait()
}

// A call from inside a delivery is itself a wait the host knows: it does not wait for its own delivery, so listeners that wait
// for the host do not wait for each other.
func TestAListenerThatAwaitsDeliveriesDoesNotWaitForItselfOrForOthersThatDo(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	var done sync.WaitGroup
	var failed atomic.Int32
	for range 2 {
		done.Add(1)
		watch := startedWatch(scheduler, func(ctx context.Context) error {
			defer done.Done()
			if err := scheduler.AwaitEntered(ctx); err != nil {
				failed.Add(1)
			}
			return nil
		})
		watch.Advance(context.Background(), 1)
	}
	finished := make(chan struct{})
	go func() { done.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("listeners that await the deliveries waited for each other")
	}
	if failed.Load() != 0 {
		t.Fatalf("%d waits failed", failed.Load())
	}
}

// A cancelled ctx ends the wait for a listener that is still at work (reloadReport's abort branch).
func TestAwaitEnteredEndsWhenItsContextIsCancelledWhileAListenerIsStillAtWork(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	var release atomic.Bool
	defer release.Store(true)
	working := make(chan struct{})
	var once sync.Once
	watch := startedWatch(scheduler, func(context.Context) error {
		once.Do(func() { close(working) })
		for !release.Load() {
			runtime.Gosched()
		}
		return nil
	})
	watch.Advance(context.Background(), 1)
	<-working
	cause := errors.New("cancelled")
	ctx, cancel := context.WithCancelCause(context.Background())
	result := make(chan error, 1)
	go func() { result <- scheduler.AwaitEntered(ctx) }()
	cancel(cause)
	select {
	case err := <-result:
		if !errors.Is(err, cause) {
			t.Fatalf("AwaitEntered = %v, want the cancellation cause", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancellation did not end the wait")
	}
	release.Store(true)
	scheduler.Wait()
}

// A promise that already resolved yields its value when it is awaited, whatever happened to the awaiting code's signal since: a
// delivery that entered is entered for a caller whose ctx was cancelled afterwards.
func TestAwaitEnteredReturnsNilForADeliveryThatEnteredEvenWhenCtxIsCancelled(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	gate := make(chan struct{})
	entered := make(chan struct{})
	watch := startedWatch(scheduler, func(context.Context) error {
		DeliveryEntered()
		close(entered)
		<-gate
		return nil
	})
	defer func() { close(gate); scheduler.Wait() }()
	watch.Advance(context.Background(), 1)
	<-entered
	cancelled, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("the caller gave up"))
	if err := returns(t, scheduler, cancelled); err != nil {
		t.Fatalf("AwaitEntered = %v, want nil: the delivery entered before the ctx was cancelled", err)
	}
}

// A listener that ends its goroutine (runtime.Goexit, as t.FailNow does) on a frame behind the first leaves no delivery that is
// never finished, so the code that awaits the deliveries does not wait for ever.
func TestAwaitEnteredReturnsWhenAListenerEndsItsGoroutineOnALaterFrame(t *testing.T) {
	scheduler := NewDeliveryScheduler()
	var calls atomic.Int32
	firstEntered := make(chan struct{})
	release := make(chan struct{})
	watch := startedWatch(scheduler, func(context.Context) error {
		if calls.Add(1) == 1 {
			DeliveryEntered()
			close(firstEntered)
			<-release
			return nil
		}
		runtime.Goexit()
		return nil
	})
	watch.Advance(context.Background(), 1)
	<-firstEntered
	// The second frame is published while the first call runs: its delivery is registered behind the first.
	watch.Advance(context.Background(), 2)
	close(release)
	// Wait until the drain's goroutine ended inside the second call: nothing is running, so only a delivery never finished is left.
	scheduler.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := scheduler.AwaitEntered(ctx); err != nil {
		t.Fatalf("AwaitEntered = %v, want nil: the listener that ended its goroutine finished its delivery", err)
	}
}
