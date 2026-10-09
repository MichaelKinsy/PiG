package storage_test

// Ports packages/durable/test/memory-storage.test.ts (reads), jsonl-storage.test.ts and sqlite-storage.test.ts: every backend
// answers the Storage read contract with the same records for the same committed writes.

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// readFixtureIds are the literal IDs the fixture commits, so every backend stores identical records.
var readFixtureIds = struct {
	entries    []durable.EntryId
	child      durable.ConversationId
	task       durable.TaskId
	submission durable.SubmissionId
	document   durable.DocumentId
}{entries: []durable.EntryId{101, 102, 103}, child: 104, task: 105, submission: 106, document: 107}

func seedReadFixture(t *testing.T, target durable.Storage) {
	t.Helper()
	ids := readFixtureIds
	request := "read-request"
	head := ids.entries[0]
	writes := []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: ids.entries[0], ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "read.entry"}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: ids.entries[1], ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "read.entry"}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: ids.entries[2], ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "read.head", Head: &head}},
		durable.TaskWrite{Value: durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{Id: ids.task, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "read.kind", Version: 1, State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending}}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: ids.child, Parent: &durable.ConversationParent{ConversationId: durable.ROOT_CONVERSATION_ID, At: ids.entries[0]}, Owner: &durable.ConversationOwner{ConversationId: durable.ROOT_CONVERSATION_ID, TaskId: ids.task}}},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: ids.submission, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &request, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &ids.entries[1]}},
		durable.DocumentCreateWrite{
			Record:  durable.DocumentCreate{Id: ids.document, Kind: "read.family", Key: new("read-key"), Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
			Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("count", 1)},
		},
	}
	if _, err := target.Commit(context.Background(), writes); err != nil {
		t.Fatal(err)
	}
}

