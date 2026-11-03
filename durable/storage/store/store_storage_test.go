package store

// pi: packages/durable/src/storage/store/commit.ts
// pi: packages/durable/src/storage/store/documents.ts
// pi: packages/durable/src/storage/store/queries.ts
// StoreStorage runs on these modules, which Pi's public entry point does not export, so the conformance suite below is what
// proves them: each mutant of portmap-mutants.json fails it (tables.ts is marked in store_storage_commit_test.go).

// Ports packages/durable/test/store-storage.test.ts
//
// StoreStorage (slice 6b §2): Durable's storage conformance suite on SQLite `:memory:` and across reopen, then what
// is its own: claims and close, owners sharing a store, the store's semantics for a refused commit, the allocator,
// archives, head markers, reads, commits.
//
// The cases that need a Chord host or a Harness (commits inside the host's batch, Durable's views as Chord sources,
// the collected closed StoreStorage) are in store_storage_host_test.go.

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// reopeningStorage is a storage that closes and reopens its store file after every commit (Durable's reopen
// harness). Pi's reads run synchronously on the storage current at the call; here a read holds the read lock for its
// whole call, so a read started inside a receipt (on its own goroutine) runs on the old storage before it closes, or on
// the reopened one, never on a closed one.
type reopeningStorage struct {
	t       *testing.T
	mu      sync.RWMutex
	current *ownedStorage
	path    string
	closed  bool
}

// via runs call on the current storage, which stays open until call returns.
func via[T any](storage *reopeningStorage, call func(*ownedStorage) (T, error)) (T, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	return call(storage.current)
}

func (storage *reopeningStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	storage.mu.RLock()
	current, closed := storage.current, storage.closed
	storage.mu.RUnlock()
	if closed {
		return current.Commit(ctx, batches, receive)
	}
	defer func() {
		storage.mu.Lock()
		defer storage.mu.Unlock()
		must(storage.t, storage.current.Close(background))
		storage.current = openStoreStorage(storage.t, storage.path)
	}()
	return current.Commit(ctx, batches, receive)
}

func (storage *reopeningStorage) MintId() (int64, error) {
	return via(storage, func(current *ownedStorage) (int64, error) { return current.MintId() })
}
func (storage *reopeningStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	return via(storage, func(current *ownedStorage) (*durable.ConversationRecord, error) { return current.Conversation(ctx, id) })
}
func (storage *reopeningStorage) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	return via(storage, func(current *ownedStorage) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
		return current.ScanConversations(ctx, query, limit, cursor)
	})
}
func (storage *reopeningStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	return via(storage, func(current *ownedStorage) (*durable.EntryAt, error) { return current.Entry(ctx, id) })
}
func (storage *reopeningStorage) VisibleEntry(ctx context.Context, conversationID durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	return via(storage, func(current *ownedStorage) (*durable.EntryAt, error) {
		return current.VisibleEntry(ctx, conversationID, id)
	})
}
func (storage *reopeningStorage) FindLatestHeadMarker(ctx context.Context, conversationID durable.ConversationId, at *durable.EntryId) (*durable.EntryRecord, error) {
	return via(storage, func(current *ownedStorage) (*durable.EntryRecord, error) {
		return current.FindLatestHeadMarker(ctx, conversationID, at)
	})
}
func (storage *reopeningStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	return via(storage, func(current *ownedStorage) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		return current.ScanEntries(ctx, query, limit, cursor)
	})
}
func (storage *reopeningStorage) Task(ctx context.Context, id durable.TaskId) (*storedTask, error) {
	return via(storage, func(current *ownedStorage) (*storedTask, error) { return current.Task(ctx, id) })
}
func (storage *reopeningStorage) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[storedTask, durable.Cursor], error) {
	return via(storage, func(current *ownedStorage) (durable.Page[storedTask, durable.Cursor], error) {
		return current.ScanTasks(ctx, query, limit, cursor)
	})
}
func (storage *reopeningStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	return via(storage, func(current *ownedStorage) (*durable.SubmissionRecord, error) { return current.Submission(ctx, id) })
}
func (storage *reopeningStorage) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	return via(storage, func(current *ownedStorage) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
		return current.ScanSubmissions(ctx, query, limit, cursor)
	})
}
func (storage *reopeningStorage) SubmissionByRequest(ctx context.Context, conversationID durable.ConversationId, requestID string) (*durable.SubmissionRecord, error) {
	return via(storage, func(current *ownedStorage) (*durable.SubmissionRecord, error) {
		return current.SubmissionByRequest(ctx, conversationID, requestID)
	})
}
func (storage *reopeningStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	return via(storage, func(current *ownedStorage) (*durable.DocumentRecord, error) {
		return current.FindDocument(ctx, address, at)
	})
}
func (storage *reopeningStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	return via(storage, func(current *ownedStorage) (*durable.StoredDocument, error) { return current.Document(ctx, id, at) })
}
func (storage *reopeningStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	return via(storage, func(current *ownedStorage) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
		return current.ScanDocuments(ctx, query, limit, cursor)
	})
}

