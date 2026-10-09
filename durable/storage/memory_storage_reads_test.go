package storage_test

// pi: packages/durable/src/storage/memory.ts

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi storage/memory.ts:245-631 (commit, conversation, entry, findLatestHeadMarker, scan*, task, submission,
// submissionByRequest, document, mintId, close): each read observes the committed write and returns a detached record,
// and every operation after close fails (memory.ts assertOpen).
// Pi source: packages/durable/src/storage/memory.ts
// mutation-checked: zeroing the results of MemoryStorage.Commit, MemoryStorage.Document, MemoryStorage.FindDocument, MemoryStorage.FindLatestHeadMarker, MemoryStorage.MintId, MemoryStorage.ScanConversations, MemoryStorage.ScanEntries, MemoryStorage.ScanSubmissions, MemoryStorage.ScanTasks, MemoryStorage.SubmissionByRequest fails it
func TestMemoryStorageReadsCommittedRecordsAndFailsAfterClose(t *testing.T) {
	ctx := context.Background()
	memory := storage.NewMemoryStorage()
	root := durable.ROOT_CONVERSATION_ID
	mint := func() int64 {
		t.Helper()
		id, err := memory.MintId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second := mint(), mint()
	if second != first+1 {
		t.Fatalf("MintId must advance by one: %d then %d", first, second)
	}
	entryId := durable.IdFromNumber[durable.EntryId](mint())
	headId := durable.IdFromNumber[durable.EntryId](mint())
	taskId := durable.IdFromNumber[durable.TaskId](mint())
	submissionId := durable.IdFromNumber[durable.SubmissionId](mint())
	documentId := durable.IdFromNumber[durable.DocumentId](mint())
	requestId := "request-1"
	key := "family-key"
	checkpoint := durable.JsonValue(map[string]any{"step": 1})
	task := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
		Id: taskId, ConversationId: root, Kind: "memory.reads", Version: 1, Input: map[string]any{"in": 1},
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint},
	}
	submission := durable.SubmissionRecord{Id: submissionId, ConversationId: root, RequestId: &requestId, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &entryId}
	seq, err := memory.Commit(ctx, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: root}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: entryId, ConversationId: root, Kind: "plain", Data: map[string]any{"n": 1}}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: headId, ConversationId: root, Kind: "marker", Head: &entryId}},
		durable.TaskWrite{Value: task},
		durable.SubmissionWrite{Value: submission},
		durable.DocumentCreateWrite{
			Record:  durable.DocumentCreate{Id: documentId, Kind: "memory.family", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
			Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("v", 1)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	conversation, err := memory.Conversation(ctx, root)
	if err != nil || conversation == nil || conversation.Id != root {
		t.Fatalf("conversation=%v err=%v", conversation, err)
	}
	if missing, err := memory.Conversation(ctx, durable.IdFromNumber[durable.ConversationId](mint())); err != nil || missing != nil {
		t.Fatalf("absent conversation=%v err=%v", missing, err)
	}
	conversations, err := memory.ScanConversations(ctx, durable.ConversationQuery{}, 10, durable.Cursor{})
	if err != nil || len(conversations.Items) != 1 {
		t.Fatalf("conversations=%+v err=%v", conversations, err)
	}

	at, err := memory.Entry(ctx, entryId)
	if err != nil || at == nil || at.CommitSeq != seq || at.Entry.Kind != "plain" {
		t.Fatalf("entry=%+v err=%v", at, err)
	}
	marker, err := memory.FindLatestHeadMarker(ctx, root, nil)
	if err != nil || marker == nil || marker.Id != headId {
		t.Fatalf("marker=%v err=%v", marker, err)
	}
	before := durable.IdFromNumber[durable.EntryId](int64(headId) - 1)
	if none, err := memory.FindLatestHeadMarker(ctx, root, &before); err != nil || none != nil {
		t.Fatalf("a cutoff below the marker must hide it: %v err=%v", none, err)
	}
	entries, err := memory.ScanEntries(ctx, durable.EntryQuery{ConversationId: root}, 10, durable.Cursor{})
	if err != nil || len(entries.Items) != 2 || entries.Items[0].Id != headId {
		t.Fatalf("entries are newest-first: %+v err=%v", entries, err)
	}

	foundTask, err := memory.Task(ctx, taskId)
	if err != nil || foundTask == nil || !reflect.DeepEqual(*foundTask, task) {
		t.Fatalf("task=%+v err=%v", foundTask, err)
	}
	pending := durable.TaskPending
	tasks, err := memory.ScanTasks(ctx, durable.TaskQuery{Status: &pending}, 10, durable.Cursor{})
	if err != nil || len(tasks.Items) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}

	foundSubmission, err := memory.Submission(ctx, submissionId)
	if err != nil || foundSubmission == nil || !reflect.DeepEqual(*foundSubmission, submission) {
		t.Fatalf("submission=%+v err=%v", foundSubmission, err)
	}
	byRequest, err := memory.SubmissionByRequest(ctx, root, requestId)
	if err != nil || byRequest == nil || byRequest.Id != submissionId {
		t.Fatalf("byRequest=%+v err=%v", byRequest, err)
	}
	if none, err := memory.SubmissionByRequest(ctx, root, "other"); err != nil || none != nil {
		t.Fatalf("unknown request=%v err=%v", none, err)
	}
	done := durable.SubmissionDone
	submissions, err := memory.ScanSubmissions(ctx, durable.SubmissionQuery{Status: &done}, 10, durable.Cursor{})
	if err != nil || len(submissions.Items) != 1 {
		t.Fatalf("submissions=%+v err=%v", submissions, err)
	}

	document, err := memory.Document(ctx, documentId, durable.CurrentPoint)
	if err != nil || document == nil {
		t.Fatalf("document=%v err=%v", document, err)
	}
	address := durable.DocumentAddress{Kind: "memory.family", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, Key: &key}
	if record, err := memory.FindDocument(ctx, address, durable.CurrentPoint); err != nil || record == nil || record.Id != documentId {
		t.Fatalf("document address=%v err=%v", record, err)
	}
	documents, err := memory.ScanDocuments(ctx, durable.DocumentQuery{Scope: address.Scope, At: durable.CurrentPoint}, 10, durable.Cursor{})
	if err != nil || len(documents.Items) != 1 {
		t.Fatalf("documents=%+v err=%v", documents, err)
	}

	if err := memory.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.MintId(); err == nil {
		t.Error("MintId after Close must fail")
	}
	if _, err := memory.Task(ctx, taskId); err == nil {
		t.Error("Task after Close must fail")
	}
	if _, err := memory.Entry(ctx, entryId); err == nil {
		t.Error("Entry after Close must fail")
	}
	if _, err := memory.Commit(ctx, nil); err == nil {
		t.Error("Commit after Close must fail")
	}
}

