// Ports packages/chord/test/sqlite/cloudflare.test.ts.

package cloudflare_test

// pi: packages/chord/src/sqlite/cloudflare.ts

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	// Registers the pure-Go "sqlite" database/sql driver.
	_ "modernc.org/sqlite"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/cloudflare"
)

// fakeStorage is a stand-in for a SQLite-backed Durable Object's `ctx.storage` over a native SQLite connection, with
// its value types: BLOB bindings and results are []byte, and Exec without bindings may run several statements.
type fakeStorage struct {
	conn *sql.Conn
	// lastBindings is what the latest statement with bindings received.
	lastBindings []cloudflare.DurableObjectSqlValue
	// onCursorOperation, when set, sees every Next and ToArray a cursor serves (sqlite-cloudflare.test.ts onCursorOperation).
	onCursorOperation func(operation string)
	// onExec, when set, sees every statement as it runs.
	onExec func(text string)
}

// fakeCursor serves its rows once: Next takes one, ToArray the rest.
type fakeCursor struct {
	storage *fakeStorage
	rows    []map[string]cloudflare.DurableObjectSqlValue
	index   int
}

func (cursor *fakeCursor) Next() (map[string]cloudflare.DurableObjectSqlValue, bool, error) {
	if cursor.storage != nil && cursor.storage.onCursorOperation != nil {
		cursor.storage.onCursorOperation("next")
	}
	if cursor.index >= len(cursor.rows) {
		return nil, false, nil
	}
	cursor.index++
	return cursor.rows[cursor.index-1], true, nil
}

func (cursor *fakeCursor) ToArray() ([]map[string]cloudflare.DurableObjectSqlValue, error) {
	if cursor.storage != nil && cursor.storage.onCursorOperation != nil {
		cursor.storage.onCursorOperation("toArray")
	}
	remaining := cursor.rows[cursor.index:]
	cursor.index = len(cursor.rows)
	return remaining, nil
}

type fakeSql struct{ storage *fakeStorage }

func newFakeStorage(t *testing.T) *fakeStorage {
	t.Helper()
	return newFakeStorageOn(t, ":memory:")
}

// newFakeStorageOn is a fake over the SQLite database at dsn.
func newFakeStorageOn(t *testing.T, dsn string) *fakeStorage {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = db.Close() })
	return &fakeStorage{conn: conn}
}

func (storage *fakeStorage) Sql() cloudflare.DurableObjectSql { return fakeSql{storage} }

