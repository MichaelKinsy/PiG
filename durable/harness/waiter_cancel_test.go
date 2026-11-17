package harness

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Pi orders what a publication resolves, not a wait that was rejected: a wait of a WithContinuation caller whose context was
// cancelled has no continuation a later publication waits for.
func TestACancelledWaitOfAContinuationDoesNotHoldLaterPublications(t *testing.T) {
	gate := deferred()
	gated := tkGated("test.cancelled-continuation", gate)
	opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()})
	id := tkStart(t, opened.root, gated.Erased())
	opened.harness.Resume()
	tasks := opened.harness.(*harnessImpl).tasks
	cancelling, cancel := context.WithCancelCause(testContext)
	continued, _ := session.WithContinuation(cancelling)
	waiting := asyncErr(func() error {
		_, err := opened.harness.WaitForTask(continued, id)
		return err
	})
	eventually(t, func() bool { return len(tasks.taskWaiters.Keys()) > 0 })
	// The caller is parked in its wait.
	awaitQuiescent(t)
	cause := errors.New("stop waiting")
	cancel(cause)
	if _, err := waiting.wait(); !errors.Is(err, cause) {
		t.Fatalf("the wait returned %v, want the cancellation cause", err)
	}
	writing := asyncErr(func() error {
		_, err := opened.root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}})
		return err
	})
	select {
	case <-writing.done:
	case <-time.After(10 * time.Second):
		t.Fatal("a publication waited for the continuation of a cancelled wait")
	}
	if _, err := writing.wait(); err != nil {
		t.Fatal(err)
	}
	gate.resolve()
	tkWaitOutcome(t, opened.harness, id)
	mustClose(t, opened.harness)
}

// heldCancelContext is a Context whose cancellation shows at once (Err, Done) while the callbacks registered with
// context.AfterFunc are held until release: the window in which a cancelled context has not yet run them on their own
// goroutine. A custom Context may implement AfterFunc for exactly this (context.AfterFunc).
type heldCancelContext struct {
	context.Context
	done chan struct{}
	mu   sync.Mutex
	err  error
	held []func()
}

func newHeldCancelContext() *heldCancelContext {
	return &heldCancelContext{Context: context.Background(), done: make(chan struct{})}
}

func (held *heldCancelContext) Done() <-chan struct{} { return held.done }

func (held *heldCancelContext) Err() error {
	held.mu.Lock()
	defer held.mu.Unlock()
	return held.err
}

func (held *heldCancelContext) AfterFunc(callback func()) func() bool {
	held.mu.Lock()
	defer held.mu.Unlock()
	held.held = append(held.held, callback)
	return func() bool { return true }
}

func (held *heldCancelContext) cancel() {
	held.mu.Lock()
	held.err = context.Canceled
	held.mu.Unlock()
	close(held.done)
}

