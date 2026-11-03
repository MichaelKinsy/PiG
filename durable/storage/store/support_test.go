package store

// Ports packages/durable/test/store-storage-support.ts and test/store-storage-node.ts
//
// StoreStorage over a SQLite file for tests: a store opened on the file, Durable's storage in it under a test owner
// name, and a close that closes the store too (the app's part: in tests the storage is the store's only owner).
//
// The harness test files that take a SQLite storage re-run on the store in Pi through `vitest.store.config.ts`, which
// replaces `../src/storage/sqlite/node.ts` by `test/store-storage-node.ts`. The Go opener of that file is
// OpenNodeSqliteStorage below; what would re-run `durable/harness`'s tests over it is in the slice's rows.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/telemetry"
)

var background = chordctx.Background()

// testOwner is the owner name the test storages use.
const testOwner = "durable.test"

// ownedStorage is a StoreStorage whose Close closes its store too.
type ownedStorage struct {
	*StoreStorage
	store chordstore.Store
}

// Close closes the storage, then its store (idempotent).
func (owned *ownedStorage) Close(ctx context.Context) error {
	if err := owned.StoreStorage.Close(ctx); err != nil {
		return err
	}
	return owned.store.Close(ctx)
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// value is got, or a panic with err: the setup calls that must succeed (a panic fails the run with the error).
func value[T any](got T, err error) T {
	if err != nil {
		panic(err)
	}
	return got
}

// openStoreStorage opens a StoreStorage on a store over the SQLite file at path (":memory:" too) whose Close closes
// the store too.
func openStoreStorage(t testing.TB, path string) *ownedStorage {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	must(t, err)
	store, err := chordstore.OpenStore(chordstore.SqliteBackend(db), background, chordstore.StoreOptions{})
	must(t, err)
	storage, err := Open(background, store, testOwner)
	if err != nil {
		_ = store.Close(background)
		t.Fatal(err)
	}
	return &ownedStorage{StoreStorage: storage, store: store}
}

// OpenNodeSqliteStorage is `openNodeSqliteStorage` of test/store-storage-node.ts: Durable's storage on Chord's store
// over the file at path, in the place of a SQLite storage (a table prefix becomes nothing: the owner name separates).
func OpenNodeSqliteStorage(t testing.TB, path string) durable.Storage {
	return openStoreStorage(t, path)
}

// memoryStore is a store on `:memory:`.
func memoryStore(t testing.TB) chordstore.Store {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	must(t, err)
	store, err := chordstore.OpenStore(chordstore.SqliteBackend(db), background, chordstore.StoreOptions{})
	must(t, err)
	return store
}

// faults is what the next backend transaction does besides its work: commit fails it after its work (a failed COMMIT,
// an unknown outcome); fail fails a statement it matches (by its SQL and its index, from 1), as a backend error; hold
// holds a statement until it returns.
type faults struct {
	commit bool
	fail   func(sql string, index int) bool
	hold   func(sql string, index int)
}

// counts are a backend's transactions and the statements of the last one.
type counts struct {
	mu           sync.Mutex
	transactions int
	statements   int
}

func (counted *counts) snapshot() (transactions, statements int) {
	counted.mu.Lock()
	defer counted.mu.Unlock()
	return counted.transactions, counted.statements
}

// countingDatabase counts its transactions and the statements of the last one, and takes faults for its next
// transaction.
type countingDatabase struct {
	sqlite.SqliteDatabase
	counts *counts
	mu     sync.Mutex
	next   faults
}

func (db *countingDatabase) setFaults(next faults) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.next = next
}

func (db *countingDatabase) Transaction(work func(sqlite.SqliteExecutor) error) error {
	db.counts.mu.Lock()
	db.counts.transactions++
	db.counts.statements = 0
	db.counts.mu.Unlock()
	db.mu.Lock()
	taken := db.next
	db.next = faults{}
	db.mu.Unlock()
	return db.SqliteDatabase.Transaction(func(executor sqlite.SqliteExecutor) error {
		if err := work(&faultyExecutor{SqliteExecutor: executor, counts: db.counts, faults: taken}); err != nil {
			return err
		}
		if taken.commit {
			return errors.New("COMMIT failed")
		}
		return nil
	})
}

type faultyExecutor struct {
	sqlite.SqliteExecutor
	counts *counts
	faults faults
}

