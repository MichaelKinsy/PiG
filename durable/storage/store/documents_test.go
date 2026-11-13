package store

// pi: packages/durable/src/storage/store/documents.ts
// The module is internal to Pi (its public entry point exports none of namespaceOf, addressOf, historyOf, engineRecord, durableRecord,
// recordOfEntry, aliveAt), so the cases below assert each against the text of documents.ts; the storage-level cases (claims, other
// owners' documents) run through StoreStorage.

import (
	"strings"
	"testing"

	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/durable"
)

func TestNamespaceOfIsTheScopesNamespace(t *testing.T) {
	cases := []struct {
		scope durable.DocumentRecordScope
		want  string
	}{
		{durable.DocumentRecordScope{Kind: durable.ScopeSession}, "session"},
		// The other id of a scope is never part of its namespace.
		{durable.DocumentRecordScope{Kind: durable.ScopeSession, ConversationId: 3, TaskId: 9}, "session"},
		{durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 3, TaskId: 9}, "conversation:3"},
		{durable.DocumentRecordScope{Kind: durable.ScopeTask, ConversationId: 3, TaskId: 9}, "task:9"},
	}
	for _, c := range cases {
		if got := NamespaceOf(c.scope); got != c.want {
			t.Errorf("NamespaceOf(%+v) = %q, want %q", c.scope, got, c.want)
		}
	}
}

func TestAddressOfEncodesKindAndKeyUnderTheOwner(t *testing.T) {
	session := durable.DocumentRecordScope{Kind: durable.ScopeSession}
	empty, quoted := "", `a"b`
	t.Run("a singleton has no key", func(t *testing.T) {
		got := AddressOf(`k"1`, session, nil, "harness")
		if got.Owner != "harness" || got.Namespace != "session" || got.Kind != `"k\"1"` || got.Key != nil {
			t.Fatalf("AddressOf = %+v, want owner harness, namespace session, kind \"k\\\"1\", no key", got)
		}
	})
	t.Run("an empty key is a key", func(t *testing.T) {
		// addressOf tests `key === undefined`: "" is a family member whose key is the encoded empty string.
		got := AddressOf("k", session, &empty, "harness")
		if got.Key == nil || *got.Key != `""` {
			t.Fatalf("AddressOf key = %v, want \"\"", got.Key)
		}
	})
	t.Run("a key is JSON-encoded", func(t *testing.T) {
		got := AddressOf("k", durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: 5}, &quoted, "other")
		if got.Key == nil || *got.Key != `"a\"b"` || got.Namespace != "task:5" || got.Owner != "other" {
			t.Fatalf("AddressOf = %+v key %v, want key \"a\\\"b\" in task:5 of other", got, got.Key)
		}
	})
}

