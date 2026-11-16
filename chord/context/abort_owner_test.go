package chordctx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// gatedParent is a parent context that holds the one cancellation of a child that was derived from it, once armed, after the
// child is cancelled and before the child's cancel function goes on: the standard library asks the parent for its cancel
// context (Value) as the last step of a child's cancellation, after the child's state is set and its lock is released, so the
// first call of a cancel function of this package stands inside cancelCause with the context cancelled and its listeners not
// yet taken. No timer: inside a synctest bubble, synctest.Wait returns when the held call and every other goroutine are blocked.
type gatedParent struct {
	context.Context
	armed   atomic.Bool
	reached chan struct{}
	release chan struct{}
}

func newGatedParent(t *testing.T) *gatedParent {
	inner, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &gatedParent{Context: inner, reached: make(chan struct{}), release: make(chan struct{})}
}

func (p *gatedParent) Value(key any) any {
	if p.armed.CompareAndSwap(true, false) {
		close(p.reached)
		<-p.release
	}
	return p.Context.Value(key)
}

// hold runs cancel(cause) on a goroutine of its own, held inside the cancellation after the context is cancelled, and returns
// the channel that closes when that call has returned. The caller must release the gate.
func (p *gatedParent) hold(t *testing.T, cancel func(error), cause error) <-chan struct{} {
	t.Helper()
	p.armed.Store(true)
	returned := make(chan struct{})
	go func() {
		cancel(cause)
		close(returned)
	}()
	synctest.Wait()
	select {
	case <-p.reached:
	default:
		t.Fatal("the first cancel did not ask its parent for its cancel context: the gate that holds it after the context is cancelled is not reached")
	}
	return returned
}

// A repeated call of a cancel function dispatches nothing: the listeners of the abort belong to the call that aborted the
// context. The first call is held after the context is cancelled and before it aborts what is derived from the context and
// takes its listeners; a second call of the same function, made while it is held, must leave both to it and wait for the abort
// to be done (awaitAbort). Before, the second call took the listeners and ran them on its own goroutine, outside whatever
// section the first call was made in (a waiter's abort listener enqueues its work on the goroutine that cancels), and returned
// at once.
func TestRepeatedCancelLeavesTheListenersToTheCancelThatOwnsThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := newGatedParent(t)
		ctx, cancel := WithCancel(gate)
		var released atomic.Bool
		var mu sync.Mutex
		var ranBeforeRelease []bool
		var causes []error
		OnAbort(ctx, func(cause error) {
			mu.Lock()
			defer mu.Unlock()
			ranBeforeRelease = append(ranBeforeRelease, !released.Load())
			causes = append(causes, cause)
		})
		first, second := errors.New("first cancel"), errors.New("second cancel")

		firstReturned := gate.hold(t, cancel, first)
		if ctx.Err() == nil {
			t.Fatal("the first cancel is held after the context is cancelled, and the context is live")
		}
		secondReturned := make(chan struct{})
		var causeAtReturn error
		go func() {
			cancel(second)
			causeAtReturn = context.Cause(ctx)
			close(secondReturned)
		}()
		synctest.Wait()
		mu.Lock()
		ranBySecond := len(ranBeforeRelease)
		mu.Unlock()
		if ranBySecond != 0 {
			t.Errorf("a second cancel ran %d of the listeners the first cancel owns, on its own goroutine", ranBySecond)
		}
		select {
		case <-secondReturned:
			t.Error("a second cancel returned while the first, which owns the abort, had not aborted what is derived from the context")
		default:
		}

		released.Store(true)
		close(gate.release)
		<-firstReturned
		<-secondReturned
		if !errors.Is(causeAtReturn, first) {
			t.Errorf("a second cancel returned with the context's cause %v, want the first cancel's %v", causeAtReturn, first)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(ranBeforeRelease) != 1 || ranBeforeRelease[0] {
			t.Fatalf("the listener ran %d times, before the first cancel went on: %v; want once, in the first cancel", len(ranBeforeRelease), ranBeforeRelease)
		}
		if !errors.Is(causes[0], first) || !errors.Is(context.Cause(ctx), first) {
			t.Errorf("listener cause %v, context cause %v; want the first cancel's %v", causes[0], context.Cause(ctx), first)
		}
	})
}

