package session_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Tx#read (packages/durable/src/session/transaction.ts:918-927) throws `new ReadAfterWrite(method)` for every table read once the transaction has written a table, and
// ReadAfterWrite (errors.ts:3-9) is named "ReadAfterWrite" with the message "Tx.<method>() cannot read tables after the first table write". The nine reads (conversation,
// entry, task, scanConversations, scanEntries, latestHeadMarker, scanTasks, submission, submissionByRequest: transaction.ts:230-275) each carry their own method name, and
// a read made before the write is unaffected.
// mutation-checked: naming a read's method wrongly, skipping the table-write check in one read, or changing the message or name fails it
func TestEveryTableReadFailsWithItsOwnNameAfterATableWrite(t *testing.T) {
	harness := open()
	conversationID := createConversation(t, harness)
	commitWith(t, harness.Session, func(tx *session.Transaction) error {
		if _, err := tx.Conversation(conversationID); err != nil {
			t.Fatalf("a read before any table write failed: %v", err)
		}
		if _, err := tx.AppendEntry(conversationID, durable.EntryDraft{Kind: "note"}); err != nil {
			t.Fatal(err)
		}
		reads := map[string]func() error{
			"conversation":      func() error { _, err := tx.Conversation(conversationID); return err },
			"entry":             func() error { _, err := tx.Entry(1); return err },
			"task":              func() error { _, err := tx.Task(1); return err },
			"scanConversations": func() error { _, err := tx.ScanConversations(durable.ConversationQuery{}, 1, nil); return err },
			"scanEntries": func() error {
				_, err := tx.ScanEntries(durable.EntryQuery{ConversationId: conversationID}, 1, nil)
				return err
			},
			"latestHeadMarker":    func() error { _, err := tx.LatestHeadMarker(conversationID); return err },
			"scanTasks":           func() error { _, err := tx.ScanTasks(durable.TaskQuery{}, 1, nil); return err },
			"submission":          func() error { _, err := tx.Submission(1); return err },
			"submissionByRequest": func() error { _, err := tx.SubmissionByRequest(conversationID, "r"); return err },
		}
		for method, read := range reads {
			err := read()
			if !isReadAfterWrite(err) {
				t.Errorf("%s after a table write = %v, want ReadAfterWrite", method, err)
				continue
			}
			if failure, ok := errors.AsType[*durable.ReadAfterWrite](err); !ok || failure.Name() != "ReadAfterWrite" {
				t.Errorf("%s error = %v, want a ReadAfterWrite named ReadAfterWrite", method, err)
			}
			if want := "Tx." + method + "() cannot read tables after the first table write"; err.Error() != want {
				t.Errorf("%s message = %q, want %q", method, err.Error(), want)
			}
		}
		return nil
	})
}
