package node

import (
	"errors"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Proof of NodeSqliteDatabase (node.ts): a transaction commits what its callback wrote and rolls back when it fails.

func TestNodeSqliteDatabaseTransactionCommitsAndRollsBack(t *testing.T) {
	var database *NodeSqliteDatabase
	database, err := OpenNodeSqliteDatabase(":memory:", NodeSqliteDatabaseOptions{})
	mustDo(t, err)
	defer func() { mustDo(t, database.Close()) }()
	mustDo(t, database.Run("CREATE TABLE t (n INTEGER)"))
	mustDo(t, database.Transaction(func(tx chordsqlite.SqliteExecutor) error { return tx.Run("INSERT INTO t VALUES (1)") }))
	boom := errors.New("boom")
	err = database.Transaction(func(tx chordsqlite.SqliteExecutor) error {
		if err := tx.Run("INSERT INTO t VALUES (2)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Transaction = %v, want the callback's error", err)
	}
	rows, err := database.All("SELECT n FROM t ORDER BY n")
	mustDo(t, err)
	if len(rows) != 1 || rows[0]["n"] != int64(1) {
		t.Fatalf("rows = %v, want only the committed 1", rows)
	}
}
