package session_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// packages/durable/test/session-documents.test.ts:623 "publishes close synchronously and supports unsubscription" (Session.subscribeClose, packages/durable/src/types.ts:907):
// a close listener runs when close begins, before Close returns, in registration order; an unsubscribed listener never runs; and subscribing to a closed session is refused.
func TestSessionSubscribeCloseRunsListenersWhenCloseBeginsAndSupportsUnsubscription(t *testing.T) {
	var sess durable.Session = session.CreateSession(storage.NewMemoryStorage())
	var calls []string
	sess.SubscribeClose(func() { calls = append(calls, "first") })
	unsubscribe := sess.SubscribeClose(func() { calls = append(calls, "removed") })
	sess.SubscribeClose(func() { calls = append(calls, "second") })
	unsubscribe()
	if err := sess.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(calls); got != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("close listeners ran %v, want [first second]", calls)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("SubscribeClose on a closed session did not fail")
		}
	}()
	sess.SubscribeClose(func() {})
}