// Every read method of each concrete backend returns what the memory backend returns for the same committed writes, and the
// records are the committed ones. MintId hands out strictly increasing candidates.
// Pi source: packages/durable/src/types.ts (Storage), packages/durable/src/storage/jsonl.ts, packages/durable/src/storage/sqlite.ts
// mutation-checked: a constant MintId, a wrong ScanTasks limit, a changed SubmissionByRequest key or predicate, a filtered ScanSubmissions each fail it
// Pi source: packages/durable/src/storage/jsonl/storage.ts, packages/durable/src/storage/sqlite/storage.ts
// mutation-checked: zeroing the results of JsonlStorage.Entry, JsonlStorage.FindLatestHeadMarker, JsonlStorage.MintId, JsonlStorage.ScanConversations, JsonlStorage.ScanDocuments, JsonlStorage.ScanEntries, JsonlStorage.ScanSubmissions, JsonlStorage.ScanTasks, JsonlStorage.Submission, JsonlStorage.SubmissionByRequest, SqliteStorage.FindLatestHeadMarker, SqliteStorage.MintId fails it
// Pi: packages/durable/src/storage/jsonl/storage.ts:333 (findLatestHeadMarker)
// Pi: packages/durable/src/storage/jsonl/storage.ts:308 (mintId)
// Pi: packages/durable/src/storage/jsonl/storage.ts:316 (scanConversations)
// Pi: packages/durable/src/storage/jsonl/storage.ts:373 (scanDocuments)
// Pi: packages/durable/src/storage/jsonl/storage.ts:341 (scanEntries)
// Pi: packages/durable/src/storage/jsonl/storage.ts:357 (scanSubmissions)
// Pi: packages/durable/src/storage/jsonl/storage.ts:349 (scanTasks)
// Pi: packages/durable/src/storage/jsonl/storage.ts:36 (submission)
// Pi: packages/durable/src/storage/jsonl/storage.ts:361 (submissionByRequest)
func TestBackendReadsAgreeWithMemory(t *testing.T) {
	ctx := context.Background()
	memory := storage.NewMemoryStorage()
	t.Cleanup(func() { _ = memory.Close(ctx) })
	seedReadFixture(t, memory)
	want, err := snapshotMemory(ctx, memory)
	if err != nil {
		t.Fatal(err)
	}
	if entry := want["entry"].(*durable.EntryAt); entry == nil || entry.Entry.Id != readFixtureIds.entries[2] || entry.Entry.Kind != "read.head" {
		t.Fatalf("memory fixture entry = %+v", want["entry"])
	}
	if marker := want["marker"].(*durable.EntryRecord); marker == nil || marker.Id != readFixtureIds.entries[2] {
		t.Fatalf("memory fixture marker = %+v", want["marker"])
	}
	for key, count := range map[string]int{
		"conversations": len(want["conversations"].(durable.Page[durable.ConversationRecord, durable.Cursor]).Items),
		"entries":       len(want["entries"].(durable.Page[durable.EntryRecord, durable.Cursor]).Items),
		"submissions":   len(want["submissions"].(durable.Page[durable.SubmissionRecord, durable.Cursor]).Items),
		"documents":     len(want["documents"].(durable.Page[durable.DocumentRecord, durable.Cursor]).Items),
	} {
		wantCount := map[string]int{"conversations": 1, "entries": 2, "submissions": 1, "documents": 1}[key]
		if count != wantCount {
			t.Fatalf("memory fixture %s page has %d items, want %d", key, count, wantCount)
		}
	}
	if want["byRequest"].(*durable.SubmissionRecord) == nil || want["document"].(*durable.DocumentRecord) == nil || want["submission"].(*durable.SubmissionRecord) == nil || want["conversation"].(*durable.ConversationRecord) == nil {
		t.Fatal("memory fixture lookups found nothing")
	}
	jsonlStorage := createFixture(t, "jsonl").(*jsonl.JsonlStorage)
	sqliteStorage := createFixture(t, "sqlite").(*sqlite.SqliteStorage)
	seedReadFixture(t, jsonlStorage)
	seedReadFixture(t, sqliteStorage)
	generic := createFixture(t, "memory")
	seedReadFixture(t, generic)
	for name, snapshot := range map[string]func() (map[string]any, error){
		"jsonl":   func() (map[string]any, error) { return snapshotJsonl(ctx, jsonlStorage) },
		"sqlite":  func() (map[string]any, error) { return snapshotSqlite(ctx, sqliteStorage) },
		"Storage": func() (map[string]any, error) { return snapshotStorage(ctx, generic) },
	} {
		got, err := snapshot()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for key, expected := range want {
			if !reflect.DeepEqual(durabletest.Normalize(got[key]), durabletest.Normalize(expected)) {
				t.Errorf("%s %s = %s, want %s", name, key, durabletest.Describe(got[key]), durabletest.Describe(expected))
			}
		}
	}
	for name, mint := range map[string]func() (int64, error){"jsonl": jsonlStorage.MintId, "sqlite": sqliteStorage.MintId} {
		first, err := mint()
		if err != nil {
			t.Fatalf("%s MintId: %v", name, err)
		}
		second, err := mint()
		if err != nil || second <= first {
			t.Errorf("%s MintId = %d then %d (%v), want increasing candidates", name, first, second, err)
		}
	}
}

