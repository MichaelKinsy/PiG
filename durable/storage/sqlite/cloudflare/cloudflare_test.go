// Ports packages/durable/test/sqlite-cloudflare.test.ts.

package cloudflare_test

// pi: packages/durable/src/storage/sqlite/cloudflare.ts

import (
	"context"
	"database/sql"
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

// packages/durable/test/sqlite-cloudflare.test.ts:76 (Pi f7e3cb54 runs the suite on a prefixed Durable Object)
func TestDurableObjectStorageConformance(t *testing.T) {
	durabletest.RegisterStorageConformance(t, "SqliteStorage on a prefixed Durable Object", func(use func(durable.Storage) error) error {
		var objectStorage cloudflare.DurableObjectSqliteStorage = newFakeStorage(t)
		storage, err := cloudflare.OpenDurableObjectSqliteStorage(objectStorage, durablesqlite.SqliteStorageOptions{Prefix: new("pi_")})
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

	// Pi f7e3cb54 sqlite-cloudflare.test.ts:84
	t.Run("keeps schema names unprefixed by default", func(t *testing.T) {
		storage := newFakeStorage(t)
		_, err := cloudflare.OpenDurableObjectSqliteStorage(storage)
		check(t, err)
		names := schemaNames(t, storage)
		if !slices.Contains(names, "durable_schema") || slices.Contains(names, "pi_durable_schema") {
			t.Fatalf("names %v, want durable_schema and no pi_ prefix", names)
		}
		// Pi e17e2e10 sqlite-cloudflare.test.ts:18: the schema is at version 2, which admits sleeping tasks.
		if version := queryIds(t, storage, "SELECT version FROM durable_schema"); !slices.Equal(version, []int64{2}) {
			t.Fatalf("schema version %v, want 2", version)
		}
	})

	// upstream: packages/durable/test-workers/storage.test.ts:19 "migrates a version 1 schema to admit sleeping tasks, keeping tasks and indexes"
	t.Run("migrates a version 1 schema to admit sleeping tasks, keeping tasks and indexes", func(t *testing.T) {
		storage := newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		schema, err := durablesqlite.CreateSqliteSchema(new("legacy_"))
		check(t, err)
		tasks := schema.Tables.Tasks
		insert := func(id int, status string) error {
			return database.Run("INSERT INTO "+tasks+" (id, conversation_id, kind, status, abort_requested, background, record) VALUES (?, 1, 'test', ?, 0, 0, '{}')", int64(id), status)
		}
		// A database written by a version that knew schema version 1 only.
		check(t, durablesqlite.ApplySqliteMigrationsForSchema(database, schema, durablesqlite.CreateSqliteMigrations(schema)[:1]))
		check(t, insert(5, "waiting"))
		if err := insert(6, "sleeping"); err == nil {
			t.Fatal("version 1 admitted a sleeping task")
		}

		_, err = cloudflare.OpenDurableObjectSqliteStorage(storage, durablesqlite.SqliteStorageOptions{Prefix: new("legacy_")})
		check(t, err)
		if version := queryIds(t, storage, "SELECT version FROM "+schema.Tables.DurableSchema); !slices.Equal(version, []int64{2}) {
			t.Fatalf("schema version %v, want 2", version)
		}
		if ids := queryIds(t, storage, "SELECT id FROM "+tasks+" WHERE status = 'waiting'"); !slices.Equal(ids, []int64{5}) {
			t.Fatalf("waiting tasks %v, want [5]", ids)
		}
		check(t, insert(6, "sleeping"))
		rows, err := storage.conn.QueryContext(context.Background(), "SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? ORDER BY name", tasks)
		check(t, err)
		defer func() { _ = rows.Close() }()
		var indexes []string
		for rows.Next() {
			var name string
			check(t, rows.Scan(&name))
			indexes = append(indexes, name)
		}
		check(t, rows.Err())
		want := []string{schema.Indexes.TasksByAbortRequested, schema.Indexes.TasksByBackground, schema.Indexes.TasksByConversation, schema.Indexes.TasksByKind, schema.Indexes.TasksByStatus}
		if !slices.Equal(indexes, want) {
			t.Fatalf("task indexes %v, want %v", indexes, want)
		}
	})

	// Pi f7e3cb54 sqlite-cloudflare.test.ts:91
	t.Run("prefixes pi's tables and indexes without changing host tables", func(t *testing.T) {
		storage := newFakeStorage(t)
		database := cloudflare.NewDurableObjectSqliteDatabase(storage)
		check(t, database.Exec("CREATE TABLE host_notes (text TEXT)"))
		check(t, database.Exec("CREATE TABLE tasks (text TEXT)"))
		_, err := cloudflare.OpenDurableObjectSqliteStorage(storage, durablesqlite.SqliteStorageOptions{Prefix: new("pi_")})
		check(t, err)
		names := schemaNames(t, storage)
		if !slices.Contains(names, "host_notes") || !slices.Contains(names, "tasks") || slices.Contains(names, "pi_host_notes") {
			t.Fatalf("names %v, want the host tables unchanged", names)
		}
		var piNames []string
		for _, name := range names {
			if name != "host_notes" && name != "tasks" {
				piNames = append(piNames, name)
			}
		}
		if len(piNames) <= 10 {
			t.Fatalf("pi names %v, want more than 10", piNames)
		}
		for _, name := range piNames {
			if !strings.HasPrefix(name, "pi_") {
				t.Fatalf("name %s is not prefixed", name)
			}
		}
		if !slices.Contains(piNames, "pi_tasks") || !slices.Contains(piNames, "pi_durable_schema") {
			t.Fatalf("pi names %v lack pi_tasks or pi_durable_schema", piNames)
		}
	})

	// Pi f7e3cb54 sqlite-cloudflare.test.ts:112: two storages with their own prefixes open and commit at once on one
	// storage object; they share one queue, so their transactions never overlap.
	t.Run("isolates concurrently opened prefixes on one Durable Object", func(t *testing.T) {
		var objectStorage cloudflare.DurableObjectSqliteStorage = newFakeStorage(t)
		var first, second *durablesqlite.SqliteStorage
		var firstErr, secondErr error
		var opened sync.WaitGroup
		opened.Go(func() {
			first, firstErr = cloudflare.OpenDurableObjectSqliteStorage(objectStorage, durablesqlite.SqliteStorageOptions{Prefix: new("first_")})
		})
		opened.Go(func() {
			second, secondErr = cloudflare.OpenDurableObjectSqliteStorage(objectStorage, durablesqlite.SqliteStorageOptions{Prefix: new("second_")})
		})
		opened.Wait()
		check(t, firstErr)
		check(t, secondErr)
		entryId := durable.IdFromNumber[durable.EntryId](2)
		commitEntry := func(storage *durablesqlite.SqliteStorage, kind string) error {
			_, err := storage.Commit(context.Background(), []durable.StorageWrite{
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
				durable.EntryWrite{Value: durable.EntryRecord{Id: entryId, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: kind}},
			})
			return err
		}
		var committed sync.WaitGroup
		var firstCommit, secondCommit error
		committed.Go(func() { firstCommit = commitEntry(first, "first") })
		committed.Go(func() { secondCommit = commitEntry(second, "second") })
		committed.Wait()
		check(t, firstCommit)
		check(t, secondCommit)
		for storage, kind := range map[*durablesqlite.SqliteStorage]string{first: "first", second: "second"} {
			found := must(t, func() (*durable.EntryAt, error) { return storage.Entry(context.Background(), entryId) })
			if found == nil || found.Entry.Kind != kind {
				t.Fatalf("entry %v, want kind %s", found, kind)
			}
		}
		check(t, first.Close(context.Background()))
		check(t, second.Close(context.Background()))
	})

	// Pi f7e3cb54 sqlite-cloudflare.test.ts:141
	t.Run("rejects invalid and reserved prefixes", func(t *testing.T) {
		for _, tc := range []struct{ prefix, message string }{{"", `Invalid SQLite schema prefix ""`}, {"bad-prefix", `Invalid SQLite schema prefix "bad-prefix"`}, {"sqlite_pi_", `SQLite schema prefix "sqlite_pi_" is reserved`}} {
			_, err := cloudflare.OpenDurableObjectSqliteStorage(newFakeStorage(t), durablesqlite.SqliteStorageOptions{Prefix: new(tc.prefix)})
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Errorf("prefix %q: error %v, want one including %q", tc.prefix, err, tc.message)
			}
		}
	})

}

// schemaNames lists the tables and indexes of the fake's database, without SQLite's own.
func schemaNames(t *testing.T, storage *fakeStorage) []string {
	t.Helper()
	rows, err := storage.conn.QueryContext(context.Background(), "SELECT name FROM sqlite_master WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_%'")
	check(t, err)
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		check(t, rows.Scan(&name))
		names = append(names, name)
	}
	check(t, rows.Err())
	return names
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
