package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/support.ts (the store's helpers: memoryStore, faultyDatabase, faultyStore,
// holdingWrite, startQueued, tableOwner, tableIn, rejection, exported, metered).
//
// Shared test helpers of package store. Every test file of the package uses these and none redefines them.

// background is Pi's BACKGROUND_CONTEXT.
var background = chordctx.Background()

// openMemory opens a store on a fresh `:memory:` database through db (which wraps it for the fault helpers).
func openMemory(t testing.TB, wrap func(sqlite.SqliteDatabase) sqlite.SqliteDatabase, outbox []bool) *ChordStore {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatalf("open :memory:: %v", err)
	}
	var db sqlite.SqliteDatabase = database
	if wrap != nil {
		db = wrap(db)
	}
	options := StoreOptions{}
	if len(outbox) > 0 {
		options.Outbox = &outbox[0]
	}
	opened, err := OpenStore(SqliteBackend(db), background, options)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store := opened.(*ChordStore)
	t.Cleanup(func() {
		// The test's store is closed once it ended; a store a test closed already answers at once.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = store.Close(ctx)
	})
	return store
}

// memoryStore is a store on a fresh SQLite `:memory:` database, as Chord's layers see it; outbox turns the outbox on
// or off as StoreOptions.Outbox does.
func memoryStore(t testing.TB, outbox ...bool) *ChordStore {
	t.Helper()
	return openMemory(t, nil, outbox)
}

// The statements of the Node facade's transactions a test can make fail (TransactionStatement).
const (
	failBegin    = "BEGIN IMMEDIATE"
	failCommit   = "COMMIT"
	failRollback = "ROLLBACK"
)

// transactionGate holds a transaction inside its backend transaction, at its first statement.
type transactionGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	freed   sync.Once
}

func newTransactionGate() *transactionGate {
	return &transactionGate{entered: make(chan struct{}), release: make(chan struct{})}
}

// hit is a statement of the held transaction: the first one announces itself and waits for Release.
func (gate *transactionGate) hit() {
	gate.once.Do(func() { close(gate.entered) })
	<-gate.release
}

// Entered is closed once the held transaction ran its first statement.
func (gate *transactionGate) Entered() <-chan struct{} { return gate.entered }

// Release lets the held transaction go on; idempotent.
func (gate *transactionGate) Release() { gate.freed.Do(func() { close(gate.release) }) }

// gatedExecutor is a transaction handle whose first statement waits for its gate.
type gatedExecutor struct {
	sqlite.SqliteExecutor
	gate *transactionGate
}

func (executor *gatedExecutor) Exec(sql string) error {
	executor.gate.hit()
	return executor.SqliteExecutor.Exec(sql)
}

func (executor *gatedExecutor) Run(sql string, params ...sqlite.SqliteValue) error {
	executor.gate.hit()
	return executor.SqliteExecutor.Run(sql, params...)
}

func (executor *gatedExecutor) RunCounted(sql string, params ...sqlite.SqliteValue) (int64, error) {
	executor.gate.hit()
	return executor.SqliteExecutor.RunCounted(sql, params...)
}

func (executor *gatedExecutor) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	executor.gate.hit()
	return executor.SqliteExecutor.Get(sql, params...)
}

func (executor *gatedExecutor) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	executor.gate.hit()
	return executor.SqliteExecutor.All(sql, params...)
}

// testDatabase wraps a Node SQLite facade on `:memory:` whose next transaction statement a test can make fail (the
// D4 matrix: a failed `BEGIN`, `COMMIT` or `ROLLBACK`, as the real facade sees one) and whose next transaction a
// test can hold at its first statement.
// Go forced difference: Pi overrides `DatabaseSync.exec` under the facade; Go's facade runs its transaction
// statements on an unexported connection, so the wrapper injects the same failures around its Transaction: a failed
// `BEGIN IMMEDIATE` fails before the callback runs, a failed `COMMIT` fails after it succeeded (the facade rolls
// back), a failed `ROLLBACK` replaces the error the facade rolled back with by one that does not unwrap to it (the
// real facade's AggregateError).
type testDatabase struct {
	sqlite.SqliteDatabase
	mu      sync.Mutex
	failing map[string]bool
	gate    *transactionGate
}

// take reports whether statement was set to fail, and clears it (one failure per fail call).
func (db *testDatabase) take(statement string) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	failed := db.failing[statement]
	delete(db.failing, statement)
	return failed
}

func (db *testDatabase) fail(statement string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.failing[statement] = true
}

func (db *testDatabase) hold(gate *transactionGate) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.gate = gate
}

func (db *testDatabase) takeGate() *transactionGate {
	db.mu.Lock()
	defer db.mu.Unlock()
	gate := db.gate
	db.gate = nil
	return gate
}

// Transaction is the facade's, with the injected failures and the hold.
func (db *testDatabase) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	if db.take(failBegin) {
		return errors.New("injected: BEGIN IMMEDIATE failed")
	}
	gate := db.takeGate()
	err := db.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		if gate != nil {
			transaction = &gatedExecutor{SqliteExecutor: transaction, gate: gate}
		}
		if err := callback(transaction); err != nil {
			return err
		}
		if db.take(failCommit) {
			return errors.New("injected: COMMIT failed")
		}
		return nil
	})
	if err != nil && db.take(failRollback) {
		// A failed `ROLLBACK` here still rolled back, so the database stays usable for the rest of the test.
		return errors.New("injected: ROLLBACK failed")
	}
	return err
}

