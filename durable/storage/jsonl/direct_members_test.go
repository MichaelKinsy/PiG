package jsonl_test

import (
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi durable/src/storage/jsonl/storage.ts JsonlStorage forwards every read to the store it opened: drive the concrete
// JsonlStorage (not the durable.Storage interface) through its identity, scan and lookup members on committed data.
// Pi: packages/durable/src/storage/jsonl/storage.ts:333 (findLatestHeadMarker).
// Pi: packages/durable/src/storage/jsonl/storage.ts:308 (mintId).
// Pi: packages/durable/src/storage/jsonl/storage.ts:316 (scanConversations).
// Pi: packages/durable/src/storage/jsonl/storage.ts:373 (scanDocuments).
// Pi: packages/durable/src/storage/jsonl/storage.ts:341 (scanEntries).
// Pi: packages/durable/src/storage/jsonl/storage.ts:357 (scanSubmissions).
// Pi: packages/durable/src/storage/jsonl/storage.ts:349 (scanTasks).
// Pi: packages/durable/src/storage/jsonl/storage.ts:361 (submissionByRequest).
// Pi: packages/durable/src/storage/jsonl/storage.ts:353 (submission).
func TestJsonlStorageDirectReadMembers(t *testing.T) {
	storage := open(t, t.TempDir())
	createRoot(t, storage)

	// MintId hands out distinct, increasing candidates from one namespace.
	first, err := storage.MintId()
	mustDo(t, err)
	second, err := storage.MintId()
	mustDo(t, err)
	if second <= first {
		t.Fatalf("MintId = %d then %d, want strictly increasing", first, second)
	}

	taskID := durable.IdFromNumber[durable.TaskId](must(storage.MintId()))
	commit(t, storage, durable.TaskWrite{Value: pendingTask(taskID, "ready")})
	tasks := must(storage.ScanTasks(testContext, durable.TaskQuery{}, 10, nil))
	if len(tasks.Items) != 1 || tasks.Items[0].Id != taskID {
		t.Fatalf("ScanTasks = %+v, want the committed task", tasks.Items)
	}
	kind := "other.kind"
	if page := must(storage.ScanTasks(testContext, durable.TaskQuery{Kind: &kind}, 10, nil)); len(page.Items) != 0 {
		t.Fatalf("ScanTasks with a non-matching kind = %+v", page.Items)
	}

	firstEntry := durable.IdFromNumber[durable.EntryId](must(storage.MintId()))
	secondEntry := durable.IdFromNumber[durable.EntryId](must(storage.MintId()))
	commit(t, storage,
		durable.EntryWrite{Value: durable.EntryRecord{Id: firstEntry, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test.entry", Head: &firstEntry}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: secondEntry, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test.entry"}},
	)
	entries := must(storage.ScanEntries(testContext, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 10, nil))
	if len(entries.Items) != 2 || entries.Items[0].Id != secondEntry || entries.Items[1].Id != firstEntry {
		t.Fatalf("ScanEntries = %+v, want newest first", entries.Items)
	}
	marker := must(storage.FindLatestHeadMarker(testContext, durable.ROOT_CONVERSATION_ID, nil))
	if marker == nil || marker.Id != firstEntry {
		t.Fatalf("FindLatestHeadMarker = %+v, want the entry that selects a head", marker)
	}

	conversations := must(storage.ScanConversations(testContext, durable.ConversationQuery{}, 10, nil))
	if len(conversations.Items) != 1 || conversations.Items[0].Id != durable.ROOT_CONVERSATION_ID {
		t.Fatalf("ScanConversations = %+v, want the root conversation", conversations.Items)
	}

	submissionID := durable.IdFromNumber[durable.SubmissionId](must(storage.MintId()))
	request := "request-1"
	commit(t, storage, durable.SubmissionWrite{Value: durable.SubmissionRecord{
		Id: submissionID, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &request,
		Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued,
	}})
	byID := must(storage.Submission(testContext, submissionID))
	byRequest := must(storage.SubmissionByRequest(testContext, durable.ROOT_CONVERSATION_ID, request))
	if byID == nil || byRequest == nil || byID.Id != submissionID || byRequest.Id != submissionID {
		t.Fatalf("Submission = %+v, SubmissionByRequest = %+v", byID, byRequest)
	}
	if missing := must(storage.SubmissionByRequest(testContext, durable.ROOT_CONVERSATION_ID, "absent")); missing != nil {
		t.Fatalf("SubmissionByRequest(absent) = %+v, want nil", missing)
	}
	submissions := must(storage.ScanSubmissions(testContext, durable.SubmissionQuery{}, 10, nil))
	if len(submissions.Items) != 1 || submissions.Items[0].Id != submissionID {
		t.Fatalf("ScanSubmissions = %+v", submissions.Items)
	}

	documentID := durable.IdFromNumber[durable.DocumentId](must(storage.MintId()))
	commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(documentID, "test.doc"), Content: base(chorddelta.JsonObjectOf("a", 1))})
	documents := must(storage.ScanDocuments(testContext, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}, 10, nil))
	if len(documents.Items) != 1 || documents.Items[0].Id != documentID {
		t.Fatalf("ScanDocuments = %+v", documents.Items)
	}
}
