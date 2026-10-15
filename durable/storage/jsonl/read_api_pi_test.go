package jsonl_test

import (
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// The read API of JsonlStorage, asked of one seeded store: packages/durable/src/storage/jsonl/storage.ts:277-377 (commit, conversation, scanConversations, entry, findLatestHeadMarker, scanEntries, task, scanTasks, submission, scanSubmissions, submissionByRequest, findDocument, document, scanDocuments, close) delegates to the shared storage contract that packages/durable/src/storage/memory.ts implements, so each result below is the contract's: lookups return the latest complete record or undefined, scans are ordered and paged by a backend cursor, and every call after close fails.
func TestJsonlStorageReadApiMirrorsPisStorageContract(t *testing.T) {
	storage := open(t, tempDirectory(t))
	createRoot(t, storage)
	if first, second := must(storage.MintId()), must(storage.MintId()); first <= 0 || second <= first {
		t.Fatalf("MintId must hand out fresh increasing numbers: %d then %d", first, second)
	}
	taskId := mint[durable.TaskId](t, storage)
	other := mint[durable.TaskId](t, storage)
	child := mint[durable.ConversationId](t, storage)
	e1, e2, e3 := mint[durable.EntryId](t, storage), mint[durable.EntryId](t, storage), mint[durable.EntryId](t, storage)
	childEntry := mint[durable.EntryId](t, storage)
	queued, done := mint[durable.SubmissionId](t, storage), mint[durable.SubmissionId](t, storage)
	singleton, member := mint[durable.DocumentId](t, storage), mint[durable.DocumentId](t, storage)
	if singleton == member || e1 >= e2 || e2 >= e3 {
		t.Fatalf("MintId must hand out fresh increasing numbers: %v %v %v %v", e1, e2, e3, singleton)
	}
	requestId := "req-1"
	key := "k1"
	headOfTwo := e1
	seq := commit(t, storage,
		durable.TaskWrite{Value: pendingTask(taskId, "ready")},
		durable.TaskWrite{Value: terminalTask(other)},
		durable.EntryWrite{Value: durable.EntryRecord{Id: e1, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "a"}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: e2, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "b", Head: &headOfTwo}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: e3, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "c"}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: child, Parent: &durable.ConversationParent{ConversationId: durable.ROOT_CONVERSATION_ID, At: e2}, Owner: &durable.ConversationOwner{ConversationId: durable.ROOT_CONVERSATION_ID, TaskId: taskId}}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: childEntry, ConversationId: child, Kind: "d"}},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: queued, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &requestId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: done, ConversationId: child, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &childEntry}},
		durable.DocumentCreateWrite{Record: sessionDocument(singleton, "note"), Content: base(chorddelta.JsonObjectOf("n", 1.0))},
		durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: member, Kind: "fam", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, Content: base(chorddelta.JsonObjectOf("m", 2.0))},
	)

	// conversation: exact lookup, undefined when absent.
	expectEqual(t, must(storage.Conversation(testContext, child)).Owner, &durable.ConversationOwner{ConversationId: durable.ROOT_CONVERSATION_ID, TaskId: taskId})
	{
		absent, err := storage.Conversation(testContext, child+1000)
		expectNil(t, absent, err)
	}

	// scanConversations: ascending ID order, optional owner filter, a limit pages with a backend cursor that finishes with no Next.
	all := must(storage.ScanConversations(testContext, durable.ConversationQuery{}, 10, nil))
	expectEqual(t, []durable.ConversationId{all.Items[0].Id, all.Items[1].Id}, []durable.ConversationId{durable.ROOT_CONVERSATION_ID, child})
	if all.Next != nil {
		t.Fatalf("a scan that returned everything has no cursor: %v", *all.Next)
	}
	owned := must(storage.ScanConversations(testContext, durable.ConversationQuery{OwnerTaskId: &taskId}, 10, nil))
	expectEqual(t, len(owned.Items), 1)
	first := must(storage.ScanConversations(testContext, durable.ConversationQuery{}, 1, nil))
	if len(first.Items) != 1 || first.Next == nil {
		t.Fatalf("limit 1 must leave a cursor: %+v", first)
	}
	second := must(storage.ScanConversations(testContext, durable.ConversationQuery{}, 1, *first.Next))
	if len(second.Items) != 1 || second.Items[0].Id != child {
		t.Fatalf("the cursor continues after the first conversation: %+v", second)
	}

	// entry: the global entry and the sequence of its commit; undefined when absent. Visible entry honors ancestry.
	found := must(storage.Entry(testContext, e2))
	if found == nil || found.Entry.Kind != "b" || found.CommitSeq != seq {
		t.Fatalf("entry = %+v, want kind b at commit %d", found, seq)
	}
	{
		absent, err := storage.Entry(testContext, childEntry+1000)
		expectNil(t, absent, err)
	}
	visible := must(storage.VisibleEntry(testContext, child, e1))
	if visible == nil || visible.Entry.Id != e1 {
		t.Fatalf("a child sees the entries of the history it forked from: %+v", visible)
	}
	if hidden := must(storage.VisibleEntry(testContext, child, e3)); hidden != nil {
		t.Fatalf("an entry after the fork point is not visible: %+v", hidden)
	}

	// findLatestHeadMarker: the newest visible entry with a head at or below the cutoff.
	marker := must(storage.FindLatestHeadMarker(testContext, durable.ROOT_CONVERSATION_ID, nil))
	if marker == nil || marker.Id != e2 {
		t.Fatalf("latest head marker = %+v, want %d", marker, e2)
	}
	{
		absent, err := storage.FindLatestHeadMarker(testContext, durable.ROOT_CONVERSATION_ID, &e1)
		expectNil(t, absent, err)
	}

	// scanEntries: newest first within the inclusive bounds, at most limit entries.
	page := must(storage.ScanEntries(testContext, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 2, nil))
	expectEqual(t, []durable.EntryId{page.Items[0].Id, page.Items[1].Id}, []durable.EntryId{e3, e2})
	if page.Next == nil {
		t.Fatal("two of three entries leave a cursor")
	}
	rest := must(storage.ScanEntries(testContext, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 2, *page.Next))
	expectEqual(t, len(rest.Items), 1)
	bounded := must(storage.ScanEntries(testContext, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, MinEntryId: &e2, MaxEntryId: &e2}, 5, nil))
	expectEqual(t, len(bounded.Items), 1)

	// task and scanTasks: the latest complete record; scans match every supplied filter.
	expectEqual(t, must(storage.Task(testContext, taskId)).Id, taskId)
	{
		absent, err := storage.Task(testContext, other+1000)
		expectNil(t, absent, err)
	}
	pending := durable.TaskPending
	filtered := must(storage.ScanTasks(testContext, durable.TaskQuery{Status: &pending}, 10, nil))
	expectEqual(t, len(filtered.Items), 1)
	expectEqual(t, filtered.Items[0].Id, taskId)
	expectEqual(t, len(must(storage.ScanTasks(testContext, durable.TaskQuery{}, 10, nil)).Items), 2)

	// submission, scanSubmissions and submissionByRequest: ascending IDs, filters, conversation-scoped request keys.
	expectEqual(t, must(storage.Submission(testContext, queued)).Status, durable.SubmissionQueued)
	{
		absent, err := storage.Submission(testContext, done+1000)
		expectNil(t, absent, err)
	}
	scanned := must(storage.ScanSubmissions(testContext, durable.SubmissionQuery{}, 10, nil))
	expectEqual(t, []durable.SubmissionId{scanned.Items[0].Id, scanned.Items[1].Id}, []durable.SubmissionId{queued, done})
	status := durable.SubmissionDone
	expectEqual(t, len(must(storage.ScanSubmissions(testContext, durable.SubmissionQuery{Status: &status}, 10, nil)).Items), 1)
	byRequest := must(storage.SubmissionByRequest(testContext, durable.ROOT_CONVERSATION_ID, requestId))
	if byRequest == nil || byRequest.Id != queued {
		t.Fatalf("by request = %+v", byRequest)
	}
	{
		absent, err := storage.SubmissionByRequest(testContext, child, requestId)
		expectNil(t, absent, err)
	}

	// findDocument, document and scanDocuments: an exact logical address at a point, a specific incarnation, and the incarnations alive in a scope.
	address := durable.DocumentAddress{Kind: "fam", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, Key: &key}
	record := must(storage.FindDocument(testContext, address, durable.CurrentPoint))
	if record == nil || record.Id != member {
		t.Fatalf("document at the address = %+v", record)
	}
	{
		absent, err := storage.FindDocument(testContext, durable.DocumentAddress{Kind: "fam", Scope: address.Scope}, durable.CurrentPoint)
		expectNil(t, absent, err)
	}
	stored := must(storage.Document(testContext, singleton, durable.CurrentPoint))
	if stored == nil || stored.Value.Value("n") != 1.0 || stored.Version != 1 {
		t.Fatalf("stored document = %+v", stored)
	}
	docs := must(storage.ScanDocuments(testContext, durable.DocumentQuery{Scope: address.Scope, At: durable.CurrentPoint}, 10, nil))
	expectEqual(t, len(docs.Items), 2)
	kind := "fam"
	expectEqual(t, len(must(storage.ScanDocuments(testContext, durable.DocumentQuery{Scope: address.Scope, At: durable.CurrentPoint, Kind: &kind}, 10, nil)).Items), 1)

	// close: every later operation fails.
	mustDo(t, storage.Close(testContext))
	if _, err := storage.Conversation(testContext, child); err == nil {
		t.Fatal("a closed storage must fail")
	}
	if _, err := storage.Commit(testContext, nil); err == nil {
		t.Fatal("a closed storage must refuse commits")
	}
}
