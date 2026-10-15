package session_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// session.ts:385 subscribeCommits adds the listener to the session's set and returns a function removing exactly that listener: disposing one
// subscription leaves the others receiving every later commit, and disposing twice is harmless.
// Pi: packages/durable/src/session/session.ts:385
func TestSessionSubscribeCommitsDisposeRemovesOnlyItsOwnListener(t *testing.T) {
	harness := open()
	conversationId := createConversation(t, harness)
	counts := map[string]int{}
	subscribe := func(name string) func() {
		return harness.Session.SubscribeCommits(func(context.Context, durable.CommitPublication) { counts[name]++ })
	}
	stopFirst := subscribe("first")
	subscribe("second")
	stopThird := subscribe("third")
	write := func(text string) {
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "note", Data: map[string]any{"text": text}})
			return err
		})
	}
	write("one")
	stopFirst()
	stopFirst() // idempotent: the second call must not remove another listener
	write("two")
	stopThird()
	write("three")
	for name, want := range map[string]int{"first": 1, "second": 3, "third": 2} {
		if counts[name] != want {
			t.Errorf("%s listener saw %d commits, want %d", name, counts[name], want)
		}
	}
}
