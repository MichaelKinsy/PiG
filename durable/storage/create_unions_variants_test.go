package storage_test

// pi: packages/durable/src/types.ts SubmissionCreate, SettledSubmissionRecord and DocumentCreate are unions of record shapes: one member per (type, status) pair of a
// submission and one per scope/history/fork combination of a document. These tests commit every member through the memory storage and read it back, so a field a
// member carries survives storage and a field it must not carry does not appear.

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func storedSubmission(t *testing.T, create durable.SubmissionCreate) (durable.SubmissionRecord, *durable.SubmissionRecord) {
	t.Helper()
	ctx := context.Background()
	memory := storage.NewMemoryStorage()
	id, err := memory.MintId()
	if err != nil {
		t.Fatal(err)
	}
	record := create.Record(durable.IdFromNumber[durable.SubmissionId](id))
	if _, err := memory.Commit(ctx, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: create.ConversationId}},
		durable.SubmissionWrite{Value: record},
	}, func(durable.Seq) {}); err != nil {
		t.Fatal(err)
	}
	stored, err := memory.Submission(ctx, record.Id)
	if err != nil {
		t.Fatal(err)
	}
	return record, stored
}

// SubmissionCreate: input queued | placed | done | unanswered, write queued | done | unanswered (seven members).
// mutation-checked: dropping Answer, Reason or Detail from SubmissionCreate.Record fails it.
func TestSubmissionCreateUnionEveryVariantIsStored(t *testing.T) {
	root := durable.ROOT_CONVERSATION_ID
	entry := durable.IdFromNumber[durable.EntryId](11)
	answer := durable.IdFromNumber[durable.EntryId](12)
	reason := "gave up"
	request := "req-1"
	for _, tc := range []struct {
		name   string
		create durable.SubmissionCreate
	}{
		{"input queued", durable.SubmissionCreate{Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}},
		{"input placed", durable.SubmissionCreate{Type: durable.SubmissionTypeInput, Status: durable.SubmissionPlaced, Entry: &entry}},
		{"input done", durable.SubmissionCreate{Type: durable.SubmissionTypeInput, Status: durable.SubmissionDone, Entry: &entry, Answer: &answer}},
		{"input unanswered without an entry", durable.SubmissionCreate{Type: durable.SubmissionTypeInput, Status: durable.SubmissionUnanswered, Reason: &reason, Detail: map[string]any{"why": "aborted"}}},
		{"input unanswered with an entry", durable.SubmissionCreate{Type: durable.SubmissionTypeInput, Status: durable.SubmissionUnanswered, Entry: &entry, Reason: &reason}},
		{"write queued", durable.SubmissionCreate{Type: durable.SubmissionTypeWrite, Status: durable.SubmissionQueued}},
		{"write done", durable.SubmissionCreate{Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &entry}},
		{"write unanswered", durable.SubmissionCreate{Type: durable.SubmissionTypeWrite, Status: durable.SubmissionUnanswered, Reason: &reason, Detail: []any{"a", 1.0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			create := tc.create
			create.ConversationId, create.RequestId = root, &request
			record, stored := storedSubmission(t, create)
			if stored == nil || !reflect.DeepEqual(*stored, record) {
				t.Fatalf("stored = %+v, want %+v", stored, record)
			}
			if stored.Type != create.Type || stored.Status != create.Status || (stored.Answer == nil) != (create.Answer == nil) ||
				(stored.Reason == nil) != (create.Reason == nil) || (stored.Entry == nil) != (create.Entry == nil) {
				t.Fatalf("the member's fields did not survive: %+v from %+v", stored, create)
			}
		})
	}
}

// SettledSubmissionRecord is the SubmissionRecord whose status is done or unanswered: input done (entry, answer), input unanswered (reason, detail, optional entry),
// write done (entry), write unanswered (reason, detail); each is stored as it is, and a settled record is found by its status filter.
// mutation-checked: making MemoryStorage.ScanSubmissions ignore the status filter fails it.
func TestSettledSubmissionRecordUnionEveryVariantIsStored(t *testing.T) {
	ctx := context.Background()
	memory := storage.NewMemoryStorage()
	root := durable.ROOT_CONVERSATION_ID
	mint := func() int64 {
		id, err := memory.MintId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	entry := durable.IdFromNumber[durable.EntryId](mint())
	answer := durable.IdFromNumber[durable.EntryId](mint())
	reason := "settled"
	var settled []durable.SettledSubmissionRecord
	for _, record := range []durable.SettledSubmissionRecord{
		{Type: durable.SubmissionTypeInput, Status: durable.SubmissionDone, Entry: &entry, Answer: &answer},
		{Type: durable.SubmissionTypeInput, Status: durable.SubmissionUnanswered, Reason: &reason, Detail: map[string]any{"k": "v"}},
		{Type: durable.SubmissionTypeInput, Status: durable.SubmissionUnanswered, Entry: &entry, Reason: &reason},
		{Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &entry},
		{Type: durable.SubmissionTypeWrite, Status: durable.SubmissionUnanswered, Reason: &reason},
	} {
		record.Id, record.ConversationId = durable.IdFromNumber[durable.SubmissionId](mint()), root
		settled = append(settled, record)
	}
	writes := []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: root}}}
	for _, record := range settled {
		writes = append(writes, durable.SubmissionWrite{Value: record})
	}
	if _, err := memory.Commit(ctx, writes, func(durable.Seq) {}); err != nil {
		t.Fatal(err)
	}
	for _, record := range settled {
		stored, err := memory.Submission(ctx, record.Id)
		if err != nil || stored == nil || !reflect.DeepEqual(*stored, record) {
			t.Fatalf("settled %s/%s: stored = %+v err=%v, want %+v", record.Type, record.Status, stored, err, record)
		}
	}
	for _, status := range []durable.SubmissionStatus{durable.SubmissionDone, durable.SubmissionUnanswered} {
		page, err := memory.ScanSubmissions(ctx, durable.SubmissionQuery{Status: &status}, 10, durable.Cursor{})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		for _, record := range settled {
			if record.Status == status {
				want++
			}
		}
		if len(page.Items) != want {
			t.Fatalf("scan for %s found %d records, want %d", status, len(page.Items), want)
		}
		for _, item := range page.Items {
			if item.Status != status {
				t.Fatalf("scan for %s returned %+v", status, item)
			}
		}
	}
}

