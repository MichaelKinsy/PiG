package session_test

// pi: packages/durable/src/session/keys.ts

import (
	"context"
	"errors"
	"fmt"
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

// recordingStorage logs every call that reaches it, with its arguments, so a test sees which backend method a gated read
// reached and with what.
type recordingStorage struct {
	durable.Storage
	log *readLog
}

func (storage recordingStorage) record(call string, args ...any) {
	storage.log.events = append(storage.log.events, fmt.Sprint(append([]any{"read " + call}, args...)...))
}

func (storage recordingStorage) Commit(_ context.Context, batches [][]durable.StorageWrite, _ func(int, durable.Seq)) (durable.CommitResult, error) {
	storage.record("commit", " ", len(batches))
	return durable.CommitResult{Stored: len(batches)}, nil
}

func (storage recordingStorage) MintId() (int64, error) {
	storage.record("mint")
	return 41, nil
}

func (storage recordingStorage) Conversation(_ context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	storage.record("conversation", " ", id)
	return nil, nil
}

func (storage recordingStorage) ScanConversations(_ context.Context, _ durable.ConversationQuery, limit int, _ durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	storage.record("scan conversations", " ", limit)
	return durable.Page[durable.ConversationRecord, durable.Cursor]{}, nil
}

func (storage recordingStorage) Entry(_ context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	storage.record("entry", " ", id)
	return nil, nil
}

func (storage recordingStorage) VisibleEntry(_ context.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	storage.record("visible entry", " ", conversationId, " ", id)
	return nil, nil
}

func (storage recordingStorage) FindLatestHeadMarker(_ context.Context, conversationId durable.ConversationId, atOrBefore *durable.EntryId) (*durable.EntryRecord, error) {
	storage.record("find latest head marker", " ", conversationId, " ", *atOrBefore)
	return nil, nil
}

func (storage recordingStorage) ScanEntries(_ context.Context, query durable.EntryQuery, limit int, _ durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	storage.record("scan entries", " ", query.ConversationId, " ", limit)
	return durable.Page[durable.EntryRecord, durable.Cursor]{}, nil
}

func (storage recordingStorage) Task(_ context.Context, id durable.TaskId) (*anyTask, error) {
	storage.record("task", " ", id)
	return nil, nil
}

func (storage recordingStorage) ScanTasks(_ context.Context, _ durable.TaskQuery, limit int, _ durable.Cursor) (durable.Page[anyTask, durable.Cursor], error) {
	storage.record("scan tasks", " ", limit)
	return durable.Page[anyTask, durable.Cursor]{}, nil
}

func (storage recordingStorage) Submission(_ context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	storage.record("submission", " ", id)
	return nil, nil
}

func (storage recordingStorage) ScanSubmissions(_ context.Context, _ durable.SubmissionQuery, limit int, _ durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	storage.record("scan submissions", " ", limit)
	return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, nil
}

func (storage recordingStorage) SubmissionByRequest(_ context.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	storage.record("submission by request", " ", conversationId, " ", requestId)
	return nil, nil
}

func (storage recordingStorage) FindDocument(_ context.Context, address durable.DocumentAddress, _ durable.DocumentPoint) (*durable.DocumentRecord, error) {
	storage.record("find document", " ", durable.AddressId(address))
	return nil, nil
}

func (storage recordingStorage) Document(_ context.Context, id durable.DocumentId, _ durable.DocumentPoint) (*durable.StoredDocument, error) {
	storage.record("document", " ", id)
	return nil, nil
}

func (storage recordingStorage) ScanDocuments(_ context.Context, query durable.DocumentQuery, limit int, _ durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	storage.record("scan documents", " ", query.Scope.Kind, " ", limit)
	return durable.Page[durable.DocumentRecord, durable.Cursor]{}, nil
}

func (storage recordingStorage) Close(context.Context) error {
	storage.record("close")
	return nil
}

// pi: keys.ts GatedStorage :130-262: each Storage read touches its own key first, the read it forwards carries the
// caller's arguments, and Commit, MintId and Close pass straight through without a touch. `entry(conversationId, id)` touches
// the conversation, then the entry; the three entry reads of a conversation touch its creation, then its appends.
// mutation-checked: a read that touches another read's key, forwards another argument, touches AllKeys for a keyed read or a key for a scan, drops the second touch of VisibleEntry, or touches before a pass-through call fails it
func TestGatedStorageEveryReadTouchesItsOwnKeyAndForwardsItsArguments(t *testing.T) {
	log := &readLog{}
	gated := session.NewGatedStorage(recordingStorage{log: log}, func(_ context.Context, key string) error {
		log.events = append(log.events, "touch "+key)
		return nil
	})
	name := "member"
	address := durable.DocumentAddress{Kind: "k", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, Key: &name}
	cutoff := durable.EntryId(12)
	cases := []struct {
		name string
		call func() error
		want []string
	}{
		{"Conversation", func() error { _, err := gated.Conversation(ctx, 2); return err }, []string{"touch conv:2", "read conversation 2"}},
		{"ScanConversations", func() error { _, err := gated.ScanConversations(ctx, durable.ConversationQuery{}, 11, nil); return err }, []string{"touch *", "read scan conversations 11"}},
		{"Entry", func() error { _, err := gated.Entry(ctx, 9); return err }, []string{"touch entry:9", "read entry 9"}},
		{"VisibleEntry", func() error { _, err := gated.VisibleEntry(ctx, 2, 9); return err }, []string{"touch conv:2", "touch entry:9", "read visible entry 2 9"}},
		{"FindLatestHeadMarker", func() error { _, err := gated.FindLatestHeadMarker(ctx, 3, &cutoff); return err }, []string{"touch conv:3", "touch entry-append:3", "read find latest head marker 3 12"}},
		{"ScanEntries", func() error {
			_, err := gated.ScanEntries(ctx, durable.EntryQuery{ConversationId: 4}, 12, nil)
			return err
		}, []string{"touch conv:4", "touch entry-append:4", "read scan entries 4 12"}},
		{"Task", func() error { _, err := gated.Task(ctx, 6); return err }, []string{"touch task:6", "read task 6"}},
		{"ScanTasks", func() error { _, err := gated.ScanTasks(ctx, durable.TaskQuery{}, 13, nil); return err }, []string{"touch *", "read scan tasks 13"}},
		{"Submission", func() error { _, err := gated.Submission(ctx, 5); return err }, []string{"touch sub:5", "read submission 5"}},
		{"ScanSubmissions", func() error { _, err := gated.ScanSubmissions(ctx, durable.SubmissionQuery{}, 14, nil); return err }, []string{"touch *", "read scan submissions 14"}},
		{"SubmissionByRequest", func() error { _, err := gated.SubmissionByRequest(ctx, 7, "r-9"); return err }, []string{"touch req:7:r-9", "read submission by request 7 r-9"}},
		{"FindDocument", func() error { _, err := gated.FindDocument(ctx, address, durable.DocumentPoint{}); return err }, []string{"touch doc:" + durable.AddressId(address), "read find document " + durable.AddressId(address)}},
		{"Document", func() error { _, err := gated.Document(ctx, 8, durable.DocumentPoint{}); return err }, []string{"touch docid:8", "read document 8"}},
		{"ScanDocuments", func() error {
			_, err := gated.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 3}}, 15, nil)
			return err
		}, []string{"touch docscope:c3", "read scan documents conversation 15"}},
	}
	for _, tc := range cases {
		log.events = nil
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(log.events, tc.want) {
			t.Errorf("%s: events = %q\nwant %q", tc.name, log.events, tc.want)
		}
	}

	// Pass-through calls reach the backend with no touch.
	log.events = nil
	if result, err := gated.Commit(ctx, [][]durable.StorageWrite{nil, nil}, func(int, durable.Seq) {}); err != nil || result.Stored != 2 {
		t.Fatalf("Commit = %+v, %v", result, err)
	}
	if id, err := gated.MintId(); err != nil || id != 41 {
		t.Fatalf("MintId = %d, %v", id, err)
	}
	if err := gated.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"read commit 2", "read mint", "read close"}; !slices.Equal(log.events, want) {
		t.Fatalf("pass-through events = %q, want %q", log.events, want)
	}
}

