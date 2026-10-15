package sqlite_test

import (
	"strings"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Pi packages/durable/src/storage/sqlite.ts SqliteStorage: each lookup and scan reads exactly what its writes committed, in ascending ID order, and a scan's cursor continues where its page stopped. Each method is called on the concrete SqliteStorage.
// Pi source: packages/durable/src/storage/sqlite/storage.ts
// mutation-checked: zeroing the results of SqliteStorage.FindDocument, SqliteStorage.ScanConversations, SqliteStorage.ScanDocuments, SqliteStorage.ScanSubmissions, SqliteStorage.ScanTasks, SqliteStorage.Submission, SqliteStorage.SubmissionByRequest, SqliteStorage.Task fails it
// Pi: packages/durable/src/storage/sqlite/storage.ts:447 (findDocument)
// Pi: packages/durable/src/storage/sqlite/storage.ts:199 (scanConversations)
// Pi: packages/durable/src/storage/sqlite/storage.ts:475 (scanDocuments)
// Pi: packages/durable/src/storage/sqlite/storage.ts:405 (scanSubmissions)
// Pi: packages/durable/src/storage/sqlite/storage.ts:359 (scanTasks)
// Pi: packages/durable/src/storage/sqlite/storage.ts:38 (submission)
// Pi: packages/durable/src/storage/sqlite/storage.ts:433 (submissionByRequest)
// Pi packages/durable/src/storage/sqlite/storage.ts SqliteStorage: each lookup and scan reads exactly what its writes committed, in ascending ID order, and a scan's cursor continues where its page stopped; the storage rejects every call after close. Each method is called on the concrete SqliteStorage.
// Pi definitions: commit packages/durable/src/storage/sqlite/storage.ts:162, conversation :193, scanConversations :199, entry :227, scanEntries :249, task :353, scanTasks :359, submission :399, scanSubmissions :405, submissionByRequest :433, findDocument :447, document :469, scanDocuments :475, close :506.
func TestSqliteStorageLookupsAndScansReadCommittedRecords(t *testing.T) {
	storage, _ := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
	createRoot(t, storage)

	// Tasks: two kinds, scanned by kind and by an unfiltered page.
	first, second := mint[durable.TaskId](t, storage), mint[durable.TaskId](t, storage)
	secondTask := pendingTask(second)
	secondTask.Kind = "other.task"
	commit(t, storage, durable.TaskWrite{Value: pendingTask(first)}, durable.TaskWrite{Value: secondTask})
	found := must(storage.Task(testContext, first))
	if found == nil || found.Id != first || found.Kind != "test.task" {
		t.Fatalf("Task(first) = %+v", found)
	}
	if missing := must(storage.Task(testContext, second+100)); missing != nil {
		t.Fatalf("Task(unknown) = %+v", missing)
	}
	kind := "other.task"
	byKind := must(storage.ScanTasks(testContext, durable.TaskQuery{Kind: &kind}, 10, nil))
	if len(byKind.Items) != 1 || byKind.Items[0].Id != second {
		t.Fatalf("ScanTasks(kind) = %+v", byKind.Items)
	}
	page := must(storage.ScanTasks(testContext, durable.TaskQuery{}, 1, nil))
	if len(page.Items) != 1 || page.Items[0].Id != first || page.Next == nil {
		t.Fatalf("first task page = %+v next=%v", page.Items, page.Next)
	}
	next := must(storage.ScanTasks(testContext, durable.TaskQuery{}, 1, *page.Next))
	if len(next.Items) != 1 || next.Items[0].Id != second {
		t.Fatalf("second task page = %+v", next.Items)
	}

	// Submissions: looked up by ID and by the conversation-scoped request ID, scanned by status.
	requestId := "req-1"
	queued, done := mint[durable.SubmissionId](t, storage), mint[durable.SubmissionId](t, storage)
	commit(t, storage,
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: queued, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &requestId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: done, ConversationId: durable.ROOT_CONVERSATION_ID, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: new(durable.EntryId(7))}},
	)
	if got := must(storage.Submission(testContext, queued)); got == nil || got.Id != queued || got.Status != durable.SubmissionQueued {
		t.Fatalf("Submission(queued) = %+v", got)
	}
	if got := must(storage.SubmissionByRequest(testContext, durable.ROOT_CONVERSATION_ID, requestId)); got == nil || got.Id != queued {
		t.Fatalf("SubmissionByRequest = %+v", got)
	}
	if got := must(storage.SubmissionByRequest(testContext, durable.ROOT_CONVERSATION_ID, "nope")); got != nil {
		t.Fatalf("SubmissionByRequest(unknown) = %+v", got)
	}
	status := durable.SubmissionDone
	doneOnly := must(storage.ScanSubmissions(testContext, durable.SubmissionQuery{Status: &status}, 10, nil))
	if len(doneOnly.Items) != 1 || doneOnly.Items[0].Id != done {
		t.Fatalf("ScanSubmissions(done) = %+v", doneOnly.Items)
	}

	// Conversations: the root scans first.
	conversations := must(storage.ScanConversations(testContext, durable.ConversationQuery{}, 10, nil))
	if len(conversations.Items) != 1 || conversations.Items[0].Id != durable.ROOT_CONVERSATION_ID {
		t.Fatalf("ScanConversations = %+v", conversations.Items)
	}

	// Documents: resolved by address and scanned by scope.
	documentId := mint[durable.DocumentId](t, storage)
	key := "member"
	record := durable.DocumentCreate{Id: documentId, Kind: "test.doc", Scope: sessionScope, Key: &key}
	commit(t, storage, durable.DocumentCreateWrite{Record: record, Content: base(chorddelta.JsonObjectOf("n", 1))})
	address := durable.DocumentAddress{Kind: "test.doc", Scope: sessionScope, Key: &key}
	if got := must(storage.FindDocument(testContext, address, durable.CurrentPoint)); got == nil || got.Id != documentId {
		t.Fatalf("FindDocument = %+v", got)
	}
	absent := "other"
	if got := must(storage.FindDocument(testContext, durable.DocumentAddress{Kind: "test.doc", Scope: sessionScope, Key: &absent}, durable.CurrentPoint)); got != nil {
		t.Fatalf("FindDocument(absent) = %+v", got)
	}
	documents := must(storage.ScanDocuments(testContext, durable.DocumentQuery{Scope: sessionScope, At: durable.CurrentPoint}, 10, nil))
	if len(documents.Items) != 1 || documents.Items[0].Id != documentId {
		t.Fatalf("ScanDocuments = %+v", documents.Items)
	}

	// Conversations by ID, entries by ID and by conversation scan, a stored document by ID, and the closed storage.
	if got := must(storage.Conversation(testContext, durable.ROOT_CONVERSATION_ID)); got == nil || got.Id != durable.ROOT_CONVERSATION_ID {
		t.Fatalf("Conversation(root) = %+v", got)
	}
	if got := must(storage.Conversation(testContext, durable.ROOT_CONVERSATION_ID+100)); got != nil {
		t.Fatalf("Conversation(unknown) = %+v", got)
	}
	must(storage.Commit(testContext, []durable.StorageWrite{
		durable.EntryWrite{Value: entryOf(10, durable.ROOT_CONVERSATION_ID, "a")}, durable.EntryWrite{Value: entryOf(20, durable.ROOT_CONVERSATION_ID, "b")},
	}))
	if got := must(storage.Entry(testContext, 20)); got == nil || got.Entry.Id != 20 || got.CommitSeq == 0 {
		t.Fatalf("Entry(20) = %+v", got)
	}
	if got := must(storage.Entry(testContext, 99)); got != nil {
		t.Fatalf("Entry(unknown) = %+v", got)
	}
	scanned := must(storage.ScanEntries(testContext, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 10, nil))
	if len(scanned.Items) != 2 || scanned.Items[0].Id != 20 || scanned.Items[1].Id != 10 {
		t.Fatalf("ScanEntries = %+v, want newest first (types.ts:1032)", scanned.Items)
	}
	if got := must(storage.Document(testContext, documentId, durable.CurrentPoint)); got == nil || got.Record.Id != documentId {
		t.Fatalf("Document = %+v", got)
	}
	if err := storage.Close(testContext); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Task(testContext, first); err == nil || !strings.Contains(err.Error(), "SqliteStorage is closed") {
		t.Fatalf("Task after Close = %v, want SqliteStorage is closed", err)
	}
}
