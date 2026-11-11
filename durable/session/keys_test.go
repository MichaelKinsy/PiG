package session_test

// pi: packages/durable/src/session/keys.ts

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// readLog records the order of touches and reads that reached it.
type readLog struct{ events []string }

type loggedStorage struct {
	durable.Storage
	log *readLog
}

func (storage loggedStorage) Conversation(context.Context, durable.ConversationId) (*durable.ConversationRecord, error) {
	storage.log.events = append(storage.log.events, "read conversation")
	return nil, nil
}

func (storage loggedStorage) VisibleEntry(context.Context, durable.ConversationId, durable.EntryId) (*durable.EntryAt, error) {
	storage.log.events = append(storage.log.events, "read visible entry")
	return nil, nil
}

func (storage loggedStorage) ScanEntries(context.Context, durable.EntryQuery, int, durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	storage.log.events = append(storage.log.events, "read scan entries")
	return durable.Page[durable.EntryRecord, durable.Cursor]{}, nil
}

func (storage loggedStorage) ScanTasks(context.Context, durable.TaskQuery, int, durable.Cursor) (durable.Page[anyTask, durable.Cursor], error) {
	storage.log.events = append(storage.log.events, "read scan tasks")
	return durable.Page[anyTask, durable.Cursor]{}, nil
}

func (storage loggedStorage) FindDocument(context.Context, durable.DocumentAddress, durable.DocumentPoint) (*durable.DocumentRecord, error) {
	storage.log.events = append(storage.log.events, "read find document")
	return nil, nil
}

func (storage loggedStorage) ScanDocuments(context.Context, durable.DocumentQuery, int, durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	storage.log.events = append(storage.log.events, "read scan documents")
	return durable.Page[durable.DocumentRecord, durable.Cursor]{}, nil
}

func TestKeyFormats(t *testing.T) {
	cases := [][2]string{
		{session.TaskKey(7), "task:7"},
		{session.ConversationKey(2), "conv:2"},
		{session.EntryKey(9), "entry:9"},
		{session.AppendKey(2), "entry-append:2"},
		{session.SubmissionKey(4), "sub:4"},
		{session.RequestKey(2, "r-1"), "req:2:r-1"},
		{session.DocumentKey(`["k","session"]`), `doc:["k","session"]`},
		{session.IncarnationKey(5), "docid:5"},
		{session.ScopeKey(durable.DocumentRecordScope{Kind: durable.ScopeSession}), "docscope:session"},
		{session.ScopeKey(durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 3}), "docscope:c3"},
		{session.ScopeKey(durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: 8}), "docscope:t8"},
		{session.OwnedKey(durable.TaskId(6)), "owned:6"},
		{session.OwnedKey("c2"), "owned:c2"},
		{session.RootsKey, "roots"},
		{session.IntentKey, "intent"},
		{session.AllKeys, "*"},
	}
	for _, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("key = %q, want %q", pair[0], pair[1])
		}
	}
}

func TestTableKeys(t *testing.T) {
	request := "r"
	var keys []string
	session.TableKeys([]durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 2}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: 9, ConversationId: 2}},
		durable.TaskWrite{Value: taskRecord(3, durable.TaskPending)},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: 4, ConversationId: 2}},
		durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: 5, ConversationId: 2, RequestId: &request}},
		durable.DocumentRetireWrite{Id: 1},
	}, &keys)
	want := []string{"conv:2", "entry:9", "entry-append:2", "task:3", "sub:4", "sub:5", "req:2:r"}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func TestGatedStorageReadsTouchTheirKeysFirst(t *testing.T) {
	log := &readLog{}
	gated := session.NewGatedStorage(loggedStorage{log: log}, func(_ context.Context, key string) error {
		log.events = append(log.events, "touch "+key)
		return nil
	})
	key := "member"
	address := durable.DocumentAddress{Kind: "k", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, Key: &key}
	_, _ = gated.Conversation(ctx, 2)
	_, _ = gated.VisibleEntry(ctx, 2, 9)
	_, _ = gated.ScanEntries(ctx, durable.EntryQuery{ConversationId: 2}, 10, nil)
	_, _ = gated.ScanTasks(ctx, durable.TaskQuery{}, 10, nil)
	_, _ = gated.FindDocument(ctx, address, durable.DocumentPoint{})
	_, _ = gated.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: 8}}, 10, nil)
	want := []string{
		"touch conv:2", "read conversation",
		"touch conv:2", "touch entry:9", "read visible entry",
		"touch conv:2", "touch entry-append:2", "read scan entries",
		"touch *", "read scan tasks",
		"touch doc:" + durable.AddressId(address), "read find document",
		"touch docscope:t8", "read scan documents",
	}
	if !slices.Equal(log.events, want) {
		t.Fatalf("events = %q\nwant     %q", log.events, want)
	}
}

func TestGatedStorageFailedTouchSkipsTheRead(t *testing.T) {
	log := &readLog{}
	boom := errors.New("touch failed")
	gated := session.NewGatedStorage(loggedStorage{log: log}, func(context.Context, string) error { return boom })
	if _, err := gated.Conversation(ctx, 2); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := gated.ScanEntries(ctx, durable.EntryQuery{ConversationId: 2}, 10, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if len(log.events) != 0 {
		t.Fatalf("events = %q", log.events)
	}
}