// faultyStore is a store over a testDatabase and its fail: the next `statement` (failBegin, failCommit or
// failRollback) of a transaction fails, once per call.
func faultyStore(t testing.TB, outbox ...bool) (*ChordStore, func(statement string)) {
	t.Helper()
	var db *testDatabase
	store := openMemory(t, func(inner sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		db = &testDatabase{SqliteDatabase: inner, failing: map[string]bool{}}
		return db
	}, outbox)
	return store, db.fail
}

// gatedStore is a store over a testDatabase and a hold: the next transaction (a commit step, a batch's apply)
// waits at its first statement for the returned gate's Release, after announcing itself on Entered.
func gatedStore(t testing.TB, outbox ...bool) (*ChordStore, func() *transactionGate) {
	t.Helper()
	var db *testDatabase
	store := openMemory(t, func(inner sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		db = &testDatabase{SqliteDatabase: inner, failing: map[string]bool{}}
		return db
	}, outbox)
	return store, func() *transactionGate {
		gate := newTransactionGate()
		db.hold(gate)
		return gate
	}
}

// stagedWrite is a StagedWrite from its order and its apply (Pi's `{ order, apply }` literal).
type stagedWrite struct {
	order WriteOrder
	apply func(step *Step) ([]StoreWrite, error)
}

func (write stagedWrite) StageOrder() WriteOrder { return write.order }

func (write stagedWrite) Apply(step *Step) ([]StoreWrite, error) { return write.apply(step) }

// holdingWrite is a staged write that holds its commit step inside its backend transaction: entered is closed when
// the step applies it, release lets the step finish. A test appends it to the writes of the commit it holds (Pi's
// `holdNextCommit` replaced `store.commit` for one call to append the same write); it records a `dropIndex` of a
// table the step does not have, which only the outbox sees.
func holdingWrite() (held StagedWrite, entered <-chan struct{}, release func()) {
	gate := make(chan struct{})
	reached := make(chan struct{})
	var once sync.Once
	held = stagedWrite{
		order: OrderWrite,
		apply: func(*Step) ([]StoreWrite, error) {
			close(reached)
			<-gate
			return []StoreWrite{&DropIndexWrite{Table: "x", Name: "y"}}, nil
		},
	}
	return held, reached, func() { once.Do(func() { close(gate) }) }
}

// queueTail is the store's commit queue tail: it changes whenever an entry takes its place.
func (store *ChordStore) queueTail() <-chan struct{} {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.tail
}

// startQueued runs call (a Commit, a Replay, an ApplyBatch) on a goroutine, as Pi starts a promise it awaits later, and
// returns its result channel once call has taken its place in store's commit queue (or settled without one: a
// refusal at the call). Entries started one after another through it queue in that order.
func startQueued[T any](t testing.TB, store *ChordStore, call func() (T, error)) <-chan chordctx.Settled[T] {
	t.Helper()
	before := store.queueTail()
	settled := make(chan chordctx.Settled[T], 1)
	go func() {
		value, err := call()
		settled <- chordctx.Settled[T]{Value: value, Err: err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for store.queueTail() == before {
		select {
		case outcome := <-settled:
			settled <- outcome
			return settled
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("a store call neither took its place in the commit queue nor settled")
		}
		time.Sleep(50 * time.Microsecond)
	}
	return settled
}

// rejected is the ChordError err is, failing the test when it is none or its code or reason differ; an empty reason
// expects none (Pi's `toMatchObject({ code, reason })`).
func rejected(t testing.TB, err error, code data.ChordErrorCode, reason data.ChordErrorReason) *data.ChordError {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok {
		t.Fatalf("expected a ChordError %s/%s, got %v", code, reason, err)
	}
	if chordErr.Code != code || (reason != "" && chordErr.Reason != reason) {
		t.Fatalf("expected %s/%s, got %s/%s: %s", code, reason, chordErr.Code, chordErr.Reason, chordErr.Message)
	}
	return chordErr
}

// metered is a telemetry adapter, a context counting through it, and the counters recorded so far, by name.
func metered(t testing.TB) *nexttest.Meter { return nexttest.Metered(t) }

// exported is every part of store's export.
func exported(t testing.TB, store Store, ctx context.Context) []ArchivePart {
	t.Helper()
	export, err := store.Export(ctx)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	var parts []ArchivePart
	for {
		part, ok, err := export.Next(ctx)
		if err != nil {
			t.Fatalf("export: %v", err)
		}
		if !ok {
			return parts
		}
		parts = append(parts, part)
	}
}

// testOwner is the owner of a test's table handles: its failures collected (Pi's `tableOwner()`).
type testOwner struct {
	failures []*data.ChordError
	scope    *struct{}
}

func (owner *testOwner) Label() string { return "app.store" }

func (owner *testOwner) Scope() any { return owner.scope }

func (owner *testOwner) AssertLive() error { return nil }

func (owner *testOwner) Fail(err *data.ChordError) { owner.failures = append(owner.failures, err) }

// tableOwner is a fresh owner for table handles; its failures are the ones its handles reported.
func tableOwner() *testOwner { return &testOwner{scope: new(struct{})} }

// tableIn creates definition (and its indexes) in store as `app.store:<name>` and returns a handle to it; owner is
// the handle's owner (a fresh one by default).
func tableIn(t testing.TB, store *ChordStore, definition *TableDefinition, owner ...*testOwner) *TableHandle {
	t.Helper()
	handleOwner := tableOwner()
	if len(owner) > 0 {
		handleOwner = owner[0]
	}
	table := "app.store:" + definition.Name
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}
	err := PrepareTables([]DeclaredTable{{Definition: definition, Table: table}}, store, fence, "app.store", background)
	if err != nil {
		t.Fatalf("PrepareTables: %v", err)
	}
	return NewTableHandle(definition, table, store, handleOwner)
}

// must fails the test when err is not nil.
func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%v", err)
	}
}
