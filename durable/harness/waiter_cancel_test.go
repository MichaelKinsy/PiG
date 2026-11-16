package harness

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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
}