// pi: keys.ts `#after`: a read whose touch fails is not made, whichever read it is (Pi's gate rejects, and `gate.then(read)`
// never runs it); a touch that fails at the second key of a read that touches two skips the read too.
// mutation-checked: a read that ignores its touch's error, or one that touches the second key without checking the first, fails it
func TestGatedStorageAFailedTouchSkipsEveryRead(t *testing.T) {
	log := &readLog{}
	boom := errors.New("touch failed")
	failOn := ""
	touched := 0
	gated := session.NewGatedStorage(recordingStorage{log: log}, func(_ context.Context, key string) error {
		touched++
		if failOn == "" || key == failOn {
			return boom
		}
		return nil
	})
	name := "member"
	address := durable.DocumentAddress{Kind: "k", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, Key: &name}
	cutoff := durable.EntryId(1)
	reads := map[string]func() error{
		"Conversation":         func() error { _, err := gated.Conversation(ctx, 2); return err },
		"ScanConversations":    func() error { _, err := gated.ScanConversations(ctx, durable.ConversationQuery{}, 1, nil); return err },
		"Entry":                func() error { _, err := gated.Entry(ctx, 9); return err },
		"VisibleEntry":         func() error { _, err := gated.VisibleEntry(ctx, 2, 9); return err },
		"FindLatestHeadMarker": func() error { _, err := gated.FindLatestHeadMarker(ctx, 3, &cutoff); return err },
		"ScanEntries": func() error {
			_, err := gated.ScanEntries(ctx, durable.EntryQuery{ConversationId: 4}, 1, nil)
			return err
		},
		"Task":                func() error { _, err := gated.Task(ctx, 6); return err },
		"ScanTasks":           func() error { _, err := gated.ScanTasks(ctx, durable.TaskQuery{}, 1, nil); return err },
		"Submission":          func() error { _, err := gated.Submission(ctx, 5); return err },
		"ScanSubmissions":     func() error { _, err := gated.ScanSubmissions(ctx, durable.SubmissionQuery{}, 1, nil); return err },
		"SubmissionByRequest": func() error { _, err := gated.SubmissionByRequest(ctx, 7, "r"); return err },
		"FindDocument":        func() error { _, err := gated.FindDocument(ctx, address, durable.DocumentPoint{}); return err },
		"Document":            func() error { _, err := gated.Document(ctx, 8, durable.DocumentPoint{}); return err },
		"ScanDocuments": func() error {
			_, err := gated.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, 1, nil)
			return err
		},
	}
	for name, read := range reads {
		touched = 0
		if err := read(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the touch's", name, err)
		}
		if touched != 1 {
			t.Errorf("%s touched %d keys after the first failed, want 1", name, touched)
		}
	}
	if len(log.events) != 0 {
		t.Fatalf("a read was made after its touch failed: %q", log.events)
	}

	// The second touch of a two-key read fails: the read is not made, and the entry's touch is not skipped on success of the first.
	for _, second := range []struct {
		failOn string
		read   string
	}{{"entry:9", "VisibleEntry"}, {"entry-append:3", "FindLatestHeadMarker"}, {"entry-append:4", "ScanEntries"}} {
		failOn, touched = second.failOn, 0
		if err := reads[second.read](); !errors.Is(err, boom) {
			t.Errorf("%s with %s failing: err = %v", second.read, second.failOn, err)
		}
		if touched != 2 {
			t.Errorf("%s touched %d keys, want 2 (the second is the one that failed)", second.read, touched)
		}
	}
	if len(log.events) != 0 {
		t.Fatalf("a read was made after its second touch failed: %q", log.events)
	}
}