// A cancel called from inside one of the listeners of its own context returns at once, as Pi's nested `abort()` does while the
// first is still running: it waits for no one, runs nothing a second time and leaves the first cause. A bubble that deadlocks
// is the failure.
func TestCancelFromAListenerReturnsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := WithCancel(Background())
		var mu sync.Mutex
		var events []string
		record := func(event string) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		}
		first := errors.New("first cancel")
		OnAbort(ctx, func(cause error) {
			record("first listener heard " + cause.Error())
			cancel(errors.New("nested cancel"))
			record("first listener's nested cancel returned")
		})
		OnAbort(ctx, func(cause error) { record("second listener heard " + cause.Error()) })
		cancel(first)
		mu.Lock()
		defer mu.Unlock()
		want := []string{"first listener heard first cancel", "first listener's nested cancel returned", "second listener heard first cancel"}
		if len(events) != len(want) {
			t.Fatalf("events %v, want %v", events, want)
		}
		for i := range want {
			if events[i] != want[i] {
				t.Fatalf("events %v, want %v", events, want)
			}
		}
		if !errors.Is(context.Cause(ctx), first) {
			t.Errorf("cause %v, want the first cancel's", context.Cause(ctx))
		}
	})
}

// A cancel with another cause after the first leaves the first cause, and runs and records nothing again.
func TestRepeatedCancelKeepsTheFirstCause(t *testing.T) {
	ctx, cancel := WithCancel(Background())
	first, second := errors.New("first cancel"), errors.New("second cancel")
	var causes []error
	OnAbort(ctx, func(cause error) { causes = append(causes, cause) })
	cancel(first)
	cancel(second)
	cancel(nil)
	if !errors.Is(context.Cause(ctx), first) || len(causes) != 1 || !errors.Is(causes[0], first) {
		t.Fatalf("cause %v, listener causes %v; want the first cancel's %v once", context.Cause(ctx), causes, first)
	}
	OnAbort(ctx, func(cause error) { causes = append(causes, cause) })
	if len(causes) != 2 || !errors.Is(causes[1], first) {
		t.Errorf("a listener added after the repeated cancels heard %v, want the first cancel's %v", causes[1:], first)
	}
}

// The calls of one cancel function that run at once agree on one abort: every listener, added before the calls or while they
// run, runs once, with the cause the context recorded, which is one of the callers'; and when any call returns the context is
// cancelled. (Not red before the cure: a listener runs once in any case. It pins what the cure must keep.)
func TestConcurrentCancelsOfOneContextRunEachListenerOnce(t *testing.T) {
	const callers, listeners = 8, 6
	ctx, cancel := WithCancel(Background())
	var runs [2 * listeners]atomic.Int32
	var wrongCause atomic.Int32
	add := func(i int) {
		OnAbort(ctx, func(cause error) {
			runs[i].Add(1)
			if !errors.Is(cause, context.Cause(ctx)) {
				wrongCause.Add(1)
			}
		})
	}
	for i := range listeners {
		add(i)
	}
	causes := make([]error, callers)
	for i := range causes {
		causes[i] = errors.New("cancel")
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var returnedLive atomic.Int32
	for i := range callers {
		wg.Go(func() {
			<-start
			cancel(causes[i])
			if ctx.Err() == nil {
				returnedLive.Add(1)
			}
		})
	}
	wg.Go(func() {
		<-start
		for i := listeners; i < 2*listeners; i++ {
			add(i)
		}
	})
	close(start)
	wg.Wait()
	for i := range runs {
		if got := runs[i].Load(); got != 1 {
			t.Errorf("listener %d ran %d times, want once", i, got)
		}
	}
	if n := wrongCause.Load(); n != 0 {
		t.Errorf("%d listeners were given a cause other than the one the context recorded", n)
	}
	if n := returnedLive.Load(); n != 0 {
		t.Errorf("%d times a cancel call returned with the context live", n)
	}
	cause := context.Cause(ctx)
	found := false
	for _, c := range causes {
		found = found || errors.Is(c, cause)
	}
	if !found {
		t.Errorf("the recorded cause %v is none of the callers'", cause)
	}
}