func snapshotMemory(ctx context.Context, s *storage.MemoryStorage) (map[string]any, error) {
	out := map[string]any{}
	var err error
	step := func(name string, value any, stepErr error) {
		if err == nil && stepErr != nil {
			err = fmt.Errorf("%s: %w", name, stepErr)
		}
		out[name] = value
	}
	first, second := readFixtureIds.entries[0], readFixtureIds.entries[2]
	conversation, e := s.Conversation(ctx, readFixtureIds.child)
	step("conversation", conversation, e)
	conversations, e := s.ScanConversations(ctx, durable.ConversationQuery{OwnerConversationId: new(durable.ROOT_CONVERSATION_ID)}, 10, nil)
	step("conversations", conversations, e)
	entry, e := s.Entry(ctx, second)
	step("entry", entry, e)
	marker, e := s.FindLatestHeadMarker(ctx, durable.ROOT_CONVERSATION_ID, nil)
	step("marker", marker, e)
	markerBefore, e := s.FindLatestHeadMarker(ctx, durable.ROOT_CONVERSATION_ID, &first)
	step("markerBefore", markerBefore, e)
	entries, e := s.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, MinEntryId: &first}, 2, nil)
	step("entries", entries, e)
	tasks, e := s.ScanTasks(ctx, durable.TaskQuery{Kind: new("read.kind")}, 10, nil)
	step("tasks", tasks, e)
	submission, e := s.Submission(ctx, readFixtureIds.submission)
	step("submission", submission, e)
	submissions, e := s.ScanSubmissions(ctx, durable.SubmissionQuery{Status: new(durable.SubmissionDone)}, 10, nil)
	step("submissions", submissions, e)
	byRequest, e := s.SubmissionByRequest(ctx, durable.ROOT_CONVERSATION_ID, "read-request")
	step("byRequest", byRequest, e)
	document, e := s.FindDocument(ctx, durable.DocumentAddress{Kind: "read.family", Key: new("read-key"), Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, durable.CurrentPoint)
	step("document", document, e)
	documents, e := s.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}, 10, nil)
	step("documents", documents, e)
	return out, err
}

func snapshotJsonl(ctx context.Context, s *jsonl.JsonlStorage) (map[string]any, error) {
	out := map[string]any{}
	var err error
	step := func(name string, value any, stepErr error) {
		if err == nil && stepErr != nil {
			err = fmt.Errorf("%s: %w", name, stepErr)
		}
		out[name] = value
	}
	first, second := readFixtureIds.entries[0], readFixtureIds.entries[2]
	conversation, e := s.Conversation(ctx, readFixtureIds.child)
	step("conversation", conversation, e)
	conversations, e := s.ScanConversations(ctx, durable.ConversationQuery{OwnerConversationId: new(durable.ROOT_CONVERSATION_ID)}, 10, nil)
	step("conversations", conversations, e)
	entry, e := s.Entry(ctx, second)
	step("entry", entry, e)
	marker, e := s.FindLatestHeadMarker(ctx, durable.ROOT_CONVERSATION_ID, nil)
	step("marker", marker, e)
	markerBefore, e := s.FindLatestHeadMarker(ctx, durable.ROOT_CONVERSATION_ID, &first)
	step("markerBefore", markerBefore, e)
	entries, e := s.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, MinEntryId: &first}, 2, nil)
	step("entries", entries, e)
	tasks, e := s.ScanTasks(ctx, durable.TaskQuery{Kind: new("read.kind")}, 10, nil)
	step("tasks", tasks, e)
	submission, e := s.Submission(ctx, readFixtureIds.submission)
	step("submission", submission, e)
	submissions, e := s.ScanSubmissions(ctx, durable.SubmissionQuery{Status: new(durable.SubmissionDone)}, 10, nil)
	step("submissions", submissions, e)
	byRequest, e := s.SubmissionByRequest(ctx, durable.ROOT_CONVERSATION_ID, "read-request")
	step("byRequest", byRequest, e)
	document, e := s.FindDocument(ctx, durable.DocumentAddress{Kind: "read.family", Key: new("read-key"), Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, durable.CurrentPoint)
	step("document", document, e)
	documents, e := s.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}, 10, nil)
	step("documents", documents, e)
	return out, err
}

