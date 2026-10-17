package storage_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

var raCtx = context.Background()

type raTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

func raMust[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func raMustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func raExpectEqual(t *testing.T, actual, expected any) {
	t.Helper()
	if !reflect.DeepEqual(durabletest.Normalize(actual), durabletest.Normalize(expected)) {
		t.Fatalf("got %s, want %s", durabletest.Describe(actual), durabletest.Describe(expected))
	}
}

func raExpectNil[T any](t *testing.T, value *T, err error) {
	t.Helper()
	raMustDo(t, err)
	if value != nil {
		t.Fatalf("got %s, want undefined", durabletest.Describe(value))
	}
}

func raCommit(t *testing.T, storage durable.Storage, writes ...durable.StorageWrite) durable.Seq {
	t.Helper()
	return raMust(storage.Commit(raCtx, writes))
}

func raCreateRoot(t *testing.T, storage durable.Storage) {
	t.Helper()
	raCommit(t, storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}})
}

func raMint[I durable.Id](t *testing.T, storage durable.Storage) I {
	t.Helper()
	return durable.IdFromNumber[I](raMust(storage.MintId()))
}

func raPending(id durable.TaskId, phase string) raTask {
	var checkpoint durable.JsonValue = map[string]any{"phase": phase}
	return raTask{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test.task", Version: 1,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint}}
}

func raTerminal(id durable.TaskId) raTask {
	var result durable.JsonValue
	return raTask{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test.task", Version: 1,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}}
}

func raSessionDoc(id durable.DocumentId, kind string) durable.DocumentCreate {
	return durable.DocumentCreate{Id: id, Kind: kind, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}
}

func raBase(value durable.JsonObject) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: value}
}