func (executor *faultyExecutor) call(sql string) error {
	executor.counts.mu.Lock()
	executor.counts.statements++
	index := executor.counts.statements
	executor.counts.mu.Unlock()
	if executor.faults.hold != nil {
		executor.faults.hold(sql, index)
	}
	if executor.faults.fail != nil && executor.faults.fail(sql, index) {
		return fmt.Errorf("injected: statement %d failed", index)
	}
	return nil
}

func (executor *faultyExecutor) Exec(sql string) error {
	if err := executor.call(sql); err != nil {
		return err
	}
	return executor.SqliteExecutor.Exec(sql)
}

func (executor *faultyExecutor) Run(sql string, params ...sqlite.SqliteValue) error {
	if err := executor.call(sql); err != nil {
		return err
	}
	return executor.SqliteExecutor.Run(sql, params...)
}

func (executor *faultyExecutor) RunCounted(sql string, params ...sqlite.SqliteValue) (int64, error) {
	if err := executor.call(sql); err != nil {
		return 0, err
	}
	return executor.SqliteExecutor.RunCounted(sql, params...)
}

func (executor *faultyExecutor) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	if err := executor.call(sql); err != nil {
		return nil, err
	}
	return executor.SqliteExecutor.Get(sql, params...)
}

func (executor *faultyExecutor) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	if err := executor.call(sql); err != nil {
		return nil, err
	}
	return executor.SqliteExecutor.All(sql, params...)
}

// rig is a storage with its root conversation, over a store with an outbox on the SQLite file at path, whose
// backend counts its transactions and the statements of the last one, and takes faults for its next transaction.
type rig struct {
	store   *chordstore.ChordStore
	storage *StoreStorage
	counts  *counts
	db      *countingDatabase
}

// counted opens a rig on path (":memory:" for none).
func counted(t testing.TB, path string) *rig {
	t.Helper()
	inner, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	must(t, err)
	db := &countingDatabase{SqliteDatabase: inner, counts: &counts{}}
	outbox := true
	opened, err := chordstore.OpenStore(chordstore.SqliteBackend(db), background, chordstore.StoreOptions{Outbox: &outbox})
	must(t, err)
	store := opened.(*chordstore.ChordStore)
	storage, err := Open(background, store, "harness")
	must(t, err)
	made := &rig{store: store, storage: storage, counts: db.counts, db: db}
	_, err = commitWrites(storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}})
	must(t, err)
	return made
}

// reopen is the same storage opened again on the file at path, as another process would.
func reopen(t testing.TB, path string) (*chordstore.ChordStore, *StoreStorage) {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	must(t, err)
	opened, err := chordstore.OpenStore(chordstore.SqliteBackend(db), background, chordstore.StoreOptions{})
	must(t, err)
	storage, err := Open(background, opened, "harness")
	must(t, err)
	return opened.(*chordstore.ChordStore), storage
}

// commitWrites is durabletest.CommitWrites on one batch: it returns the batch's seq, a refusal as the error.
func commitWrites(storage durable.Storage, writes ...durable.StorageWrite) (durable.Seq, error) {
	return durabletest.CommitWrites(background, storage, writes)
}

func entryWrite(id durable.EntryId, kind ...string) durable.StorageWrite {
	text := "m"
	if len(kind) > 0 {
		text = kind[0]
	}
	return durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: text}}
}

func mintID[I durable.Id](t testing.TB, storage durable.Storage) I {
	t.Helper()
	id, err := storage.MintId()
	must(t, err)
	return durable.IdFromNumber[I](id)
}

// meter is a telemetry context counting `chord.store.statements`.
type meter struct {
	telemetry *telemetry.InMemoryTelemetryContext
	ctx       context.Context
}

func newMeter(t testing.TB) *meter {
	t.Helper()
	recorder := telemetry.NewInMemoryTelemetryContext()
	var settled telemetry.TelemetrySpan
	must(t, recorder.StartSpan(telemetry.SpanOptions{Name: "test"}, func(span telemetry.TelemetrySpan) error {
		settled = span
		return nil
	}))
	return &meter{telemetry: recorder, ctx: chordctx.WithTelemetry(chordctx.Background(), settled)}
}

// statements is the sum of the `chord.store.statements` recorded so far.
func (counter *meter) statements() float64 {
	var total float64
	for _, measurement := range counter.telemetry.GetMetrics() {
		if measurement.Name == "chord.store.statements" {
			total += measurement.Value
		}
	}
	return total
}
