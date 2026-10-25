package session_test

// pi: packages/durable/src/session/session.ts

// pi: packages/durable/src/session/observation.ts

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// The Harness drives committed observations directly (view.ts:90 and :104, events.ts:132); these cases pin the exported
// constructor and lifecycle surface it uses.
func TestCommittedObservationHarnessSurface(t *testing.T) {
	t.Run("a session-bound watch delivers advanced frames and ends cancelled", func(t *testing.T) {
		harness := open()
		released := 0
		watch := session.NewCommittedWatch(harness.Session, []any{}, func() { released++ }, func() []any { return []any{"reset"} })
		frames := make(chan []any, 2)
		watch.Start(func(_ context.Context, value []any, _ []durable.Op) error { frames <- value; return nil })
		watch.Advance(context.Background(), []any{"a"}, []durable.Op{{"p", "", 0, 0, []any{"a"}}})
		expectEqual(t, <-frames, []any{"a"})
		watch.Cancel()
		<-watch.Closed()
		harness.Session.WaitDeliveries()
		if watch.End().Reason != durable.WatchCancelled || released != 1 {
			t.Fatalf("end %+v released %d", watch.End(), released)
		}
	})

	t.Run("counts live commit subscriptions", func(t *testing.T) {
		harness := open()
		before := harness.Session.CommitSubscriptions()
		unsubscribe := harness.Session.SubscribeCommits(func(context.Context, durable.CommitPublication) {})
		if harness.Session.CommitSubscriptions() != before+1 {
			t.Fatal("subscribing adds one")
		}
		unsubscribe()
		if harness.Session.CommitSubscriptions() != before {
			t.Fatal("unsubscribing removes it")
		}
	})
}

// session.ts:350-366: close listeners run synchronously when close begins, and beforeClose runs only in a later
// microtask, so a Harness's beforeClose (TaskScheduler join) always follows the listeners that seal scheduling.
func TestSessionCloseBeforeCloseFollowsYieldingListeners(t *testing.T) {
	for range 50 {
		var listenerDone atomic.Bool
		var sawListener atomic.Bool
		kernel := session.NewSessionImpl(sessiontest.NewControlledStorage(), session.Hooks{BeforeClose: func() {
			sawListener.Store(listenerDone.Load())
		}}, nil, nil)
		kernel.SubscribeClose(func() {
			for range 1000 {
				runtime.Gosched()
			}
			listenerDone.Store(true)
		})
		must(t, kernel.Close(context.Background()))
		if !sawListener.Load() {
			t.Fatal("beforeClose ran before the close listeners returned")
		}
	}
}

// transaction.ts:387-389 reads options.ownership.kind, so createTask without an ownership throws; a zero Go
// TaskOwnership is that missing ownership, and a kind outside conversation/task has no upstream form.
func TestCreateTaskRequiresOwnership(t *testing.T) {
	harness := open()
	conversationId := createConversation(t, harness)
	for _, ownership := range []durable.TaskOwnership{{}, {Kind: "other"}} {
		mints := harness.Storage.MintCount()
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.CreateTaskErased(workTask.Erased(), delta.JsonObjectOf("path", "a"), durable.TaskOptions{Ownership: ownership, ConversationId: &conversationId})
			return err
		})
		expectErrorContains(t, err, "requires options.ownership")
		if harness.Storage.MintCount() != mints {
			t.Fatal("a rejected createTask mints no ID")
		}
	}
}