// The read API of MemoryStorage, asked of one seeded store: packages/durable/src/storage/memory.ts:245-631 (commit, conversation, scanConversations, entry, findLatestHeadMarker, scanEntries, task, scanTasks, submission, scanSubmissions, submissionByRequest, findDocument, document, scanDocuments, close) is the reference implementation of the storage contract, so each result below is the contract's: lookups return the latest complete record or undefined, scans are ordered and paged by a backend cursor, and every call after close fails.
func TestMemoryStorageReadApiMirrorsPisStorageContract(t *testing.T) {
	storage := storage.NewMemoryStorage()
	raCreateRoot(t, storage)
	taskId := raMint[durable.TaskId](t, storage)
	other := raMint[durable.TaskId](t, storage)
	child := raMint[durable.ConversationId](t, storage)
	e1, e2, e3 := raMint[durable.EntryId](t, storage), raMint[durable.EntryId](t, storage), raMint[durable.EntryId](t, storage)
	childEntry := raMint[durable.EntryId](t, storage)
	queued, done := raMint[durable.SubmissionId](t, storage), raMint[durable.SubmissionId](t, storage)
	singleton, member := raMint[durable.DocumentId](t, storage), raMint[durable.DocumentId](t, storage)
	if singleton == member || e1 >= e2 || e2 >= e3 {
		t.Fatalf("MintId must hand out fresh increasing numbers: %v %v %v %v", e1, e2, e3, singleton)
	}
	requestId := "req-1"
	key := "k1"
	headOfTwo := e1
	seq := raCommit(t, storage,
		durable.TaskWrite{Value: raPending(taskId, "ready")},
		durable.TaskWrite{Value: raTerminal(other)},
		durable.EntryWrite{Value: durable.EntryRecord{Id: e1, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "a"}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: e2, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "b", Head: &headOfTwo}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: e3, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "c"}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: child, Parent: &durable.ConversationParent{ConversationId: durable.ROOT_CONVERSATION_ID, At: e2}, Owner: &durable.ConversationOwner{ConversationId: durable.ROOT_CONVERSATION_ID, TaskId: taskId}}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: childEntry, ConversationId: child, Kind: "d"}},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: queued, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &requestId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: done, ConversationId: child, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &childEntry}},
		durable.DocumentCreateWrite{Record: raSessionDoc(singleton, "note"), Content: raBase(delta.JsonObjectOf("n", 1.0))},
		durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: member, Kind: "fam", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, Content: raBase(delta.JsonObjectOf("m", 2.0))},
	)

	// conversation: exact lookup, undefined when absent.
	raExpectEqual(t, raMust(storage.Conversation(raCtx, child)).Owner, &durable.ConversationOwner{ConversationId: durable.ROOT_CONVERSATION_ID, TaskId: taskId})
	{
		absent, err := storage.Conversation(raCtx, child+1000)
		raExpectNil(t, absent, err)
	}

	// scanConversations: ascending ID order, optional owner filter, a limit pages with a backend cursor that finishes with no Next.
	all := raMust(storage.ScanConversations(raCtx, durable.ConversationQuery{}, 10, nil))
	raExpectEqual(t, []durable.ConversationId{all.Items[0].Id, all.Items[1].Id}, []durable.ConversationId{durable.ROOT_CONVERSATION_ID, child})
	if all.Next != nil {
		t.Fatalf("a scan that returned everything has no cursor: %v", *all.Next)
	}
	owned := raMust(storage.ScanConversations(raCtx, durable.ConversationQuery{OwnerTaskId: &taskId}, 10, nil))
	raExpectEqual(t, len(owned.Items), 1)
	first := raMust(storage.ScanConversations(raCtx, durable.ConversationQuery{}, 1, nil))
	if len(first.Items) != 1 || first.Next == nil {
		t.Fatalf("limit 1 must leave a cursor: %+v", first)
	}
	second := raMust(storage.ScanConversations(raCtx, durable.ConversationQuery{}, 1, *first.Next))
	if len(second.Items) != 1 || second.Items[0].Id != child {
		t.Fatalf("the cursor continues after the first conversation: %+v", second)
	}

	// entry: the global entry and the sequence of its commit; undefined when absent. Visible entry honors ancestry.
	found := raMust(storage.Entry(raCtx, e2))
	if found == nil || found.Entry.Kind != "b" || found.CommitSeq != seq {
		t.Fatalf("entry = %+v, want kind b at commit %d", found, seq)
	}
	{
		absent, err := storage.Entry(raCtx, childEntry+1000)
		raExpectNil(t, absent, err)
	}
	visible := raMust(storage.VisibleEntry(raCtx, child, e1))
	if visible == nil || visible.Entry.Id != e1 {
		t.Fatalf("a child sees the entries of the history it forked from: %+v", visible)
	}
	if hidden := raMust(storage.VisibleEntry(raCtx, child, e3)); hidden != nil {
		t.Fatalf("an entry after the fork point is not visible: %+v", hidden)
	}

	// findLatestHeadMarker: the newest visible entry with a head at or below the cutoff.
	marker := raMust(storage.FindLatestHeadMarker(raCtx, durable.ROOT_CONVERSATION_ID, nil))
	if marker == nil || marker.Id != e2 {
		t.Fatalf("latest head marker = %+v, want %d", marker, e2)
	}
	{
		absent, err := storage.FindLatestHeadMarker(raCtx, durable.ROOT_CONVERSATION_ID, &e1)
		raExpectNil(t, absent, err)
	}

	// scanEntries: newest first within the inclusive bounds, at most limit entries.
	page := raMust(storage.ScanEntries(raCtx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 2, nil))
	raExpectEqual(t, []durable.EntryId{page.Items[0].Id, page.Items[1].Id}, []durable.EntryId{e3, e2})
	if page.Next == nil {
		t.Fatal("two of three entries leave a cursor")
	}
	rest := raMust(storage.ScanEntries(raCtx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 2, *page.Next))
	raExpectEqual(t, len(rest.Items), 1)
	bounded := raMust(storage.ScanEntries(raCtx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, MinEntryId: &e2, MaxEntryId: &e2}, 5, nil))
	raExpectEqual(t, len(bounded.Items), 1)

	// task and scanTasks: the latest complete record; scans match every supplied filter.
	raExpectEqual(t, raMust(storage.Task(raCtx, taskId)).Id, taskId)
	{
		absent, err := storage.Task(raCtx, other+1000)
		raExpectNil(t, absent, err)
	}
	pending := durable.TaskPending
	filtered := raMust(storage.ScanTasks(raCtx, durable.TaskQuery{Status: &pending}, 10, nil))
	raExpectEqual(t, len(filtered.Items), 1)
	raExpectEqual(t, filtered.Items[0].Id, taskId)
	raExpectEqual(t, len(raMust(storage.ScanTasks(raCtx, durable.TaskQuery{}, 10, nil)).Items), 2)

	// submission, scanSubmissions and submissionByRequest: ascending IDs, filters, conversation-scoped request keys.
	raExpectEqual(t, raMust(storage.Submission(raCtx, queued)).Status, durable.SubmissionQueued)
	{
		absent, err := storage.Submission(raCtx, done+1000)
		raExpectNil(t, absent, err)
	}
	scanned := raMust(storage.ScanSubmissions(raCtx, durable.SubmissionQuery{}, 10, nil))
	raExpectEqual(t, []durable.SubmissionId{scanned.Items[0].Id, scanned.Items[1].Id}, []durable.SubmissionId{queued, done})
	status := durable.SubmissionDone
	raExpectEqual(t, len(raMust(storage.ScanSubmissions(raCtx, durable.SubmissionQuery{Status: &status}, 10, nil)).Items), 1)
	byRequest := raMust(storage.SubmissionByRequest(raCtx, durable.ROOT_CONVERSATION_ID, requestId))
	if byRequest == nil || byRequest.Id != queued {
		t.Fatalf("by request = %+v", byRequest)
	}
	{
		absent, err := storage.SubmissionByRequest(raCtx, child, requestId)
		raExpectNil(t, absent, err)
	}

	// findDocument, document and scanDocuments: an exact logical address at a point, a specific incarnation, and the incarnations alive in a scope.
	address := durable.DocumentAddress{Kind: "fam", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, Key: &key}
	record := raMust(storage.FindDocument(raCtx, address, durable.CurrentPoint))
	if record == nil || record.Id != member {
		t.Fatalf("document at the address = %+v", record)
	}
	{
		absent, err := storage.FindDocument(raCtx, durable.DocumentAddress{Kind: "fam", Scope: address.Scope}, durable.CurrentPoint)
		raExpectNil(t, absent, err)
	}
	stored := raMust(storage.Document(raCtx, singleton, durable.CurrentPoint))
	if stored == nil || stored.Value.Value("n") != 1.0 || stored.Version != 1 {
		t.Fatalf("stored document = %+v", stored)
	}
	docs := raMust(storage.ScanDocuments(raCtx, durable.DocumentQuery{Scope: address.Scope, At: durable.CurrentPoint}, 10, nil))
	raExpectEqual(t, len(docs.Items), 2)
	kind := "fam"
	raExpectEqual(t, len(raMust(storage.ScanDocuments(raCtx, durable.DocumentQuery{Scope: address.Scope, At: durable.CurrentPoint, Kind: &kind}, 10, nil)).Items), 1)

	// close: every later operation fails.
	raMustDo(t, storage.Close(raCtx))
	if _, err := storage.Conversation(raCtx, child); err == nil {
		t.Fatal("a closed storage must fail")
	}
	if _, err := storage.Commit(raCtx, nil); err == nil {
		t.Fatal("a closed storage must refuse commits")
	}
}