func (held *heldCancelContext) release() {
	held.mu.Lock()
	callbacks := held.held
	held.held = nil
	held.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

// Pi's Waiters.add runs the abort listener at the abort itself, so a wait whose signal was aborted is gone from the set by
// the time a later resolve looks: it was rejected, and resolve does not reach it. Here the cancellation reaches the wait
// on its own goroutine, so a settle that comes first must still reject it.
func TestAWaitSettledAfterItsContextWasCancelledFailsWithTheCancellation(t *testing.T) {
	t.Run("a context cancelled before the settle", func(t *testing.T) {
		var waiters Waiters[int, string]
		ctx := newHeldCancelContext()
		waiter := waiters.Add(ctx, nil, 1)
		ctx.cancel()
		waiters.Resolve(1, "settled")
		<-waiter.Done()
		if !errors.Is(waiter.err, context.Canceled) || waiter.value != "" {
			t.Fatalf("wait = (%q, %v), want the cancellation and no value", waiter.value, waiter.err)
		}
		ctx.release()
	})
	t.Run("a context still live at the settle", func(t *testing.T) {
		var waiters Waiters[int, string]
		ctx := newHeldCancelContext()
		waiter := waiters.Add(ctx, nil, 1)
		waiters.Resolve(1, "settled")
		<-waiter.Done()
		ctx.cancel()
		ctx.release()
		if waiter.err != nil || waiter.value != "settled" {
			t.Fatalf("wait = (%q, %v), want settled and no error", waiter.value, waiter.err)
		}
	})
	t.Run("a chord context, with no callback to wait for", func(t *testing.T) {
		cause := errors.New("stop waiting")
		for range 200 {
			var waiters Waiters[int, string]
			ctx, cancel := chordctx.WithCancel(chordctx.Background())
			waiter := waiters.Add(ctx, nil, 1)
			cancel(cause)
			waiters.Resolve(1, "settled")
			<-waiter.Done()
			if !errors.Is(waiter.err, cause) || waiter.value != "" {
				t.Fatalf("wait = (%q, %v), want the cancellation cause and no value", waiter.value, waiter.err)
			}
		}
	})
	t.Run("a chord context under a foreign parent that was cancelled", func(t *testing.T) {
		cause := errors.New("parent stopped")
		for range 200 {
			var waiters Waiters[int, string]
			parent, cancelParent := context.WithCancelCause(context.Background())
			ctx, cancel := chordctx.WithCancel(parent)
			waiter := waiters.Add(ctx, nil, 1)
			cancelParent(cause)
			waiters.Resolve(1, "settled")
			<-waiter.Done()
			if !errors.Is(waiter.err, cause) || waiter.value != "" {
				t.Fatalf("wait = (%q, %v), want the parent's cause and no value", waiter.value, waiter.err)
			}
			cancel(nil)
		}
	})
}

// Pi's Waiters.rejectAll (util.ts:44-48) rejects the waits still in the set; a wait whose signal was aborted is gone from it
// and was rejected with the abort reason at the abort (first rejection wins), so a rejectAll after the abort never gives it
// the close error. A foreign context's cancellation reaches the wait on its own goroutine, so a rejectAll that comes first
// must still leave the cancellation.
func TestARejectAllAfterAWaitsContextWasCancelledKeepsTheCancellation(t *testing.T) {
	closed := errors.New("harness closed")
	t.Run("a foreign context whose cancellation has not reached the wait", func(t *testing.T) {
		var waiters Waiters[int, string]
		ctx := newHeldCancelContext()
		waiter := waiters.Add(ctx, nil, 1)
		ctx.cancel()
		waiters.RejectAll(closed)
		<-waiter.Done()
		if !errors.Is(waiter.err, context.Canceled) || errors.Is(waiter.err, closed) {
			t.Fatalf("wait error = %v, want the cancellation, not the close error", waiter.err)
		}
		ctx.release()
	})
	t.Run("a foreign context still live at the rejectAll", func(t *testing.T) {
		var waiters Waiters[int, string]
		ctx := newHeldCancelContext()
		waiter := waiters.Add(ctx, nil, 1)
		waiters.RejectAll(closed)
		<-waiter.Done()
		ctx.cancel()
		ctx.release()
		if !errors.Is(waiter.err, closed) {
			t.Fatalf("wait error = %v, want the close error", waiter.err)
		}
	})
	t.Run("a chord context, with no callback to wait for", func(t *testing.T) {
		cause := errors.New("stop waiting")
		for range 200 {
			var waiters Waiters[int, string]
			ctx, cancel := chordctx.WithCancel(chordctx.Background())
			waiter := waiters.Add(ctx, nil, 1)
			cancel(cause)
			waiters.RejectAll(closed)
			<-waiter.Done()
			if !errors.Is(waiter.err, cause) {
				t.Fatalf("wait error = %v, want the cancellation cause", waiter.err)
			}
		}
	})
}

// Pi's Waiters.keys (util.ts:34-36) lists the keys of the waits in the set, and the abort listener took an aborted wait out
// of it at the abort: a cancelled idle wait no longer counts. A foreign context's cancellation reaches the wait on its own
// goroutine, so Keys must leave the key out until it has.
func TestAWaitsKeyIsGoneOnceItsContextWasCancelled(t *testing.T) {
	t.Run("a foreign context whose cancellation has not reached the wait", func(t *testing.T) {
		var waiters Waiters[int, string]
		ctx := newHeldCancelContext()
		waiters.Add(ctx, nil, 1)
		live := newHeldCancelContext()
		waiters.Add(live, nil, 2)
		ctx.cancel()
		if keys := waiters.Keys(); len(keys) != 1 || keys[0] != 2 {
			t.Fatalf("keys = %v, want only the key of the live wait", keys)
		}
		ctx.release()
		if keys := waiters.Keys(); len(keys) != 1 || keys[0] != 2 {
			t.Fatalf("keys after the callback = %v, want only the key of the live wait", keys)
		}
		// The live wait ends with the test: its goroutine waits for it.
		waiters.RejectAll(errors.New("test over"))
	})
	t.Run("a chord context, with no callback to wait for", func(t *testing.T) {
		cause := errors.New("stop waiting")
		for range 200 {
			var waiters Waiters[int, string]
			ctx, cancel := chordctx.WithCancel(chordctx.Background())
			waiter := waiters.Add(ctx, nil, 1)
			cancel(cause)
			if keys := waiters.Keys(); len(keys) != 0 {
				t.Fatalf("keys = %v, want none once the cancel returned", keys)
			}
			select {
			case <-waiter.Done():
			default:
				t.Fatal("the wait was not rejected when the cancel returned")
			}
			if !errors.Is(waiter.err, cause) {
				t.Fatalf("wait error = %v, want the cancellation cause", waiter.err)
			}
		}
	})
}

// A cancel that runs while Add registers the wait either finds the wait in the set or leaves its cause for Add: whichever
// happens, the wait is rejected with the cause and no key is left once the cancel and Add have both returned.
func TestAWaitAddedWhileItsContextIsCancelledIsNeverLeftPending(t *testing.T) {
	cause := errors.New("aborted while registering")
	for range 20000 {
		var waiters Waiters[int, string]
		ctx, cancel := chordctx.WithCancel(chordctx.Background())
		var start, finished sync.WaitGroup
		start.Add(1)
		finished.Go(func() {
			start.Wait()
			cancel(cause)
		})
		start.Done()
		waiter := waiters.Add(ctx, nil, 1)
		finished.Wait()
		select {
		case <-waiter.Done():
		default:
			t.Fatal("a wait added while its context was cancelled was left pending after both returned")
		}
		if !errors.Is(waiter.err, cause) {
			t.Fatalf("wait error = %v, want the cancellation cause", waiter.err)
		}
		if keys := waiters.Keys(); len(keys) != 0 {
			t.Fatalf("keys = %v, want none", keys)
		}
	}
}
