package workers

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports `memoryStore` and `holdNextCommit` of packages/chord/test/support.ts, which Pi's workers test imports from its shared
// test support. A copy, reduced to what the workers tests use, of chord/next/host/host_next_support_test.go as of commit 5b150924c
// (`faultyStore`: transactionGate, gatedExecutor, closesWrites and faultyDatabase's hold): that file is a _test file of another package
// (17 test files use it, 73 times), so it cannot be imported, and moving it would rewrite those tests. A change to the hold there is
// to be made here too.

// heldCommit is Pi's `{ entered, release }` of `holdNextCommit(store)`: Entered is closed when the held commit step reached its
// hold inside its backend transaction, Release lets it finish.
type heldCommit struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	freed   sync.Once
}

// hit holds the transaction until Release.
func (held *heldCommit) hit() {
	held.once.Do(func() { close(held.entered) })
	_, _ = chordctx.Receive(context.Background(), held.release)
}

// Entered is closed when the held transaction reached its hold.
func (held *heldCommit) Entered() <-chan struct{} { return held.entered }

// Release lets the held transaction go on.
func (held *heldCommit) Release() { held.freed.Do(func() { close(held.release) }) }

// closesWrites reports whether sql is a statement a commit step runs after all its writes: the catalog records, the outbox row
// and the metadata that close it.
func closesWrites(sql string) bool {
	return strings.HasPrefix(sql, "INSERT OR REPLACE INTO "+store.TableTables+" ") ||
		strings.HasPrefix(sql, "INSERT INTO "+store.TableOutbox+" ") ||
		strings.HasPrefix(sql, "UPDATE "+store.TableMeta+" ")
}

// holdingDatabase is a Node SQLite facade on `:memory:` that can hold the next commit step at the statement that follows its
// writes.
type holdingDatabase struct {
	sqlite.SqliteDatabase
	mu   sync.Mutex
	next *heldCommit
}

func (db *holdingDatabase) take() *heldCommit {
	db.mu.Lock()
	defer db.mu.Unlock()
	held := db.next
	db.next = nil
	return held
}

// Transaction is the facade's, with the hold.
func (db *holdingDatabase) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	return db.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		return callback(&holdingExecutor{SqliteExecutor: transaction, db: db})
	})
}

// holdingExecutor holds the commit step the database's pending hold is for at the statement that follows its writes.
type holdingExecutor struct {
	sqlite.SqliteExecutor
	db *holdingDatabase
}

func (e *holdingExecutor) hold(sql string) {
	if !closesWrites(sql) {
		return
	}
	if held := e.db.take(); held != nil {
		held.hit()
	}
}

func (e *holdingExecutor) Exec(sql string) error {
	e.hold(sql)
	return e.SqliteExecutor.Exec(sql)
}

func (e *holdingExecutor) Run(sql string, params ...sqlite.SqliteValue) error {
	e.hold(sql)
	return e.SqliteExecutor.Run(sql, params...)
}

func (e *holdingExecutor) RunCounted(sql string, params ...sqlite.SqliteValue) (int64, error) {
	e.hold(sql)
	return e.SqliteExecutor.RunCounted(sql, params...)
}

func (e *holdingExecutor) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	e.hold(sql)
	return e.SqliteExecutor.Get(sql, params...)
}

func (e *holdingExecutor) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	e.hold(sql)
	return e.SqliteExecutor.All(sql, params...)
}

// heldStore is a store on a fresh `:memory:` database (Pi's `memoryStore()`) and its `holdNextCommit`: the store's next commit
// step stops after its writes, inside its backend transaction, until the returned hold is released; later commits run as usual.
// The store is closed after the test, after what the test registered later (the fleet closes first).
func heldStore(t testing.TB) (*store.ChordStore, func() *heldCommit) {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatalf("open :memory:: %v", err)
	}
	db := &holdingDatabase{SqliteDatabase: database}
	opened, err := store.OpenStore(store.SqliteBackend(db), background, store.StoreOptions{})
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	chordStore := opened
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = chordStore.Close(ctx)
	})
	return chordStore, func() *heldCommit {
		held := &heldCommit{entered: make(chan struct{}), release: make(chan struct{})}
		db.mu.Lock()
		db.next = held
		db.mu.Unlock()
		// A test that fails while the commit is held must not hang its cleanup (the component's close waits for the commit).
		t.Cleanup(held.Release)
		return held
	}
}
