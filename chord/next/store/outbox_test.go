package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

// Ports packages/chord/test/next/store/outbox.test.ts: tests of the outbox (slice 6a §2; decisions "The outbox is
// optional, internal and versioned", small items 3-4): on, off and kept across opens, its commits in their recorded
// form, `trim` and its floor, and the format check.

var (
	outboxBaseValue = func() delta.JsonValue { return delta.JsonObjectOf("n", float64(0)) }
	outboxBase      = func() DocumentBase {
		return DocumentBase{Version: 1, Fingerprint: "0000000000000000", Value: outboxBaseValue()}
	}
)

// outboxCreated creates the singleton at the address (with key) in one commit.
func outboxCreated(t *testing.T, store *ChordStore, key string) {
	t.Helper()
	address := DocumentAddress{Namespace: "chord", Owner: "app.store", Kind: "notes", Key: &key}
	base := outboxBase()
	_, err := store.Commit([]StagedWrite{CreateDocument(address, base, engineJSON(t, base.Value), nil)}, openFence, background)
	noError(t, err)
}

// onFile is a store on a fresh file, reopened by the returned function (outbox nil: as persisted).
func onFile(t *testing.T) (path string, open func(outbox *bool) *ChordStore) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "store.sqlite")
	return path, func(outbox *bool) *ChordStore {
		t.Helper()
		db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
		noError(t, err)
		opened, err := OpenStore(SqliteBackend(db), background, StoreOptions{Outbox: outbox})
		noError(t, err)
		return opened.(*ChordStore)
	}
}

// hookedDatabase is a database whose Transaction and All a test can intercept (Pi reassigns `db.transaction` and
// `db.all`).
type hookedDatabase struct {
	sqlite.SqliteDatabase
	mu          sync.Mutex
	transaction func(inner func(callback func(sqlite.SqliteExecutor) error) error, callback func(sqlite.SqliteExecutor) error) error
	all         func(inner func(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error), sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error)
}

func (db *hookedDatabase) Transaction(callback func(sqlite.SqliteExecutor) error) error {
	db.mu.Lock()
	hook := db.transaction
	db.mu.Unlock()
	if hook == nil {
		return db.SqliteDatabase.Transaction(callback)
	}
	return hook(db.SqliteDatabase.Transaction, callback)
}

func (db *hookedDatabase) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	db.mu.Lock()
	hook := db.all
	db.mu.Unlock()
	if hook == nil {
		return db.SqliteDatabase.All(sql, params...)
	}
	return hook(db.SqliteDatabase.All, sql, params...)
}

// hookedStore is an in-memory store with the outbox on, over a hookedDatabase.
func hookedStore(t *testing.T) (*ChordStore, *hookedDatabase) {
	t.Helper()
	inner, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	noError(t, err)
	db := &hookedDatabase{SqliteDatabase: inner}
	opened, err := OpenStore(SqliteBackend(db), background, StoreOptions{Outbox: new(true)})
	noError(t, err)
	store := opened.(*ChordStore)
	t.Cleanup(func() { _ = store.Close(background) })
	return store, db
}

func outboxSeqs(commits []OutboxCommit) []int64 {
	seqs := []int64{}
	for _, commit := range commits {
		seqs = append(seqs, commit.Seq)
	}
	return seqs
}