// packages/durable/src/types.ts:993-1084 declares the Storage interface every backend implements: through the interface, a seeded store answers every lookup and scan with the contract's records, and a closed store fails (the same seeded data as the concrete test above, read through durable.Storage).
func TestStorageInterfaceReadContractThroughTheInterface(t *testing.T) {
	memory := storage.NewMemoryStorage()
	var contract durable.Storage = memory
	raCreateRoot(t, contract)
	taskId, e1, sub := raMint[durable.TaskId](t, contract), raMint[durable.EntryId](t, contract), raMint[durable.SubmissionId](t, contract)
	doc := raMint[durable.DocumentId](t, contract)
	requestId := "r"
	seq := raCommit(t, contract,
		durable.TaskWrite{Value: raPending(taskId, "ready")},
		durable.EntryWrite{Value: durable.EntryRecord{Id: e1, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "a", Head: &e1}},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: sub, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &requestId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}},
		durable.DocumentCreateWrite{Record: raSessionDoc(doc, "note"), Content: raBase(delta.JsonObjectOf("n", 1.0))},
	)
	if got := raMust(contract.Conversation(raCtx, durable.ROOT_CONVERSATION_ID)); got == nil || got.Id != durable.ROOT_CONVERSATION_ID {
		t.Fatalf("conversation = %+v", got)
	}
	if page := raMust(contract.ScanConversations(raCtx, durable.ConversationQuery{}, 5, nil)); len(page.Items) != 1 || page.Next != nil {
		t.Fatalf("scanConversations = %+v", page)
	}
	if got := raMust(contract.Entry(raCtx, e1)); got == nil || got.CommitSeq != seq {
		t.Fatalf("entry = %+v", got)
	}
	if got := raMust(contract.FindLatestHeadMarker(raCtx, durable.ROOT_CONVERSATION_ID, nil)); got == nil || got.Id != e1 {
		t.Fatalf("latest head marker = %+v", got)
	}
	if page := raMust(contract.ScanEntries(raCtx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 5, nil)); len(page.Items) != 1 {
		t.Fatalf("scanEntries = %+v", page)
	}
	if got := raMust(contract.Task(raCtx, taskId)); got == nil || got.Id != taskId {
		t.Fatalf("task = %+v", got)
	}
	if page := raMust(contract.ScanTasks(raCtx, durable.TaskQuery{}, 5, nil)); len(page.Items) != 1 {
		t.Fatalf("scanTasks = %+v", page)
	}
	if got := raMust(contract.Submission(raCtx, sub)); got == nil || got.Status != durable.SubmissionQueued {
		t.Fatalf("submission = %+v", got)
	}
	if page := raMust(contract.ScanSubmissions(raCtx, durable.SubmissionQuery{}, 5, nil)); len(page.Items) != 1 {
		t.Fatalf("scanSubmissions = %+v", page)
	}
	if got := raMust(contract.SubmissionByRequest(raCtx, durable.ROOT_CONVERSATION_ID, requestId)); got == nil || got.Id != sub {
		t.Fatalf("submissionByRequest = %+v", got)
	}
	address := durable.DocumentAddress{Kind: "note", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}
	if got := raMust(contract.FindDocument(raCtx, address, durable.CurrentPoint)); got == nil || got.Id != doc {
		t.Fatalf("findDocument = %+v", got)
	}
	if got := raMust(contract.Document(raCtx, doc, durable.CurrentPoint)); got == nil || got.Value.Value("n") != 1.0 {
		t.Fatalf("document = %+v", got)
	}
	if page := raMust(contract.ScanDocuments(raCtx, durable.DocumentQuery{Scope: address.Scope, At: durable.CurrentPoint}, 5, nil)); len(page.Items) != 1 {
		t.Fatalf("scanDocuments = %+v", page)
	}
	raMustDo(t, contract.Close(raCtx))
	if _, err := contract.Conversation(raCtx, durable.ROOT_CONVERSATION_ID); err == nil {
		t.Fatal("a closed storage must fail")
	}
}