func TestHistoryOfKeepsOnlyRewindableConversationDocumentsRewindable(t *testing.T) {
	cases := []struct {
		name    string
		scope   durable.DocumentRecordScope
		history durable.DocumentHistory
		want    chordstore.DocumentHistory
	}{
		{"a rewindable conversation document", durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 1}, durable.HistoryRewindable, chordstore.HistoryRewindable},
		{"a latest conversation document", durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 1}, durable.HistoryLatest, chordstore.HistoryLatest},
		{"a conversation document without a history", durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 1}, "", chordstore.HistoryLatest},
		{"a rewindable session document", durable.DocumentRecordScope{Kind: durable.ScopeSession}, durable.HistoryRewindable, chordstore.HistoryLatest},
		{"a rewindable task document", durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: 1}, durable.HistoryRewindable, chordstore.HistoryLatest},
	}
	for _, c := range cases {
		if got := HistoryOf(c.scope, c.history); got != c.want {
			t.Errorf("%s: HistoryOf = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEngineRecordIsTheCreationAtItsSeqWithTheDurableRecordInMeta(t *testing.T) {
	key := "family"
	create := durable.DocumentCreate{
		Id: 41, Kind: "k", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 7},
		History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
	}
	live, err := EngineRecord(create, "harness", 3, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	if live.ID != 41 || live.Owner != "harness" || live.Namespace != "conversation:7" || live.Kind != `"k"` ||
		live.Key == nil || *live.Key != `"family"` || live.History != chordstore.HistoryRewindable ||
		live.CreatedAt != 12 || live.RetiredAt != nil || live.Revision != 1 || live.Version != 3 || live.Fingerprint != "" {
		t.Fatalf("EngineRecord = %+v, want the creation at seq 12 at revision 1, version 3, live", live)
	}
	wantMeta := `{"id":41,"kind":"k","key":"family","scope":{"kind":"conversation","conversationId":7},"history":"rewindable","fork":"asOf","createdAt":12}`
	if live.Meta == nil || *live.Meta != wantMeta {
		t.Fatalf("meta = %v, want %s", live.Meta, wantMeta)
	}
	retired, err := EngineRecord(create, "harness", 3, 12, true)
	if err != nil {
		t.Fatal(err)
	}
	if retired.RetiredAt == nil || *retired.RetiredAt != 12 {
		t.Fatalf("a create-plus-retire retires at its creation seq: RetiredAt = %v, want 12", retired.RetiredAt)
	}
	// Only the engine's retired_at records a retirement: the meta never holds one.
	if retired.Meta == nil || strings.Contains(*retired.Meta, "retiredAt") {
		t.Fatalf("meta of a retired creation = %v, want none of retiredAt", retired.Meta)
	}
}

func TestDurableRecordIsOnlyAnEngineRecordOfThisStoragesOwnerAndScopes(t *testing.T) {
	meta := `{"id":5,"kind":"k","scope":{"kind":"session"},"createdAt":2}`
	record := func(owner, namespace string, text *string) chordstore.DocumentRecord {
		return chordstore.DocumentRecord{ID: 5, Owner: owner, Namespace: namespace, Kind: `"k"`, Meta: text}
	}
	owned := []string{"session", "conversation:", "conversation:8", "task:", "task:8"}
	for _, namespace := range owned {
		found, err := DurableRecord(record("harness", namespace, &meta), "harness")
		if err != nil || found == nil || found.Id != 5 || found.Kind != "k" {
			t.Errorf("namespace %q of the owner: DurableRecord = %+v, %v, want the record", namespace, found, err)
		}
	}
	refused := []struct {
		name   string
		record chordstore.DocumentRecord
	}{
		{"another owner's", record("other", "session", &meta)},
		{"another owner's conversation document", record("other", "conversation:8", &meta)},
		{"a Chord document of the owner", record("harness", "chord", &meta)},
		{"a namespace that only starts like a scope", record("harness", "sessions", &meta)},
		{"a namespace that holds a scope's name", record("harness", "x:task:8", &meta)},
		{"a record without meta", record("harness", "session", nil)},
	}
	for _, c := range refused {
		if found, err := DurableRecord(c.record, "harness"); err != nil || found != nil {
			t.Errorf("%s: DurableRecord = %+v, %v, want nil", c.name, found, err)
		}
	}
}

func TestRecordOfEntryReadsMetaAndSetsTheRetirement(t *testing.T) {
	meta := `{"id":5,"kind":"k","key":"","scope":{"kind":"task","taskId":2},"createdAt":2}`
	live, err := RecordOfEntry(chordstore.DocumentEntry{Meta: &meta})
	if err != nil {
		t.Fatal(err)
	}
	if live.Id != 5 || live.Key == nil || *live.Key != "" || live.Scope.TaskId != 2 || live.CreatedAt != 2 || live.RetiredAt != nil {
		t.Fatalf("live record = %+v", live)
	}
	retiredAt := int64(9)
	retired, err := RecordOfEntry(chordstore.DocumentEntry{Meta: &meta, RetiredAt: &retiredAt})
	if err != nil {
		t.Fatal(err)
	}
	if retired.RetiredAt == nil || *retired.RetiredAt != 9 {
		t.Fatalf("retired record RetiredAt = %v, want 9", retired.RetiredAt)
	}
	if _, err := RecordOfEntry(chordstore.DocumentEntry{}); err == nil {
		t.Fatal("an entry without meta is an error")
	}
}

func TestAliveAtIsTheHalfOpenLifetime(t *testing.T) {
	seq := func(n int) *durable.Seq { value := durable.Seq(n); return &value }
	live := durable.DocumentRecord{CreatedAt: 3}
	retired := durable.DocumentRecord{CreatedAt: 3, RetiredAt: seq(6)}
	cases := []struct {
		name   string
		record durable.DocumentRecord
		at     durable.DocumentPoint
		want   bool
	}{
		{"live, current", live, durable.CurrentPoint, true},
		{"retired, current", retired, durable.CurrentPoint, false},
		{"before its creation", live, durable.AtSeq(2), false},
		{"at its creation", live, durable.AtSeq(3), true},
		{"live, long after", live, durable.AtSeq(100), true},
		{"retired, the seq before its retirement", retired, durable.AtSeq(5), true},
		{"retired, at its retirement", retired, durable.AtSeq(6), false},
		{"retired, after", retired, durable.AtSeq(7), false},
	}
	for _, c := range cases {
		if got := AliveAt(c.record, c.at); got != c.want {
			t.Errorf("%s: AliveAt = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStoreStorageClaimsEveryScopeNamespaceOfItsOwner(t *testing.T) {
	store := memoryStore(t)
	storage := value(Open(background, store, "harness"))
	other := &chordstore.NamedClaimant{Label: "another user of the store"}
	chord := store.(*chordstore.ChordStore)
	for _, namespace := range []string{"session", "conversation:", "task:"} {
		rejectedArgs(t, chord.Claims.Documents("harness", namespace, other))
	}
	// Another owner's namespaces are free, and a closed storage releases its own.
	must(t, chord.Claims.Documents("elsewhere", "task:", other))
	must(t, storage.Close(background))
	for _, namespace := range []string{"session", "conversation:", "task:"} {
		must(t, chord.Claims.Documents("harness", namespace, other))
	}
	must(t, store.Close(background))
}

func TestStoreStorageDoesNotReadAnotherOwnersDocuments(t *testing.T) {
	store := memoryStore(t)
	mine := value(Open(background, store, "harness"))
	theirs := value(Open(background, store, "other"))
	id := mintID[durable.DocumentId](t, mine)
	value(commitWrites(mine, durable.DocumentCreateWrite{
		Record:  durable.DocumentCreate{Id: id, Kind: "k", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
		Content: base(1),
	}))
	if found := value(mine.Document(background, id, durable.CurrentPoint)); found == nil {
		t.Fatal("the owner does not read its document")
	}
	if found := value(theirs.Document(background, id, durable.CurrentPoint)); found != nil {
		t.Fatalf("another owner reads document %d: %+v", id, found)
	}
	// The id is claimed in the store's one namespace, so the other owner's commit of it is refused as well.
	if _, err := commitWrites(theirs, durable.DocumentChangeWrite{Id: id, Content: deltaTo(2)}); err == nil || !strings.Contains(err.Error(), "Unknown document") {
		t.Fatalf("another owner's change of document %d = %v, want Unknown document", id, err)
	}
	must(t, store.Close(background))
}