func snapshotSqlite(ctx context.Context, s *sqlite.SqliteStorage) (map[string]any, error) {
	out := map[string]any{}
	var err error
	step := func(name string, value any, stepErr error) {
		if err == nil && stepErr != nil {
			err = fmt.Errorf("%s: %w", name, stepErr)
		}
		out[name] = value
	}
	first, second := readFixtureIds.entries[0], readFixtureIds.entries[2]
	conversation, e := s.Conversation(ctx, readFixtureIds.child)
	step("conversation", conversation, e)
	conversations, e := s.ScanConversations(ctx, durable.ConversationQuery{OwnerConversationId: new(durable.ROOT_CONVERSATION_ID)}, 10, nil)
	step("conversations", conversations, e)
	entry, e := s.Entry(ctx, second)
	step("entry", entry, e)
	marker, e := s.FindLatestHeadMarker(ctx, durable.ROOT_CONVERSATION_ID, nil)
	step("marker", marker, e)
	markerBefore, e := s.FindLatestHeadMarker(ctx, durable.ROOT_CONVERSATION_ID, &first)
	step("markerBefore", markerBefore, e)
	entries, e := s.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, MinEntryId: &first}, 2, nil)
	step("entries", entries, e)
	tasks, e := s.ScanTasks(ctx, durable.TaskQuery{Kind: new("read.kind")}, 10, nil)
	step("tasks", tasks, e)
	submission, e := s.Submission(ctx, readFixtureIds.submission)
	step("submission", submission, e)
	submissions, e := s.ScanSubmissions(ctx, durable.SubmissionQuery{Status: new(durable.SubmissionDone)}, 10, nil)
	step("submissions", submissions, e)
	byRequest, e := s.SubmissionByRequest(ctx, durable.ROOT_CONVERSATION_ID, "read-request")
	step("byRequest", byRequest, e)
	document, e := s.FindDocument(ctx, durable.DocumentAddress{Kind: "read.family", Key: new("read-key"), Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, durable.CurrentPoint)
	step("document", document, e)
	documents, e := s.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}, 10, nil)
	step("documents", documents, e)
	return out, err
}

func snapshotStorage(ctx context.Context, s durable.Storage) (map[string]any, error) {
	out := map[string]any{}
	var err error
	step := func(name string, value any, stepErr error) {
		if err == nil && stepErr != nil {
			err = fmt.Errorf("%s: %w", name, stepErr)
		}
		out[name] = value
	}
	first, second := readFixtureIds.entries[0], readFixtureIds.entries[2]
	conversation, e := s.Conversation(ctx, readFixtureIds.child)
	step("conversation", conversation, e)
	conversations, e := s.ScanConversations(ctx, durable.ConversationQuery{OwnerConversationId: new(durable.ROOT_CONVERSATION_ID)}, 10, nil)
	step("conversations", conversations, e)
	entry, e := s.Entry(ctx, second)
	step("entry", entry, e)
	marker, e := s.FindLatestHeadMarker(ctx, durable.ROOT_CONVERSATION_ID, nil)
	step("marker", marker, e)
	markerBefore, e := s.FindLatestHeadMarker(ctx, durable.ROOT_CONVERSATION_ID, &first)
	step("markerBefore", markerBefore, e)
	entries, e := s.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, MinEntryId: &first}, 2, nil)
	step("entries", entries, e)
	tasks, e := s.ScanTasks(ctx, durable.TaskQuery{Kind: new("read.kind")}, 10, nil)
	step("tasks", tasks, e)
	submission, e := s.Submission(ctx, readFixtureIds.submission)
	step("submission", submission, e)
	submissions, e := s.ScanSubmissions(ctx, durable.SubmissionQuery{Status: new(durable.SubmissionDone)}, 10, nil)
	step("submissions", submissions, e)
	byRequest, e := s.SubmissionByRequest(ctx, durable.ROOT_CONVERSATION_ID, "read-request")
	step("byRequest", byRequest, e)
	document, e := s.FindDocument(ctx, durable.DocumentAddress{Kind: "read.family", Key: new("read-key"), Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, durable.CurrentPoint)
	step("document", document, e)
	documents, e := s.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}, 10, nil)
	step("documents", documents, e)
	return out, err
}