func TestOutbox(t *testing.T) {
	t.Run("is off for a new store, on from the seq it is turned on at, kept when omitted, dropped when turned off", func(t *testing.T) {
		_, open := onFile(t)
		store := open(nil)
		outboxCreated(t, store, "")
		noError(t, store.Close(background))
		store = open(new(true))
		if store.Outbox().Floor() != 1 {
			t.Fatalf("floor = %d, want 1", store.Outbox().Floor())
		}
		const id = 2
		opsText := `[["s",["n"],2]]`
		change := ChangeDocument(id, 1, DocumentContent{Kind: "delta", Version: 1, Ops: opsOf(t, opsText)}, opsText)
		_, err := store.Commit([]StagedWrite{change}, openFence, background)
		noError(t, err)
		noError(t, store.Close(background))
		store = open(nil)
		commits, err := store.Outbox().Read(1, 10, background)
		noError(t, err)
		if len(commits) != 1 || commits[0].Seq != 2 || commits[0].NextID != 3 {
			t.Fatalf("commits = %+v", commits)
		}
		want := `[{"kind":"document.change","id":2,"revision":1,"content":{"kind":"delta","version":1,"ops":[["s",["n"],2]]}}]`
		if got := nexttest.JSONText(t, commits[0].Writes); got != want {
			t.Fatalf("writes = %s, want %s", got, want)
		}
		_, err = store.Outbox().Read(0, 10, background)
		_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		_, err = store.Outbox().Read(3, 10, background)
		_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		noError(t, store.Close(background))
		store = open(new(false))
		if store.Outbox() != nil {
			t.Fatalf("outbox = %v, want none", store.Outbox())
		}
		noError(t, store.Close(background))
	})

	t.Run("records creations with their ids and seqs; trim is idempotent and refuses beyond lastSeq", func(t *testing.T) {
		store := memoryStore(t, true)
		outboxCreated(t, store, "")
		commits, err := store.Outbox().Read(0, 10, background)
		noError(t, err)
		create, ok := commits[0].Writes[0].(*DocumentCreate)
		if !ok || create.Record.ID != 2 || create.Record.CreatedAt != 1 || create.Record.Namespace != "chord" ||
			create.Record.Owner != "app.store" || create.Record.Kind != "notes" || create.Record.Key == nil || *create.Record.Key != "" {
			t.Fatalf("write = %+v", commits[0].Writes[0])
		}
		if create.Base == nil || create.Base.Version != 1 || create.Base.Fingerprint != "0000000000000000" || engineJSON(t, create.Base.Value) != `{"n":0}` {
			t.Fatalf("base = %+v", create.Base)
		}
		noError(t, store.Outbox().Trim(1, background))
		noError(t, store.Outbox().Trim(1, background))
		if store.Outbox().Floor() != 1 {
			t.Fatalf("floor = %d, want 1", store.Outbox().Floor())
		}
		commits, err = store.Outbox().Read(1, 10, background)
		noError(t, err)
		if len(commits) != 0 {
			t.Fatalf("commits after the floor = %+v", commits)
		}
		_ = rejected(t, store.Outbox().Trim(2, background), data.CodeInvalid, data.ReasonArgs)
	})

	t.Run("overlapping trims run one at a time: the stored floor never moves back and equals the one in memory", func(t *testing.T) {
		_, open := onFile(t)
		store := open(new(true))
		for i := range 11 {
			outboxCreated(t, store, "k"+string(rune('a'+i)))
		}
		var wait sync.WaitGroup
		for _, through := range []int64{9, 3} {
			wait.Go(func() {
				noError(t, store.Outbox().Trim(through, background))
			})
		}
		wait.Wait()
		if store.Outbox().Floor() != 9 {
			t.Fatalf("floor = %d, want 9", store.Outbox().Floor())
		}
		noError(t, store.Close(background))
		store = open(nil)
		if store.Outbox().Floor() != 9 {
			t.Fatalf("stored floor = %d, want 9", store.Outbox().Floor())
		}
		_, err := store.Outbox().Read(3, 10, background)
		_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		commits, err := store.Outbox().Read(9, 10, background)
		noError(t, err)
		if got := outboxSeqs(commits); len(got) != 2 || got[0] != 10 || got[1] != 11 {
			t.Fatalf("seqs = %v, want [10 11]", got)
		}
		noError(t, store.Close(background))
	})

	t.Run("a read waits for a trim whose commit has not returned: the trimmed floor refuses it, never an empty page", func(t *testing.T) {
		store, db := hookedStore(t)
		outboxCreated(t, store, "")
		// The trim's DELETE committed; its transaction has not returned (the floor in memory is still 0).
		entered, gate := make(chan struct{}), make(chan struct{})
		db.mu.Lock()
		db.transaction = func(inner func(func(sqlite.SqliteExecutor) error) error, callback func(sqlite.SqliteExecutor) error) error {
			err := inner(callback)
			close(entered)
			<-gate
			return err
		}
		db.mu.Unlock()
		trimmed := make(chan error, 1)
		go func() { trimmed <- store.Outbox().Trim(1, background) }()
		<-entered
		db.mu.Lock()
		db.transaction = nil
		db.mu.Unlock()
		read := make(chan error, 1)
		go func() {
			_, err := store.Outbox().Read(0, 100, background)
			read <- err
		}()
		time.Sleep(20 * time.Millisecond)
		close(gate)
		noError(t, <-trimmed)
		_ = rejected(t, <-read, data.CodeInvalid, data.ReasonArgs)
	})

	t.Run("a trim waits for a read in flight: the read gets the commits it was admitted for", func(t *testing.T) {
		store, db := hookedStore(t)
		outboxCreated(t, store, "")
		entered, gate := make(chan struct{}), make(chan struct{})
		var once sync.Once
		db.mu.Lock()
		db.all = func(inner func(string, ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error), sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
			once.Do(func() { close(entered) })
			<-gate
			return inner(sql, params...)
		}
		db.mu.Unlock()
		type readResult struct {
			commits []OutboxCommit
			err     error
		}
		read := make(chan readResult, 1)
		go func() {
			commits, err := store.Outbox().Read(0, 100, background)
			read <- readResult{commits, err}
		}()
		<-entered
		db.mu.Lock()
		db.all = nil
		db.mu.Unlock()
		trimmed := make(chan error, 1)
		go func() { trimmed <- store.Outbox().Trim(1, background) }()
		time.Sleep(20 * time.Millisecond)
		close(gate)
		result := <-read
		noError(t, result.err)
		if got := outboxSeqs(result.commits); len(got) != 1 || got[0] != 1 {
			t.Fatalf("seqs = %v, want [1]", got)
		}
		noError(t, <-trimmed)
		if store.Outbox().Floor() != 1 {
			t.Fatalf("floor = %d, want 1", store.Outbox().Floor())
		}
	})

	t.Run("a commit stored in a newer format is refused unavailable; the data stays", func(t *testing.T) {
		path, open := onFile(t)
		store := open(new(true))
		outboxCreated(t, store, "")
		noError(t, store.Close(background))
		db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
		noError(t, err)
		noError(t, db.Run(`UPDATE `+TableOutbox+` SET format = ? WHERE seq = 1`, OutboxFormat+1))
		noError(t, db.Close())
		store = open(nil)
		for range 2 {
			_, err := store.Outbox().Read(0, 10, background)
			chordErr := rejected(t, err, data.CodeUnavailable, "")
			if !strings.Contains(chordErr.Message, "format 2") {
				t.Fatalf("message = %q, want format 2", chordErr.Message)
			}
		}
		noError(t, store.Close(context.Background()))
	})
}

