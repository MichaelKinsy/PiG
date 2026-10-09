// Support for the ported examples of packages/durable/test/examples. Upstream's examples are scripts run by hand with
// `node --experimental-strip-types`; each Go example is a test that runs the same flow and asserts what the script
// prints.

package examples_test

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// background is BACKGROUND_CONTEXT: never cancelled.
var background = context.Background()

type notes struct {
	Text string `json:"text"`
}

// notesDoc is the conversation-scoped, rewindable `example.notes` document of examples 01 to 05.
var notesDoc = durable.DefineDoc(durable.DocDefinition[notes]{
	CommonDocDefinition: durable.CommonDocDefinition[notes]{Kind: "example.notes", Version: 1, Initial: func() notes { return notes{} }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
})

var ownerless = durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}

func newSession(t *testing.T) *session.SessionImpl {
	t.Helper()
	return session.CreateSession(storage.NewMemoryStorage())
}

func closeSession(t *testing.T, committer interface{ Close(context.Context) error }) {
	t.Helper()
	if err := committer.Close(background); err != nil {
		t.Fatal(err)
	}
}

func commit[T any](t *testing.T, committer durable.Committer, change func(tx durable.Tx) (T, error)) T {
	t.Helper()
	value, err := durable.Commit(background, committer, change)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// setNotes is `(await tx.doc(Notes, id)).text = text`.
func setNotes(tx durable.Tx, id durable.ConversationId, text string) error {
	draft, err := durable.TxDoc[notes](tx, notesDoc, id)
	if err != nil {
		return err
	}
	return draft.Set("text", text)
}

func appendNote(tx durable.Tx, id durable.ConversationId, data string) (durable.EntryRecord, error) {
	return tx.AppendEntry(id, durable.EntryDraft{Kind: "note", Data: data})
}

func snapshotNotes(t *testing.T, session durable.DocumentReader, id durable.ConversationId) string {
	t.Helper()
	snapshot, err := durable.Snapshot[notes](background, session, notesDoc, id)
	if err != nil || snapshot == nil {
		t.Fatalf("notes snapshot: %v %v", snapshot, err)
	}
	return snapshot.Text
}

// eventually polls until check holds, as the flush loops of the upstream test support do.
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
