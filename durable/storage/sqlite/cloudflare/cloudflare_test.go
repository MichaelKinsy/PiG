// Ports packages/durable/test/sqlite-cloudflare.test.ts.

package cloudflare_test

// pi: packages/durable/src/storage/sqlite/cloudflare.ts

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

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/cloudflare"
)

// fakeStorage is a stand-in for a SQLite-backed Durable Object's `ctx.storage` over a native SQLite connection, with
// its value types: BLOB bindings and results are []byte, and Exec without bindings may run several statements.
type fakeStorage struct {
	conn *sql.Conn
	// lastBindings is what the latest statement with bindings received.
	lastBindings []cloudflare.DurableObjectSqlValue
}

type fakeCursor struct {
	rows []map[string]cloudflare.DurableObjectSqlValue
}

func (cursor fakeCursor) ToArray() ([]map[string]cloudflare.DurableObjectSqlValue, error) {
	return cursor.rows, nil
}

type fakeSql struct{ storage *fakeStorage }

func newFakeStorage(t *testing.T) *fakeStorage {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
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
	if len(bindings) == 0 && strings.Contains(strings.TrimRight(strings.TrimSpace(text), "; \t\r\n"), ";") {
		_, err := conn.ExecContext(context.Background(), text)
		return fakeCursor{}, err
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
	return fakeCursor{rows: found}, rows.Err()
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

// packages/durable/test/sqlite-cloudflare.test.ts:55
func TestDurableObjectStorageConformance(t *testing.T) {
	durabletest.RegisterStorageConformance(t, "SqliteStorage on a Durable Object", func(use func(durable.Storage) error) error {
		var objectStorage cloudflare.DurableObjectSqliteStorage = newFakeStorage(t)
		storage, err := cloudflare.OpenDurableObjectSqliteStorage(objectStorage)
		if err != nil {
			return err
		}
		return use(storage)
	})
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

func TestDurableObjectSqliteAdapter(t *testing.T) {
	// packages/durable/test/sqlite-cloudflare.test.ts:60
	t.Run("binds and returns BLOB values as []byte", func(t *testing.T) {
		var storage cloudflare.DurableObjectSqliteStorage = newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		check(t, database.Exec("CREATE TABLE blobs (id INTEGER PRIMARY KEY, data BLOB, n INTEGER)"))
		check(t, database.Run("INSERT INTO blobs (id, data, n) VALUES (?, ?, ?)", 1, []byte{1, 2, 3}, int64(7)))
		row := must(t, func() (durablesqlite.SqliteRow, error) {
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

	// packages/durable/test/sqlite-cloudflare.test.ts:70
	t.Run("rejects a bigint binding outside the safe integer range", func(t *testing.T) {
		database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
		check(t, database.Exec("CREATE TABLE numbers (n INTEGER)"))
		err := database.Run("INSERT INTO numbers (n) VALUES (?)", int64(9_007_199_254_740_993))
		if _, ok := errors.AsType[*cloudflare.RangeError](err); !ok {
			t.Fatalf("error %v, want a RangeError", err)
		}
		if rows := must(t, func() ([]durablesqlite.SqliteRow, error) { return database.All("SELECT n FROM numbers") }); len(rows) != 0 {
			t.Fatalf("rows %v after the rejected binding, want none", rows)
		}
	})

	// packages/durable/test/sqlite-cloudflare.test.ts:79
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
			check(t, database.Transaction(func(handle durablesqlite.SqliteExecutor) error {
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

	// packages/durable/test/sqlite-cloudflare.test.ts:95
	t.Run("rolls back a rejected transaction and rejects its handle afterwards", func(t *testing.T) {
		database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
		check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
		var handle durablesqlite.SqliteExecutor
		abort := errors.New("abort")
		err := database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
			handle = transaction
			if err := transaction.Run("INSERT INTO items (id) VALUES (?)", 1); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatalf("error %v, want the callback's", err)
		}
		if rows := must(t, func() ([]durablesqlite.SqliteRow, error) { return database.All("SELECT id FROM items") }); len(rows) != 0 {
			t.Fatalf("rows %v after rollback, want none", rows)
		}
		if err := handle.Run("INSERT INTO items (id) VALUES (?)", 2); err == nil || !strings.Contains(err.Error(), "no longer active") {
			t.Fatalf("a settled handle ran: %v", err)
		}
	})

	// packages/durable/test/sqlite-cloudflare.test.ts:110
	t.Run("queues operations behind an active transaction", func(t *testing.T) {
		database := cloudflare.NewDurableObjectSqliteDatabase(newFakeStorage(t))
		check(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"))
		var mu sync.Mutex
		var order []string
		record := func(entry string) { mu.Lock(); order = append(order, entry); mu.Unlock() }
		gate := make(chan struct{})
		inside := make(chan struct{})
		transactionDone := make(chan struct{})
		go func() {
			defer close(transactionDone)
			check(t, database.Transaction(func(handle durablesqlite.SqliteExecutor) error {
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
		readDone := make(chan []durablesqlite.SqliteRow)
		reading := make(chan struct{})
		go func() {
			close(reading)
			rows := must(t, func() ([]durablesqlite.SqliteRow, error) { return database.All("SELECT id FROM items") })
			record("read")
			readDone <- rows
		}()
		<-reading
		// The read must still be waiting: give an unqueued read every chance to finish first.
		for range 2000 {
			runtime.Gosched()
		}
		close(gate)
		<-transactionDone
		rows := <-readDone
		if len(rows) != 1 || rows[0]["id"] != int64(1) {
			t.Fatalf("rows %v, want the committed row", rows)
		}
		if !slices.Equal(order, []string{"transaction", "read"}) {
			t.Fatalf("order %v, want the transaction before the read", order)
		}
		check(t, database.Close())
	})
}
