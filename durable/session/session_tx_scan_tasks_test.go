package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// transaction.ts:264-265 scanTasks reads through to storage.scanTasks(query, limit, cursor): the query filters, the limit and cursor page the
// result in ascending task ID order, and like every table read it fails once the transaction has written a table row (#read, transaction.ts).
// Pi: packages/durable/src/session/transaction.ts:264-265
func TestSessionTransactionScanTasksFiltersPagesAndFailsAfterAWrite(t *testing.T) {
	harness := open()
	first := createConversation(t, harness)
	second := createConversation(t, harness)
	a1 := createWorkTask(t, harness, first, false)
	a2 := createWorkTask(t, harness, first, false)
	a3 := createWorkTask(t, harness, first, false)
	b1 := createWorkTask(t, harness, second, false)
	ids := func(items []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) []durable.TaskId {
		out := []durable.TaskId{}
		for _, item := range items {
			out = append(out, item.Id)
		}
		return out
	}
	commit(t, harness.Session, func(tx durable.Tx) error {
		page, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &first}, 2, nil)
		must(t, err)
		expectEqual(t, ids(page.Items), []durable.TaskId{a1, a2})
		if page.Next == nil {
			t.Fatal("a page cut by the limit has a continuation")
		}
		rest, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &first}, 2, *page.Next)
		must(t, err)
		expectEqual(t, ids(rest.Items), []durable.TaskId{a3})
		if rest.Next != nil {
			t.Fatal("the last page has no continuation")
		}
		other, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &second}, 10, nil)
		must(t, err)
		expectEqual(t, ids(other.Items), []durable.TaskId{b1})
		_, err = tx.AppendEntry(first, durable.EntryDraft{Kind: "note"})
		must(t, err)
		if _, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &first}, 10, nil); !isReadAfterWrite(err) {
			t.Fatalf("scanTasks after a write: %v", err)
		}
		return nil
	})
}