// DocumentCreate: a session document; a conversation document that is latest (fork current | initial) or rewindable (fork asOf | current | initial); a task document.
// mutation-checked: dropping History or Fork from the stored document record fails it.
func TestDocumentCreateUnionEveryScopeHistoryAndForkIsStored(t *testing.T) {
	ctx := context.Background()
	conversation := durable.IdFromNumber[durable.ConversationId](21)
	task := durable.IdFromNumber[durable.TaskId](22)
	conversationScope := durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversation}
	var cases []durable.DocumentCreate
	cases = append(cases,
		durable.DocumentCreate{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
		durable.DocumentCreate{Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: task}},
	)
	for _, fork := range []durable.DocumentFork{durable.ForkCurrent, durable.ForkInitial} {
		cases = append(cases, durable.DocumentCreate{Scope: conversationScope, History: durable.HistoryLatest, Fork: fork})
	}
	for _, fork := range []durable.DocumentFork{durable.ForkAsOf, durable.ForkCurrent, durable.ForkInitial} {
		cases = append(cases, durable.DocumentCreate{Scope: conversationScope, History: durable.HistoryRewindable, Fork: fork})
	}
	memory := storage.NewMemoryStorage()
	var created []durable.DocumentCreate
	var writes []durable.StorageWrite
	for _, create := range cases {
		id, err := memory.MintId()
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("k%d", id)
		create.Id, create.Kind, create.Key = durable.IdFromNumber[durable.DocumentId](id), "union.doc", &key
		created = append(created, create)
		writes = append(writes, durable.DocumentCreateWrite{Record: create, Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("v", 1)}})
	}
	if _, err := memory.Commit(ctx, writes, func(durable.Seq) {}); err != nil {
		t.Fatal(err)
	}
	for _, create := range created {
		stored, err := memory.Document(ctx, create.Id, durable.CurrentPoint)
		if err != nil || stored == nil {
			t.Fatalf("document %+v: %v err=%v", create, stored, err)
		}
		record := stored.Record
		if record.Scope != create.Scope || record.History != create.History || record.Fork != create.Fork || record.Kind != create.Kind || record.Key == nil || *record.Key != *create.Key || record.RetiredAt != nil || record.CreatedAt == 0 {
			t.Fatalf("scope %+v history %q fork %q stored as %+v", create.Scope, create.History, create.Fork, record)
		}
	}
}