func (storage *reopeningStorage) Close(ctx context.Context) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.closed {
		return nil
	}
	storage.closed = true
	return storage.current.Close(ctx)
}

func TestStoreStorageConformance(t *testing.T) {
	durabletest.RegisterStorageConformance(t, "StoreStorage", func(use func(storage durable.Storage) error) error {
		storage := openStoreStorage(t, ":memory:")
		defer func() { _ = storage.Close(background) }()
		return use(storage)
	})
	durabletest.RegisterStorageConformance(t, "StoreStorage across reopen", func(use func(storage durable.Storage) error) error {
		path := filepath.Join(t.TempDir(), "store.sqlite")
		storage := &reopeningStorage{t: t, current: openStoreStorage(t, path), path: path}
		defer func() { _ = storage.Close(background) }()
		return use(storage)
	})
}

func rejectedArgs(t *testing.T, err error) {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != data.CodeInvalid || chordErr.Reason != data.ReasonArgs {
		t.Fatalf("error = %v, want invalid/args", err)
	}
}

func rootWrite() durable.StorageWrite {
	return durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}
}

func TestStoreStorageClaimsAndClose(t *testing.T) {
	t.Run("refuse a second StoreStorage on one owner until the first closed; two owners share a store", func(t *testing.T) {
		store := memoryStore(t)
		first := value(Open(background, store, "harness"))
		_, err := Open(background, store, "harness")
		rejectedArgs(t, err)
		other := value(Open(background, store, "other"))
		value(commitWrites(first, rootWrite()))
		if found := value(other.Conversation(background, durable.ROOT_CONVERSATION_ID)); found != nil {
			t.Fatalf("another owner sees %+v", found)
		}
		must(t, first.Close(background))
		if _, err := first.Conversation(background, durable.ROOT_CONVERSATION_ID); err == nil || !strings.Contains(err.Error(), "StoreStorage is closed") {
			t.Fatalf("read on a closed storage = %v", err)
		}
		reopened := value(Open(background, store, "harness"))
		found := value(reopened.Conversation(background, durable.ROOT_CONVERSATION_ID))
		if found == nil || found.Id != durable.ROOT_CONVERSATION_ID {
			t.Fatalf("reopened sees %+v", found)
		}
		must(t, store.Close(background))
	})

	t.Run("waits at close for its calls underway, then releases its claims", func(t *testing.T) {
		// Pi commits the root conversation on a fresh store here; Go holds an entry commit at its first statement instead, so
		// the call is underway at a known point and the test is deterministic. The expectations are Pi's: Close waits for the
		// call, then the owner is free to claim again.
		made := counted(t, ":memory:")
		entered, release := make(chan struct{}), make(chan struct{})
		made.db.setFaults(faults{hold: func(_ string, index int) {
			if index == 1 {
				close(entered)
				<-release
			}
		}})
		committed := make(chan error, 1)
		held := entryWrite(mintID[durable.EntryId](t, made.storage))
		go func() {
			_, err := commitWrites(made.storage, held)
			committed <- err
		}()
		<-entered
		closing := make(chan error, 1)
		go func() { closing <- made.storage.Close(background) }()
		select {
		case err := <-closing:
			t.Fatalf("Close returned with a commit underway: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		if _, err := Open(background, made.store, "harness"); err == nil {
			t.Fatal("the claims were released while a commit was underway")
		}
		close(release)
		must(t, <-committed)
		must(t, <-closing)
		value(Open(background, made.store, "harness"))
		must(t, made.store.Close(background))
	})
}

func TestStoreStorageSemantics(t *testing.T) {
	t.Run("a refused Durable commit rejects with Durable's message and keeps the store usable", func(t *testing.T) {
		store := memoryStore(t)
		storage := value(Open(background, store, "harness"))
		value(commitWrites(storage, rootWrite()))
		if _, err := commitWrites(storage, rootWrite()); err == nil || !strings.Contains(err.Error(), "ID 1 already belongs to conversation") {
			t.Fatalf("second root = %v", err)
		}
		entry := durable.EntryRecord{Id: mintID[durable.EntryId](t, storage), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "m"}
		value(commitWrites(storage, durable.EntryWrite{Value: entry}))
		found := value(storage.Entry(background, entry.Id))
		if found == nil || !reflect.DeepEqual(found.Entry, entry) {
			t.Fatalf("entry = %+v, want %+v", found, entry)
		}
		must(t, store.Close(background))
	})

	t.Run("raises the allocator above every id it holds when it opens (a store built by import)", func(t *testing.T) {
		store := memoryStore(t)
		storage := value(Open(background, store, "harness"))
		entry := durable.EntryRecord{Id: 500, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "m"}
		value(commitWrites(storage, rootWrite(), durable.EntryWrite{Value: entry}))
		if id := value(storage.MintId()); id != 501 {
			t.Fatalf("MintId = %d, want 501", id)
		}
		must(t, store.Close(background))
	})
}

// recorded is a store on `:memory:` with an outbox from its first commit, and Durable's storage in it.
func recorded(t *testing.T) (*chordstore.ChordStore, *StoreStorage) {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	must(t, err)
	outbox := true
	opened, err := chordstore.OpenStore(chordstore.SqliteBackend(db), background, chordstore.StoreOptions{Outbox: &outbox})
	must(t, err)
	storage := value(Open(background, opened, "harness"))
	value(commitWrites(storage, rootWrite()))
	return opened.(*chordstore.ChordStore), storage
}

func base(n float64) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("n", n)}
}

