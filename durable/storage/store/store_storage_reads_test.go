package store

// pi: packages/durable/src/storage/store/queries.ts

// Pi storage/store/queries.ts and storage.ts: every read of StoreStorage (task, scanTasks, submission, submissionByRequest,
// scanSubmissions, scanConversations, visibleEntry, findDocument, scanDocuments) returns what the commit wrote and nothing
// else: an id or request that was never committed reads as absent, and a scan applies each filter of its query.

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

func TestStoreStorageEveryReadReturnsWhatWasCommittedAndNothingElse(t *testing.T) {
	store, storage := recorded(t)
	root := durable.ROOT_CONVERSATION_ID
	child := mintID[durable.ConversationId](t, storage)
	entryID := mintID[durable.EntryId](t, storage)
	taskID := mintID[durable.TaskId](t, storage)
	doneTaskID := mintID[durable.TaskId](t, storage)
	queuedID := mintID[durable.SubmissionId](t, storage)
	doneID := mintID[durable.SubmissionId](t, storage)
	documentID := mintID[durable.DocumentId](t, storage)
	request, key := "request-1", "family-key"
	checkpoint := durable.JsonValue(map[string]any{"step": 1})
	pending := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
		Id: taskID, ConversationId: root, Kind: "store.reads", Version: 1, Input: map[string]any{"in": 1},
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint},
	}
	finished := pending
	finished.Id, finished.State = doneTaskID, durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal}
	queued := durable.SubmissionRecord{Id: queuedID, ConversationId: root, RequestId: &request, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}
	done := durable.SubmissionRecord{Id: doneID, ConversationId: root, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &entryID}
	scope := durable.DocumentRecordScope{Kind: durable.ScopeSession}
	seq := value(commitWrites(storage,
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: child}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: entryID, ConversationId: root, Kind: "plain", Data: map[string]any{"n": 1}}},
		durable.TaskWrite{Value: pending},
		durable.TaskWrite{Value: finished},
		durable.SubmissionWrite{Value: queued},
		durable.SubmissionWrite{Value: done},
		durable.DocumentCreateWrite{
			Record:  durable.DocumentCreate{Id: documentID, Kind: "store.family", Key: &key, Scope: scope},
			Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("v", 1)},
		},
	))

	if got := value(storage.Task(background, taskID)); got == nil || jsonOf(t, *got) != jsonOf(t, pending) {
		t.Fatalf("Task = %+v, want %+v", got, pending)
	}
	if got := value(storage.Task(background, mintID[durable.TaskId](t, storage))); got != nil {
		t.Fatalf("Task of an id never committed = %+v, want none", got)
	}
	status := durable.TaskPending
	if page := value(storage.ScanTasks(background, durable.TaskQuery{Status: &status}, 10, nil)); len(page.Items) != 1 || page.Items[0].Id != taskID {
		t.Fatalf("ScanTasks(pending) = %+v, want only task %d", page.Items, taskID)
	}
	if page := value(storage.ScanTasks(background, durable.TaskQuery{}, 10, nil)); len(page.Items) != 2 {
		t.Fatalf("ScanTasks() = %+v, want both tasks", page.Items)
	}

	if got := value(storage.Submission(background, queuedID)); got == nil || jsonOf(t, *got) != jsonOf(t, queued) {
		t.Fatalf("Submission = %+v, want %+v", got, queued)
	}
	if got := value(storage.Submission(background, mintID[durable.SubmissionId](t, storage))); got != nil {
		t.Fatalf("Submission of an id never committed = %+v, want none", got)
	}
	if got := value(storage.SubmissionByRequest(background, root, request)); got == nil || got.Id != queuedID {
		t.Fatalf("SubmissionByRequest = %+v, want submission %d", got, queuedID)
	}
	if got := value(storage.SubmissionByRequest(background, root, "other")); got != nil {
		t.Fatalf("SubmissionByRequest of an unknown request = %+v, want none", got)
	}
	finishedStatus := durable.SubmissionDone
	if page := value(storage.ScanSubmissions(background, durable.SubmissionQuery{Status: &finishedStatus}, 10, nil)); len(page.Items) != 1 || page.Items[0].Id != doneID {
		t.Fatalf("ScanSubmissions(done) = %+v, want only submission %d", page.Items, doneID)
	}

	first := value(storage.ScanConversations(background, durable.ConversationQuery{}, 1, nil))
	if len(first.Items) != 1 || first.Items[0].Id != root || first.Next == nil {
		t.Fatalf("ScanConversations(limit 1) = %+v, want the root and a cursor", first)
	}
	if second := value(storage.ScanConversations(background, durable.ConversationQuery{}, 1, *first.Next)); len(second.Items) != 1 || second.Items[0].Id != child || second.Next != nil {
		t.Fatalf("ScanConversations after the cursor = %+v, want only conversation %d and no cursor", second, child)
	}
	if at := value(storage.VisibleEntry(background, root, entryID)); at == nil || at.CommitSeq != seq || at.Entry.Id != entryID {
		t.Fatalf("VisibleEntry = %+v, want entry %d at commit %d", at, entryID, seq)
	}
	if at := value(storage.VisibleEntry(background, root, mintID[durable.EntryId](t, storage))); at != nil {
		t.Fatalf("VisibleEntry of an entry never committed = %+v, want none", at)
	}

	address := durable.DocumentAddress{Kind: "store.family", Scope: scope, Key: &key}
	if record := value(storage.FindDocument(background, address, durable.CurrentPoint)); record == nil || record.Id != documentID {
		t.Fatalf("FindDocument = %+v, want document %d", record, documentID)
	}
	other := "other-key"
	if record := value(storage.FindDocument(background, durable.DocumentAddress{Kind: "store.family", Scope: scope, Key: &other}, durable.CurrentPoint)); record != nil {
		t.Fatalf("FindDocument of an address never created = %+v, want none", record)
	}
	if page := value(storage.ScanDocuments(background, durable.DocumentQuery{Scope: scope, At: durable.CurrentPoint}, 10, nil)); len(page.Items) != 1 || page.Items[0].Id != documentID {
		t.Fatalf("ScanDocuments = %+v, want only document %d", page.Items, documentID)
	}
	elsewhere := "other.family"
	if page := value(storage.ScanDocuments(background, durable.DocumentQuery{Scope: scope, Kind: &elsewhere, At: durable.CurrentPoint}, 10, nil)); len(page.Items) != 0 {
		t.Fatalf("ScanDocuments of another kind = %+v, want none", page.Items)
	}
	must(t, store.Close(background))
}