func (query fakeSql) Exec(text string, bindings ...cloudflare.DurableObjectSqlValue) (cloudflare.DurableObjectSqlCursor, error) {
	conn := query.storage.conn
	if len(bindings) > 0 {
		query.storage.lastBindings = bindings
	}
	if query.storage.onExec != nil {
		query.storage.onExec(text)
	}
	if len(bindings) == 0 && strings.Contains(strings.TrimRight(strings.TrimSpace(text), "; \t\r\n"), ";") {
		_, err := conn.ExecContext(context.Background(), text)
		return &fakeCursor{storage: query.storage}, err
	}
	rows, err := conn.QueryContext(context.Background(), text, bindings...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	found := []map[string]cloudflare.DurableObjectSqlValue{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := make(map[string]cloudflare.DurableObjectSqlValue, len(columns))
		for index, column := range columns {
			row[column] = values[index]
		}
		found = append(found, row)
	}
	return &fakeCursor{storage: query.storage, rows: found}, rows.Err()
}

func (storage *fakeStorage) Transaction(closure func() error) error {
	ctx := context.Background()
	if _, err := storage.conn.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	if err := closure(); err != nil {
		_, _ = storage.conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	_, err := storage.conn.ExecContext(ctx, "COMMIT")
	return err
}

// must returns the value of a call that also returns an error, failing the test on the error.
func must[T any](t *testing.T, call func() (T, error)) T {
	t.Helper()
	value, err := call()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestDurableObjectSqliteDriver(t *testing.T) {
	// packages/chord/test/sqlite/cloudflare.test.ts (moved from packages/durable/test/sqlite-cloudflare.test.ts in Pi c268154c), was durable:60
	t.Run("binds and returns BLOB values as []byte", func(t *testing.T) {
		var storage cloudflare.DurableObjectSqliteStorage = newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		check(t, database.Exec("CREATE TABLE blobs (id INTEGER PRIMARY KEY, data BLOB, n INTEGER)"))
		check(t, database.Run("INSERT INTO blobs (id, data, n) VALUES (?, ?, ?)", 1, []byte{1, 2, 3}, int64(7)))
		row := must(t, func() (chordsqlite.SqliteRow, error) {
			return database.Get("SELECT data, n FROM blobs WHERE id = ?", 1)
		})
		if !reflect.DeepEqual(row["data"], []byte{1, 2, 3}) || row["n"] != int64(7) {
			t.Fatalf("row %v, want data [1 2 3] and n 7", row)
		}
	})

	t.Run("hands the Durable Object its own copy of a BLOB binding", func(t *testing.T) {
		storage := newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		check(t, database.Exec("CREATE TABLE blobs (data BLOB)"))
		data := []byte{1, 2, 3}
		check(t, database.Run("INSERT INTO blobs (data) VALUES (?)", data))
		data[0] = 9
		if got := storage.lastBindings[0]; !reflect.DeepEqual(got, []byte{1, 2, 3}) {
			t.Fatalf("binding %v follows the caller's slice, want a copy [1 2 3]", got)
		}
	})

	// packages/chord/test/sqlite/cloudflare.test.ts (moved from packages/durable/test/sqlite-cloudflare.test.ts in Pi c268154c), was durable:70
	t.Run("rejects a bigint binding outside the safe integer range", func(t *testing.T) {
		database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
		check(t, database.Exec("CREATE TABLE numbers (n INTEGER)"))
		err := database.Run("INSERT INTO numbers (n) VALUES (?)", int64(9_007_199_254_740_993))
		if _, ok := errors.AsType[*cloudflare.RangeError](err); !ok {
			t.Fatalf("error %v, want a RangeError", err)
		}
		if rows := must(t, func() ([]chordsqlite.SqliteRow, error) { return database.All("SELECT n FROM numbers") }); len(rows) != 0 {
			t.Fatalf("rows %v after the rejected binding, want none", rows)
		}
	})

	// packages/chord/test/sqlite/cloudflare.test.ts (moved from packages/durable/test/sqlite-cloudflare.test.ts in Pi c268154c), was durable:79
	t.Run("closes after an active transaction settles", func(t *testing.T) {
		database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
		check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
		var mu sync.Mutex
		var order []string
		record := func(entry string) { mu.Lock(); order = append(order, entry); mu.Unlock() }
		gate := make(chan struct{})
		inside := make(chan struct{})
		var done sync.WaitGroup
		done.Add(2)
		go func() {
			defer done.Done()
			check(t, database.Transaction(func(handle chordsqlite.SqliteExecutor) error {
				if err := handle.Run("INSERT INTO items (id) VALUES (?)", 1); err != nil {
					return err
				}
				close(inside)
				<-gate
				record("transaction")
				return nil
			}))
		}()
		<-inside
		go func() {
			defer done.Done()
			check(t, database.Close())
			record("close")
		}()
		close(gate)
		done.Wait()
		if !slices.Equal(order, []string{"transaction", "close"}) {
			t.Fatalf("order %v, want the transaction before close", order)
		}
	})

	// packages/chord/test/sqlite/cloudflare.test.ts (moved from packages/durable/test/sqlite-cloudflare.test.ts in Pi c268154c), was durable:95
	t.Run("rolls back a rejected transaction and rejects its handle afterwards", func(t *testing.T) {
		database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
		check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
		var handle chordsqlite.SqliteExecutor
		abort := errors.New("abort")
		err := database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
			handle = transaction
			if err := transaction.Run("INSERT INTO items (id) VALUES (?)", 1); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatalf("error %v, want the callback's", err)
		}
		if rows := must(t, func() ([]chordsqlite.SqliteRow, error) { return database.All("SELECT id FROM items") }); len(rows) != 0 {
			t.Fatalf("rows %v after rollback, want none", rows)
		}
		if err := handle.Run("INSERT INTO items (id) VALUES (?)", 2); err == nil || !strings.Contains(err.Error(), "no longer active") {
			t.Fatalf("a settled handle ran: %v", err)
		}
	})

	// Pi f7e3cb54 sqlite-cloudflare.test.ts:217 replaces "queues operations behind an active transaction": the operations
	// come from another adapter of the same storage object, and the transaction rolls back.
	t.Run("runs operations from another adapter after a transaction, outside its rollback", func(t *testing.T) {
		var storage cloudflare.DurableObjectSqliteStorage = newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		other := cloudflare.NewDurableObjectSqliteDatabase(storage)
		check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
		gate := make(chan struct{})
		inside := make(chan struct{})
		abort := errors.New("abort")
		transactionDone := make(chan error, 1)
		go func() {
			transactionDone <- database.Transaction(func(handle chordsqlite.SqliteExecutor) error {
				if err := handle.Run("INSERT INTO items (id) VALUES (?)", 1); err != nil {
					return err
				}
				close(inside)
				<-gate
				return abort
			})
		}()
		<-inside
		type outcome struct {
			outsideErr, readErr error
			rows                []chordsqlite.SqliteRow
		}
		outcomes := make(chan outcome, 1)
		go func() {
			var result outcome
			result.outsideErr = other.Run("INSERT INTO items (id) VALUES (?)", 2)
			result.rows, result.readErr = other.All("SELECT id FROM items")
			outcomes <- result
		}()
		// The other adapter's calls must still be waiting: give an unqueued call every chance to run first.
		for range 2000 {
			runtime.Gosched()
		}
		close(gate)
		if err := <-transactionDone; !errors.Is(err, abort) {
			t.Fatalf("transaction error %v, want the callback's", err)
		}
		result := <-outcomes
		check(t, result.outsideErr)
		check(t, result.readErr)
		if len(result.rows) != 1 || result.rows[0]["id"] != int64(2) {
			t.Fatalf("rows %v, want only the other adapter's row", result.rows)
		}
		check(t, database.Close())
	})

	// Pi f7e3cb54 sqlite-cloudflare.test.ts:233: a direct call to the underlying storage bypasses the queue, so a
	// transaction rolls it back with its own writes.
	t.Run("includes direct underlying calls in an active transaction", func(t *testing.T) {
		storage := newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
		gate := make(chan struct{})
		opened := make(chan struct{})
		abort := errors.New("abort")
		transactionDone := make(chan error, 1)
		go func() {
			transactionDone <- database.Transaction(func(handle chordsqlite.SqliteExecutor) error {
				if err := handle.Run("INSERT INTO items (id) VALUES (?)", 1); err != nil {
					return err
				}
				close(opened)
				<-gate
				return abort
			})
		}()
		<-opened
		_, err := storage.Sql().Exec("INSERT INTO items (id) VALUES (?)", 2)
		check(t, err)
		close(gate)
		if err := <-transactionDone; !errors.Is(err, abort) {
			t.Fatalf("transaction error %v, want the callback's", err)
		}
		if ids := queryIds(t, storage, "SELECT id FROM items ORDER BY id"); len(ids) != 0 {
			t.Fatalf("rows %v remain, want the direct call rolled back with the transaction", ids)
		}
	})

	// Pi f7e3cb54 sqlite-cloudflare.test.ts:157
	t.Run("reads only the cursor's first row for get", func(t *testing.T) {
		storage := newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
		check(t, database.Run("INSERT INTO items (id) VALUES (1), (2)"))
		var operations []string
		storage.onCursorOperation = func(operation string) { operations = append(operations, operation) }
		row := must(t, func() (chordsqlite.SqliteRow, error) { return database.Get("SELECT id FROM items ORDER BY id") })
		if !reflect.DeepEqual(row, chordsqlite.SqliteRow{"id": int64(1)}) {
			t.Fatalf("row %v, want id 1", row)
		}
		if !slices.Equal(operations, []string{"next"}) {
			t.Fatalf("cursor operations %v, want [next]", operations)
		}
	})

	// Pi f7e3cb54 sqlite-cloudflare.test.ts:193 "runs an uncontended statement before returning its promise". Calls block
	// in Go, so the statement has run when Run returns; the queue takes no turn for it while it is idle.
	t.Run("runs an uncontended statement before returning", func(t *testing.T) {
		storage := newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
		check(t, database.Run("INSERT INTO items (id) VALUES (?)", 1))
		if ids := queryIds(t, storage, "SELECT id FROM items ORDER BY id"); !slices.Equal(ids, []int64{1}) {
			t.Fatalf("rows %v, want the written row", ids)
		}
	})
}

// queryIds reads the one integer column of every row a query returns, bypassing the adapter.
func queryIds(t *testing.T, storage *fakeStorage, query string) []int64 {
	t.Helper()
	rows, err := storage.conn.QueryContext(context.Background(), query)
	check(t, err)
	defer func() { _ = rows.Close() }()
	var values []int64
	for rows.Next() {
		var value int64
		check(t, rows.Scan(&value))
		values = append(values, value)
	}
	check(t, rows.Err())
	return values
}