func deltaTo(n float64) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentDelta, Version: 1, Ops: []durable.Op{{"s", []any{"n"}, n}}}
}

var rootScope = durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID}

// history is rewindable history, a retired latest document, a create-plus-retire and a change-plus-retire.
func history(t *testing.T, storage durable.Storage) (rewindable durable.DocumentId, seqs []durable.Seq) {
	t.Helper()
	rewindable = mintID[durable.DocumentId](t, storage)
	latest := mintID[durable.DocumentId](t, storage)
	empty := mintID[durable.DocumentId](t, storage)
	seqs = append(seqs,
		value(commitWrites(storage,
			durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: rewindable, Kind: "r", Scope: rootScope, History: durable.HistoryRewindable, Fork: durable.ForkAsOf}, Content: base(0)},
			durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: latest, Kind: "l", Scope: rootScope, History: durable.HistoryLatest, Fork: durable.ForkCurrent}, Content: base(0)},
		)),
		value(commitWrites(storage, durable.DocumentChangeWrite{Id: rewindable, Content: deltaTo(1)})),
		value(commitWrites(storage,
			durable.DocumentChangeWrite{Id: latest, Content: deltaTo(2)},
			durable.DocumentRetireWrite{Id: latest},
			durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: empty, Kind: "e", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, Content: base(3)},
			durable.DocumentRetireWrite{Id: empty},
		)),
	)
	return rewindable, seqs
}

// exportedParts is every part of store's archive.
func exportedParts(t *testing.T, store chordstore.Store) []chordstore.ArchivePart {
	t.Helper()
	export, err := store.Export(background)
	must(t, err)
	var parts []chordstore.ArchivePart
	for {
		part, ok, err := export.Next(background)
		must(t, err)
		if !ok {
			return parts
		}
		parts = append(parts, part)
	}
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	text, err := jsstring.MarshalJSON(value)
	must(t, err)
	return string(text)
}

func newMemoryDatabase(t *testing.T) *node.NodeSqliteDatabase {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	must(t, err)
	return db
}

