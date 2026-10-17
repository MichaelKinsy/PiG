package cloudflare_test

import (
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/cloudflare"
)

// Pi packages/durable/src/storage/sqlite/cloudflare.ts:36-43 bindValue: a bigint binding is exact between Number.MIN_SAFE_INTEGER and Number.MAX_SAFE_INTEGER (±(2^53-1)) and a RangeError beyond; the check applies
// to both Go integer kinds that carry a bigint or number.
func TestDurableObjectSqliteDatabaseBindsIntegersExactlyWithinTheSafeRange(t *testing.T) {
	database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
	if _, ok := any(database).(*cloudflare.DurableObjectSqliteDatabase); !ok {
		t.Fatalf("NewDurableObjectSqliteDatabase returned %T", database)
	}
	check(t, database.Exec("CREATE TABLE numbers (n INTEGER)"))
	const safe = 1<<53 - 1
	for _, accepted := range []any{int64(safe), int64(-safe), safe, -safe, int64(0)} {
		if err := database.Run("INSERT INTO numbers (n) VALUES (?)", accepted); err != nil {
			t.Errorf("binding %v = %v, want it accepted", accepted, err)
		}
	}
	for _, rejected := range []any{int64(safe + 1), int64(-safe - 1), safe + 1, -safe - 1} {
		err := database.Run("INSERT INTO numbers (n) VALUES (?)", rejected)
		if rangeError, ok := errors.AsType[*cloudflare.RangeError](err); !ok || !strings.Contains(rangeError.Error(), "without losing precision") {
			t.Errorf("binding %v = %v, want a RangeError", rejected, err)
		}
	}
	// A rejected binding rejects the read too: get and all bind their parameters like run.
	_, getErr := database.Get("SELECT n FROM numbers WHERE n = ?", int64(safe+1))
	if _, ok := errors.AsType[*cloudflare.RangeError](getErr); !ok {
		t.Errorf("Get of an unsafe integer = %v, want a RangeError", getErr)
	}
	_, allErr := database.All("SELECT n FROM numbers WHERE n = ?", int64(safe+1))
	if _, ok := errors.AsType[*cloudflare.RangeError](allErr); !ok {
		t.Errorf("All of an unsafe integer = %v, want a RangeError", allErr)
	}
}

// cloudflare.ts:79-85 get/all: get returns the first row of the result or undefined, all returns every row, and a failing cursor rejects.
func TestDurableObjectSqliteDatabaseGetAndAllReadTheCursor(t *testing.T) {
	database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
	check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"))
	check(t, database.Exec("INSERT INTO items VALUES (1, 'a'); INSERT INTO items VALUES (2, 'b'); INSERT INTO items VALUES (3, 'c')"))
	first := must(t, func() (chordsqlite.SqliteRow, error) { return database.Get("SELECT id, name FROM items ORDER BY id") })
	if first["id"] != int64(1) || first["name"] != "a" {
		t.Fatalf("Get = %v, want the first row", first)
	}
	if none := must(t, func() (chordsqlite.SqliteRow, error) { return database.Get("SELECT id FROM items WHERE id > ?", 9) }); none != nil {
		t.Fatalf("Get of no rows = %v, want nil", none)
	}
	all := must(t, func() ([]chordsqlite.SqliteRow, error) { return database.All("SELECT id FROM items ORDER BY id") })
	if len(all) != 3 || all[0]["id"] != int64(1) || all[2]["id"] != int64(3) {
		t.Fatalf("All = %v, want three rows in order", all)
	}
	boom := errors.New("cursor failed")
	failing := cloudflare.NewDurableObjectSqliteDatabase(failingCursorStorage{err: boom})
	if _, err := failing.All("SELECT 1"); !errors.Is(err, boom) {
		t.Errorf("All with a failing cursor = %v, want its error", err)
	}
	if _, err := failing.Get("SELECT 1"); !errors.Is(err, boom) {
		t.Errorf("Get with a failing cursor = %v, want its error", err)
	}
}

