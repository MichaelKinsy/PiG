package jsonl_test

import (
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
)

// Pi storage/jsonl/storage.ts:377-380 close() and :841-844 assertUsable(): close is idempotent (`if (this.closed) return`), and every later
// operation throws exactly "JsonlStorage is closed", whether it is a read, a mint or a commit. assertUsable tests `closed` before `poisonError`,
// so a storage that was poisoned and then closed reports the closure, not the poison.
// mutation-checked: never setting the closed flag, dropping the closed check, testing the poison first, or changing the message fails it.
func TestJsonlStorageEveryOperationAfterCloseFailsWithTheClosedMessage(t *testing.T) {
	const closedMessage = "JsonlStorage is closed"
	for _, poisoned := range []bool{false, true} {
		directory := tempDirectory(t)
		instrumented := newInstrumentedEnv(directory)
		storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
		createRoot(t, storage)
		documentId := mint[durable.DocumentId](t, storage)
		if poisoned {
			instrumented.fail(failure{operation: "append", call: 1, mode: "before"})
			if _, err := storage.Commit(testContext, []durable.StorageWrite{
				durable.DocumentCreateWrite{Record: sessionDocument(documentId, "first"), Content: base(chorddelta.JsonObjectOf("text", "α"))},
			}); err == nil {
				t.Fatal("the failed publication did not fail the commit")
			}
		}
		if err := storage.Close(testContext); err != nil {
			t.Fatalf("poisoned=%v: Close = %v", poisoned, err)
		}
		if err := storage.Close(testContext); err != nil {
			t.Fatalf("poisoned=%v: a second Close = %v, want nil", poisoned, err)
		}

		root := durable.ROOT_CONVERSATION_ID
		entryId := durable.IdFromNumber[durable.EntryId](1)
		point := durable.CurrentPoint
		_, mintErr := storage.MintId()
		_, commitErr := storage.Commit(testContext, nil)
		_, conversationErr := storage.Conversation(testContext, root)
		_, scanConversationsErr := storage.ScanConversations(testContext, durable.ConversationQuery{}, 1, nil)
		_, entryErr := storage.Entry(testContext, entryId)
		_, visibleErr := storage.VisibleEntry(testContext, root, entryId)
		_, headErr := storage.FindLatestHeadMarker(testContext, root, nil)
		_, scanEntriesErr := storage.ScanEntries(testContext, durable.EntryQuery{ConversationId: root}, 1, nil)
		_, taskErr := storage.Task(testContext, durable.IdFromNumber[durable.TaskId](1))
		_, scanTasksErr := storage.ScanTasks(testContext, durable.TaskQuery{}, 1, nil)
		_, submissionErr := storage.Submission(testContext, durable.IdFromNumber[durable.SubmissionId](1))
		_, scanSubmissionsErr := storage.ScanSubmissions(testContext, durable.SubmissionQuery{}, 1, nil)
		_, byRequestErr := storage.SubmissionByRequest(testContext, root, "request")
		_, findDocumentErr := storage.FindDocument(testContext, durable.DocumentAddress{}, point)
		_, documentErr := storage.Document(testContext, documentId, point)
		_, scanDocumentsErr := storage.ScanDocuments(testContext, durable.DocumentQuery{}, 1, nil)
		for name, err := range map[string]error{
			"MintId": mintErr, "Commit": commitErr, "Conversation": conversationErr, "ScanConversations": scanConversationsErr,
			"Entry": entryErr, "VisibleEntry": visibleErr, "FindLatestHeadMarker": headErr, "ScanEntries": scanEntriesErr,
			"Task": taskErr, "ScanTasks": scanTasksErr, "Submission": submissionErr, "ScanSubmissions": scanSubmissionsErr,
			"SubmissionByRequest": byRequestErr, "FindDocument": findDocumentErr, "Document": documentErr, "ScanDocuments": scanDocumentsErr,
		} {
			if err == nil || err.Error() != closedMessage {
				t.Errorf("poisoned=%v: %s after Close = %v, want %q", poisoned, name, err, closedMessage)
			}
		}
	}
}