func TestStoreStorageArchives(t *testing.T) {
	t.Run("export and import keep rewindable history, retired records and Chord's own data: reads at a point agree", func(t *testing.T) {
		store, storage := recorded(t)
		rewindable, seqs := history(t, storage)
		// Chord's own data in the same store: an object's persisted document.
		persistMemo(t, store)
		parts := exportedParts(t, store)
		chordOwned := false
		for _, part := range parts {
			if create, ok := part.(*chordstore.DocumentCreate); ok && create.Record.Owner == "app.memo" {
				chordOwned = true
			}
		}
		if !chordOwned {
			t.Fatal("the archive holds no document of app.memo")
		}
		copied, err := chordstore.ImportStore(chordstore.SqliteBackend(newMemoryDatabase(t)), chordstore.SlicePartSource(parts), background, chordstore.StoreOptions{})
		must(t, err)
		imported := value(Open(background, copied, "harness"))
		for _, at := range []durable.DocumentPoint{durable.AtSeq(seqs[0]), durable.AtSeq(seqs[1]), durable.CurrentPoint} {
			want := value(storage.Document(background, rewindable, at))
			got := value(imported.Document(background, rewindable, at))
			if jsonOf(t, got) != jsonOf(t, want) {
				t.Fatalf("document at %+v = %s, want %s", at, jsonOf(t, got), jsonOf(t, want))
			}
		}
		query := durable.DocumentQuery{Scope: rootScope, At: durable.AtSeq(seqs[1])}
		wantPage := value(storage.ScanDocuments(background, query, 10, nil))
		gotPage := value(imported.ScanDocuments(background, query, 10, nil))
		if jsonOf(t, gotPage) != jsonOf(t, wantPage) {
			t.Fatalf("scan = %s, want %s", jsonOf(t, gotPage), jsonOf(t, wantPage))
		}
		again := exportedParts(t, copied)
		if jsonOf(t, again) != jsonOf(t, parts) {
			t.Fatal("the imported store exports another archive")
		}
		if got, want := value(imported.MintId()), value(storage.MintId()); got != want {
			t.Fatalf("MintId = %d, want %d", got, want)
		}
		must(t, copied.Close(background))
		must(t, store.Close(background))
	})

	t.Run("an outbox of Durable commits replayed into an empty store gives the same archive (create-plus-retire included)", func(t *testing.T) {
		store, storage := recorded(t)
		history(t, storage)
		commits, err := store.Outbox().Read(0, 1000, background)
		must(t, err)
		replica, err := chordstore.OpenStore(chordstore.SqliteBackend(newMemoryDatabase(t)), background, chordstore.StoreOptions{})
		must(t, err)
		must(t, chordstore.ApplyBatch(replica.(*chordstore.ChordStore), commits, background))
		if jsonOf(t, exportedParts(t, replica)) != jsonOf(t, exportedParts(t, store)) {
			t.Fatal("the replica exports another archive")
		}
		must(t, replica.Close(background))
		must(t, store.Close(background))
	})
}

func TestStoreStorageHeadMarkers(t *testing.T) {
	t.Run("a head entry is found after reopen, in an imported archive and in a replayed outbox", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "store.sqlite")
		open := func() *chordstore.ChordStore {
			db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
			must(t, err)
			outbox := true
			opened, err := chordstore.OpenStore(chordstore.SqliteBackend(db), background, chordstore.StoreOptions{Outbox: &outbox})
			must(t, err)
			return opened.(*chordstore.ChordStore)
		}
		store := open()
		storage := value(Open(background, store, "harness"))
		value(commitWrites(storage, rootWrite()))
		entry := func(head *durable.EntryId) durable.EntryId {
			id := mintID[durable.EntryId](t, storage)
			value(commitWrites(storage, durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "k", Data: delta.JsonObjectOf(), Head: head}}))
			return id
		}
		first := entry(nil)
		marker := entry(&first)
		entry(nil)
		// heads is the newest head marker, and the one at or before the first entry (none).
		heads := func(of durable.Storage) [2]durable.EntryId {
			var found [2]durable.EntryId
			if newest := value(of.FindLatestHeadMarker(background, durable.ROOT_CONVERSATION_ID, nil)); newest != nil {
				found[0] = newest.Id
			}
			if bounded := value(of.FindLatestHeadMarker(background, durable.ROOT_CONVERSATION_ID, &first)); bounded != nil {
				found[1] = bounded.Id
			}
			return found
		}
		want := [2]durable.EntryId{marker, 0}
		if got := heads(storage); got != want {
			t.Fatalf("heads = %v, want %v", got, want)
		}
		must(t, storage.Close(background))
		must(t, store.Close(background))
		store = open()
		storage = value(Open(background, store, "harness"))
		if got := heads(storage); got != want {
			t.Fatalf("heads after reopen = %v, want %v", got, want)
		}
		parts := exportedParts(t, store)
		var puts []*chordstore.PutWrite
		for _, part := range parts {
			if put, ok := part.(*chordstore.PutWrite); ok && put.Table == "harness:entry_heads" {
				puts = append(puts, put)
			}
		}
		if len(puts) != 1 || jsonOf(t, puts[0].Row) != jsonOf(t, delta.JsonObjectOf("id", float64(marker), "conversation_id", float64(durable.ROOT_CONVERSATION_ID))) {
			t.Fatalf("entry_heads puts = %s", jsonOf(t, puts))
		}
		copied, err := chordstore.ImportStore(chordstore.SqliteBackend(newMemoryDatabase(t)), chordstore.SlicePartSource(parts), background, chordstore.StoreOptions{})
		must(t, err)
		if got := heads(value(Open(background, copied, "harness"))); got != want {
			t.Fatalf("heads in the imported archive = %v, want %v", got, want)
		}
		replica, err := chordstore.OpenStore(chordstore.SqliteBackend(newMemoryDatabase(t)), background, chordstore.StoreOptions{})
		must(t, err)
		commits, err := store.Outbox().Read(0, 1000, background)
		must(t, err)
		must(t, chordstore.ApplyBatch(replica.(*chordstore.ChordStore), commits, background))
		if got := heads(value(Open(background, replica, "harness"))); got != want {
			t.Fatalf("heads in the replayed outbox = %v, want %v", got, want)
		}
		for _, each := range []chordstore.Store{copied, replica, store} {
			must(t, each.Close(background))
		}
	})
}

