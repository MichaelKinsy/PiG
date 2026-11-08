package node

// pi: packages/chord/src/sqlite/node.ts

// Ports the "read-only connections" case of packages/chord/test/sqlite/node.test.ts.

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// execRecorder is `vi.spyOn(DatabaseSync.prototype, "exec")` on one connection: the SQL of every exec, in order.
type execRecorder struct {
	connection
	sql []string
}

func (recorder *execRecorder) Exec(sqlText string) error {
	recorder.sql = append(recorder.sql, sqlText)
	return recorder.connection.Exec(sqlText)
}

func TestNodeSqliteReadOnlyConnections(t *testing.T) {
	t.Run("read a file another connection writes, in deferred transactions, and never write it", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "data.sqlite")
		writer, err := OpenNodeSqliteDatabase(file, NodeSqliteDatabaseOptions{})
		mustDo(t, err)
		mustDo(t, writer.Exec("CREATE TABLE probe (value INTEGER)"))
		mustDo(t, writer.Run("INSERT INTO probe VALUES (1)"))
		reader, err := OpenNodeSqliteDatabase(file, NodeSqliteDatabaseOptions{Mode: ReadOnly})
		mustDo(t, err)
		recorder := &execRecorder{connection: reader.database}
		reader.database = recorder
		// One snapshot per transaction: a write committed meanwhile shows in the next one.
		var seen [][]chordsqlite.SqliteRow
		mustDo(t, reader.Transaction(func(tx chordsqlite.SqliteExecutor) error {
			before, err := tx.All("SELECT value FROM probe")
			mustDo(t, err)
			mustDo(t, writer.Run("INSERT INTO probe VALUES (2)"))
			after, err := tx.All("SELECT value FROM probe")
			mustDo(t, err)
			seen = [][]chordsqlite.SqliteRow{before, after}
			return nil
		}))
		one := []chordsqlite.SqliteRow{{"value": int64(1)}}
		if !reflect.DeepEqual(seen, [][]chordsqlite.SqliteRow{one, one}) {
			t.Fatalf("snapshot reads = %v, want one snapshot twice", seen)
		}
		if want := []string{"BEGIN", "COMMIT"}; !reflect.DeepEqual(recorder.sql, want) {
			t.Fatalf("exec calls = %v, want %v", recorder.sql, want)
		}
		rows, err := reader.All("SELECT value FROM probe")
		mustDo(t, err)
		if want := []chordsqlite.SqliteRow{{"value": int64(1)}, {"value": int64(2)}}; !reflect.DeepEqual(rows, want) {
			t.Fatalf("rows = %v, want %v", rows, want)
		}
		if err := reader.Run("INSERT INTO probe VALUES (3)"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "readonly") {
			t.Fatalf("write on a read-only connection = %v, want a readonly refusal", err)
		}
		mustDo(t, reader.Close())
		mustDo(t, writer.Close())
		if _, err := OpenNodeSqliteDatabase(filepath.Join(filepath.Dir(file), "missing.sqlite"), NodeSqliteDatabaseOptions{Mode: ReadOnly}); err == nil {
			t.Fatal("a read-only open of a missing file succeeded")
		}
	})
}
