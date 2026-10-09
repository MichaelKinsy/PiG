package sqlite_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// Ports the SqliteExecutor contract of packages/durable/src/storage/sqlite/database.ts:11-16 against the Node adapter, on the
// database itself and on the handle a transaction passes its callback: exec runs several statements (database.ts:12), run and
// get and all take positional bindings (13-15), get yields no row for an empty result (14), and all yields every row in
// statement order, an empty list when nothing matches (15).
func TestSqliteExecutorExecRunGetAllOnDatabaseAndTransaction(t *testing.T) {
	database := openDatabase(t, databasePath(t))
	defer func() { mustDo(t, database.Close()) }()
	mustDo(t, database.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, score REAL, data BLOB); CREATE INDEX items_name ON items (name)"))

	check := func(t *testing.T, executor sqlite.SqliteExecutor, firstID int64) {
		t.Helper()
		mustDo(t, executor.Run("INSERT INTO items (id, name, score, data) VALUES (?, ?, ?, ?)", firstID, "a", 1.5, []byte{1, 2}))
		mustDo(t, executor.Run("INSERT INTO items (id, name, score, data) VALUES (?, ?, ?, ?)", firstID+1, "b", nil, nil))
		mustDo(t, executor.Run("INSERT INTO items (id, name) VALUES (?, ?)", firstID+2, "a"))

		rows := must(executor.All("SELECT id, name, score, data FROM items WHERE id >= ? ORDER BY id", firstID))
		expectEqual(t, rows, []sqlite.SqliteRow{
			{"id": firstID, "name": "a", "score": 1.5, "data": []byte{1, 2}},
			{"id": firstID + 1, "name": "b", "score": nil, "data": nil},
			{"id": firstID + 2, "name": "a", "score": nil, "data": nil},
		})
		named := must(executor.All("SELECT id FROM items WHERE name = ? AND id >= ? ORDER BY id", "a", firstID))
		expectEqual(t, named, []sqlite.SqliteRow{{"id": firstID}, {"id": firstID + 2}})
		none := must(executor.All("SELECT id FROM items WHERE name = ?", "missing"))
		if none == nil || len(none) != 0 {
			t.Fatalf("All with no match = %#v, want an empty list", none)
		}
		expectEqual(t, must(executor.Get("SELECT name FROM items WHERE id = ?", firstID+1)), sqlite.SqliteRow{"name": "b"})
		if row := must(executor.Get("SELECT name FROM items WHERE id = ?", -1)); row != nil {
			t.Fatalf("Get with no match = %#v, want nil", row)
		}
	}

	t.Run("database", func(t *testing.T) { check(t, database, 1) })
	t.Run("transaction handle", func(t *testing.T) {
		mustDo(t, database.Transaction(func(transaction sqlite.SqliteExecutor) error {
			check(t, transaction, 10)
			return nil
		}))
		expectEqual(t, must(database.Get("SELECT count(*) AS n FROM items")), sqlite.SqliteRow{"n": int64(6)})
	})
}

// Ports SqliteValue of packages/durable/src/storage/sqlite/database.ts:2 (`null | number | bigint | string | Uint8Array`):
// each member binds as a parameter through run and reads back as the matching column value. null is nil; number and bigint
// are int64 (a whole Go integer) or float64, with the bigint range kept exactly past 2^53; string is a string; Uint8Array is
// []byte.
func TestSqliteValueMembersBindAndReadBack(t *testing.T) {
	database := openDatabase(t, databasePath(t))
	defer func() { mustDo(t, database.Close()) }()
	mustDo(t, database.Exec("CREATE TABLE v (k INTEGER PRIMARY KEY, i INTEGER, r REAL, s TEXT, b BLOB)"))

	const beyondSafeInteger = int64(1<<53 + 1)
	cases := []struct {
		name  string
		bound sqlite.SqliteValue
		want  any
		col   string
	}{
		{"null", nil, nil, "s"},
		{"number integer", 7, int64(7), "i"},
		{"number float", 1.5, 1.5, "r"},
		{"bigint beyond 2^53", beyondSafeInteger, beyondSafeInteger, "i"},
		{"bigint max", int64(1<<63 - 1), int64(1<<63 - 1), "i"},
		{"string", "pi", "pi", "s"},
		{"Uint8Array", []byte{0, 255, 7}, []byte{0, 255, 7}, "b"},
	}
	for index, c := range cases {
		mustDo(t, database.Run("INSERT INTO v (k, "+c.col+") VALUES (?, ?)", int64(index), c.bound))
		expectEqual(t, must(database.Get("SELECT "+c.col+" AS value FROM v WHERE k = ?", int64(index))), sqlite.SqliteRow{"value": c.want})
	}
}
