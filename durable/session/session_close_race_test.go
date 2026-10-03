package session_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

var errCancelledClose = errors.New("caller stopped waiting")

func newHookedSession(beforeClose func()) *session.SessionImpl {
	return session.NewSessionImpl(sessiontest.NewControlledStorage(), session.Hooks{BeforeClose: beforeClose})
}

// Upstream session.ts admits a commit synchronously: commitWith checks #assertUsable and enqueues on the line in one
// turn, and close() seals admission before its close job is queued. A commit that passed admission therefore settles
// before Storage closes; a later one rejects with "Session is closed". These guard that contract under real
// concurrency, where admission and the line ticket must be taken atomically.
func TestSessionAdmissionIsAtomicWithClose(t *testing.T) {
	for range 200 {
		harness := open()
		conversationId := createConversation(t, harness)
		const writers = 8
		errs := make(chan error, writers)
		var start sync.WaitGroup
		start.Add(1)
		var group sync.WaitGroup
		for range writers {
			group.Go(func() {
				start.Wait()
				_, err := harness.Session.Commit(ctx, func(tx durable.Tx) (any, error) {
					return tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "note"})
				})
				errs <- err
			})
		}
		closed := make(chan error, 1)
		go func() {
			start.Wait()
			closed <- harness.Session.Close(ctx)
		}()
		start.Done()
		group.Wait()
		must(t, <-closed)
		close(errs)
		for err := range errs {
			if err != nil && err.Error() != "Session is closed" {
				t.Fatalf("an admitted commit must settle before Storage closes: %v", err)
			}
		}
	}
}

// Upstream close() runs close listeners synchronously before the beforeClose hook, which runs on a later microtask.
func TestSessionCloseRunsListenersBeforeBeforeClose(t *testing.T) {
	for range 50 {
		var mu sync.Mutex
		var order []string
		record := func(step string) {
			mu.Lock()
			order = append(order, step)
			mu.Unlock()
		}
		kernel := newHookedSession(func() { record("beforeClose") })
		kernel.SubscribeClose(func() {
			record("listener")
			// Yield long enough that a hook started concurrently would interleave.
			time.Sleep(100 * time.Microsecond)
			record("listener done")
		})
		must(t, kernel.Close(ctx))
		mu.Lock()
		expectEqual(t, order, []string{"listener", "listener done", "beforeClose"})
		mu.Unlock()
	}
}

// Upstream awaitWithContext rejects at once when the caller's signal is already aborted, even if close settled.
func TestSessionCloseWithCancelledContextRejects(t *testing.T) {
	harness := open()
	must(t, harness.Session.Close(ctx))
	cancelled, cancel := context.WithCancelCause(ctx)
	cancel(errCancelledClose)
	for range 100 {
		if err := harness.Session.Close(cancelled); !errors.Is(err, errCancelledClose) {
			t.Fatalf("got %v, want the cancellation cause", err)
		}
	}
}

// Upstream close() flips the closing flag and runs the close listeners in one synchronous turn, so nothing observes a
// rejected operation before the listeners have run. The Harness scheduler relies on this: its close listener sets the
// closing state that decides whether a failed commit is reported. Under concurrency, an operation rejected with
// "Session is closed" must therefore see the listeners' effects.
func TestSessionRejectionAfterCloseBeganSeesTheCloseListeners(t *testing.T) {
	for range 20 {
		harness := open()
		conversationId := createConversation(t, harness)
		entered := make(chan struct{})
		var ran atomic.Bool
		harness.Session.SubscribeClose(func() {
			close(entered)
			time.Sleep(5 * time.Millisecond)
			ran.Store(true)
		})
		closed := make(chan error, 1)
		go func() { closed <- harness.Session.Close(ctx) }()
		<-entered
		_, err := harness.Session.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "note"})
		})
		if err == nil || err.Error() != "Session is closed" {
			t.Fatalf("commit after close began = %v, want Session is closed", err)
		}
		if !ran.Load() {
			t.Fatal("the commit was rejected before the close listeners ran")
		}
		must(t, <-closed)
	}
}