// TestOutboxReadsStoredWritesAsJSONParse pins what Pi's outbox.ts does with a stored commit's writes text, which it
// never validates: `JSON.parse(writes) as StoreWrite[]` hands out any JSON as it parsed (an element that is no write, a
// value that is no array), and text that is no JSON rejects the read with the SyntaxError JSON.parse throws (V8's
// message, no ChordError); the throw is outside the store's read guard, so the store and the outbox go on.
func TestOutboxReadsStoredWritesAsJSONParse(t *testing.T) {
	// stored overwrites the writes text of commit seq.
	stored := func(t *testing.T, store *ChordStore, seq int64, text string) {
		t.Helper()
		noError(t, store.Maintain(func(executor sqlite.SqliteExecutor) error {
			return executor.Run(`UPDATE `+TableOutbox+` SET writes = ? WHERE seq = ?`, text, seq)
		}))
	}
	twoCommits := func(t *testing.T) *ChordStore {
		t.Helper()
		store := memoryStore(t, true)
		outboxCreated(t, store, "a")
		outboxCreated(t, store, "b")
		return store
	}

	t.Run("passes valid JSON through as it parsed: elements that are no write, a value that is no array", func(t *testing.T) {
		store := twoCommits(t)
		const first = `[{"kind":"nope","x":1},5,{"kind":"table"},{"kind":"document.create"},null]`
		stored(t, store, 1, first)
		stored(t, store, 2, `{"a":1}`)
		commits, err := store.Outbox().Read(0, 10, background)
		noError(t, err)
		if len(commits) != 2 {
			t.Fatalf("commits = %d, want 2", len(commits))
		}
		if got := nexttest.JSONText(t, commits[0].Raw); got != first {
			t.Fatalf("raw 0 = %s, want %s", got, first)
		}
		if got := nexttest.JSONText(t, commits[0].Writes); got != first || len(commits[0].Writes) != 5 {
			t.Fatalf("writes 0 = %s (%d), want %s", got, len(commits[0].Writes), first)
		}
		// An element's kind is its string `kind` member (the malformed table and document.create ones stay unknown).
		for index, kind := range []string{"nope", "", "table", "document.create", ""} {
			if got := commits[0].Writes[index].StoreWriteKind(); got != kind {
				t.Fatalf("writes[%d] kind = %q, want %q", index, got, kind)
			}
		}
		for index, write := range commits[0].Writes {
			if _, unknown := write.(*UnknownWrite); !unknown {
				t.Fatalf("writes[%d] = %T, want *UnknownWrite", index, write)
			}
		}
		if got := nexttest.JSONText(t, commits[1].Raw); got != `{"a":1}` || commits[1].Writes != nil {
			t.Fatalf("commit 1 raw = %s writes = %v, want {\"a\":1} and none", got, commits[1].Writes)
		}
	})

	t.Run("text that is no JSON rejects the read with JSON.parse's SyntaxError; the store and the outbox go on", func(t *testing.T) {
		store := twoCommits(t)
		stored(t, store, 1, `nope`)
		_, err := store.Outbox().Read(0, 10, background)
		if err == nil {
			t.Fatalf("Read of text that is no JSON succeeded")
		}
		if _, isChord := data.IsChordError(err); isChord {
			t.Fatalf("Read = %v, want a plain SyntaxError, not a ChordError", err)
		}
		want := jsonparse.Validate([]byte(`nope`))
		if want == nil || err.Error() != want.Error() {
			t.Fatalf("Read = %q, want JSON.parse's message %v", err.Error(), want)
		}
		// The throw is outside the store's read guard: it neither stopped the store nor blocked the outbox's queue.
		commits, err := store.Outbox().Read(1, 10, background)
		noError(t, err)
		if len(commits) != 1 || commits[0].Seq != 2 {
			t.Fatalf("commits after the bad one = %+v", commits)
		}
		stored(t, store, 1, `[]`)
		commits, err = store.Outbox().Read(0, 10, background)
		noError(t, err)
		if len(commits) != 2 {
			t.Fatalf("commits once repaired = %d, want 2", len(commits))
		}
	})
}