// Pi storage/memory.ts:449-471: the two-argument overload entry(conversationId, id) returns an entry only when it is visible through that conversation's
// ancestry: its own entries, and a parent's entries up to the fork point; an entry past the fork point, another branch's entry and an
// unknown id are absent.
func TestMemoryStorageVisibleEntryFollowsConversationAncestry(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	mint := func() int64 {
		id, err := store.MintId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	root := durable.ROOT_CONVERSATION_ID
	child := durable.IdFromNumber[durable.ConversationId](mint())
	sibling := durable.IdFromNumber[durable.ConversationId](mint())
	forked := durable.IdFromNumber[durable.EntryId](mint())
	afterFork := durable.IdFromNumber[durable.EntryId](mint())
	childOwn := durable.IdFromNumber[durable.EntryId](mint())
	siblingOwn := durable.IdFromNumber[durable.EntryId](mint())
	entry := func(conversation durable.ConversationId, id durable.EntryId) durable.StorageWrite {
		return durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: conversation, Kind: "plain", Data: map[string]any{"n": int64(id)}}}
	}
	if _, err := store.Commit(ctx, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: root}},
		entry(root, forked),
		entry(root, afterFork),
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: child, Parent: &durable.ConversationParent{ConversationId: root, At: forked}}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: sibling, Parent: &durable.ConversationParent{ConversationId: root, At: afterFork}}},
		entry(child, childOwn),
		entry(sibling, siblingOwn),
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		conversation durable.ConversationId
		id           durable.EntryId
		visible      bool
	}{
		{"own entry", child, childOwn, true},
		{"parent entry at the fork point", child, forked, true},
		{"parent entry past the fork point", child, afterFork, false},
		{"another branch's entry", child, siblingOwn, false},
		{"root cannot see a child's entry", root, childOwn, false},
		{"sibling sees the parent entries up to its fork", sibling, afterFork, true},
		{"unknown entry", child, durable.IdFromNumber[durable.EntryId](mint()), false},
	} {
		got, err := store.VisibleEntry(ctx, tc.conversation, tc.id)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.visible != (got != nil) || got != nil && got.Entry.Id != tc.id {
			t.Errorf("%s: VisibleEntry(%d, %d) = %+v, visible want %v", tc.name, tc.conversation, tc.id, got, tc.visible)
		}
	}
}