func TestStoreStorageReads(t *testing.T) {
	t.Run("a request error inside a multi-query read rejects that read only; the store goes on", func(t *testing.T) {
		store, storage := recorded(t)
		_, err := storage.ScanEntries(background, durable.EntryQuery{ConversationId: 999}, 10, nil)
		if _, ok := errors.AsType[*durable.StorageRequestError](err); !ok {
			t.Fatalf("ScanEntries = %v, want a StorageRequestError", err)
		}
		if found := value(storage.Document(background, 999, durable.CurrentPoint)); found != nil {
			t.Fatalf("Document = %+v, want none", found)
		}
		if found := value(storage.Conversation(background, durable.ROOT_CONVERSATION_ID)); found == nil || found.Id != durable.ROOT_CONVERSATION_ID {
			t.Fatalf("Conversation = %+v", found)
		}
		must(t, store.Close(background))
	})
}

func TestStoreStorageCommits(t *testing.T) {
	t.Run("counts four statements for an entry append and for a delta, with a host attached", func(t *testing.T) {
		meter := newMeter(t)
		store := memoryStore(t).(*chordstore.ChordStore)
		must(t, store.Attach(chordstore.Attachment{Failed: func(error) {}, Batch: state.NewBatch(func(state.BatchError) {}, nil)}))
		storage := value(Open(background, store, "harness"))
		value(commitWrites(storage, rootWrite()))
		entry := durable.EntryRecord{Id: mintID[durable.EntryId](t, storage), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "m"}
		before := meter.statements()
		_, err := storage.Commit(meter.ctx, [][]durable.StorageWrite{{durable.EntryWrite{Value: entry}}}, func(int, durable.Seq) {})
		must(t, err)
		appended := meter.statements() - before
		id := mintID[durable.DocumentId](t, storage)
		value(commitWrites(storage, durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: id, Kind: "s", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, Content: base(0)}))
		changed := meter.statements()
		_, err = storage.Commit(meter.ctx, [][]durable.StorageWrite{{durable.DocumentChangeWrite{Id: id, Content: deltaTo(1)}}}, func(int, durable.Seq) {})
		must(t, err)
		if got := [2]float64{appended, meter.statements() - changed}; got != [2]float64{4, 4} {
			t.Fatalf("statements = %v, want [4 4]", got)
		}
		must(t, store.Close(background))
	})
}

func TestStoreStorageReviewRegressions(t *testing.T) {
	t.Run(`a head-marker entry whose kind is "refused" is returned, not taken for a request error`, func(t *testing.T) {
		storage := openStoreStorage(t, ":memory:")
		value(commitWrites(storage, rootWrite()))
		first := mintID[durable.EntryId](t, storage)
		marker := mintID[durable.EntryId](t, storage)
		value(commitWrites(storage,
			durable.EntryWrite{Value: durable.EntryRecord{Id: first, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "message"}},
			durable.EntryWrite{Value: durable.EntryRecord{Id: marker, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "refused", Head: &first}},
		))
		if found := value(storage.FindLatestHeadMarker(background, durable.ROOT_CONVERSATION_ID, nil)); found == nil || found.Id != marker {
			t.Fatalf("FindLatestHeadMarker = %+v, want %d", found, marker)
		}
		page := value(storage.ScanEntries(background, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 10, nil))
		var kinds []string
		for _, item := range page.Items {
			kinds = append(kinds, item.Kind)
		}
		if !reflect.DeepEqual(kinds, []string{"refused", "message"}) {
			t.Fatalf("kinds = %v", kinds)
		}
		must(t, storage.Close(background))
	})
}