type failingCursorStorage struct{ err error }

func (storage failingCursorStorage) Sql() cloudflare.DurableObjectSql { return failingSql(storage) }
func (storage failingCursorStorage) Transaction(closure func() error) error {
	return closure()
}

type failingSql struct{ err error }

func (query failingSql) Exec(string, ...cloudflare.DurableObjectSqlValue) (cloudflare.DurableObjectSqlCursor, error) {
	return failingCursor(query), nil
}

type failingCursor struct{ err error }

func (cursor failingCursor) Next() (map[string]cloudflare.DurableObjectSqlValue, bool, error) {
	return nil, false, cursor.err
}

func (cursor failingCursor) ToArray() ([]map[string]cloudflare.DurableObjectSqlValue, error) {
	return nil, cursor.err
}

// cloudflare.ts:45-51 and :55-69 transaction: every operation of the database (exec, run, get, all, close) waits in the serial queue behind an active transaction, and the transaction's handle rejects exec, run, get and all once the callback settles.
func TestDurableObjectSqliteDatabaseQueuesEveryOperationBehindATransaction(t *testing.T) {
	operations := map[string]func(database *cloudflare.DurableObjectSqliteDatabase) error{
		"exec": func(database *cloudflare.DurableObjectSqliteDatabase) error { return database.Exec("SELECT 1") },
		"run": func(database *cloudflare.DurableObjectSqliteDatabase) error {
			return database.Run("INSERT INTO items (id) VALUES (?)", 7)
		},
		"get": func(database *cloudflare.DurableObjectSqliteDatabase) error {
			_, err := database.Get("SELECT id FROM items")
			return err
		},
		"all": func(database *cloudflare.DurableObjectSqliteDatabase) error {
			_, err := database.All("SELECT id FROM items")
			return err
		},
		"close": func(database *cloudflare.DurableObjectSqliteDatabase) error { return database.Close() },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
			check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
			gate := make(chan struct{})
			inside := make(chan struct{})
			transactionDone := make(chan struct{})
			go func() {
				defer close(transactionDone)
				check(t, database.Transaction(func(handle chordsqlite.SqliteExecutor) error {
					close(inside)
					<-gate
					return nil
				}))
			}()
			<-inside
			var finished atomic.Bool
			operationDone := make(chan error, 1)
			go func() {
				err := operation(database)
				finished.Store(true)
				operationDone <- err
			}()
			for range 2000 {
				runtime.Gosched()
			}
			if finished.Load() {
				t.Fatalf("%s completed while a transaction was active", name)
			}
			close(gate)
			<-transactionDone
			if err := <-operationDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDurableObjectSqliteTransactionHandleRejectsEveryOperationAfterTheCallbackSettles(t *testing.T) {
	database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
	check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
	var handle chordsqlite.SqliteExecutor
	ran := false
	check(t, database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
		handle = transaction
		ran = true
		return transaction.Run("INSERT INTO items (id) VALUES (?)", 5)
	}))
	if !ran {
		t.Fatal("the transaction callback did not run")
	}
	if rows := must(t, func() ([]chordsqlite.SqliteRow, error) { return database.All("SELECT id FROM items") }); len(rows) != 1 {
		t.Fatalf("rows %v, want the callback's insert committed", rows)
	}
	inactive := func(err error) bool { return err != nil && strings.Contains(err.Error(), "no longer active") }
	if !inactive(handle.Exec("SELECT 1")) {
		t.Error("a settled handle ran Exec")
	}
	if _, err := handle.Get("SELECT 1"); !inactive(err) {
		t.Errorf("a settled handle ran Get: %v", err)
	}
	if _, err := handle.All("SELECT 1"); !inactive(err) {
		t.Errorf("a settled handle ran All: %v", err)
	}
	// A database outside a transaction is always active.
	check(t, database.Exec("SELECT 1"))
}
