package node

// pi: packages/chord/src/sqlite/node.ts

// Ports the "the outcome of a transaction (slice 6b D4)" cases of packages/chord/test/sqlite/node.test.ts (Pi 14b4c56e).

import (
	"errors"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// faultyConnection is node.test.ts `faulty`: a native connection whose next failing statement throws, and whose failing ROLLBACK still rolls back.
type faultyConnection struct {
	connection
	failing string
}

func (faulty *faultyConnection) Exec(sqlText string) error {
	if sqlText != faulty.failing {
		return faulty.connection.Exec(sqlText)
	}
	faulty.failing = ""
	if sqlText == "ROLLBACK" {
		if err := faulty.connection.Exec(sqlText); err != nil {
			return err
		}
	}
	return errors.New(sqlText + " failed")
}

func faulty(t *testing.T, statement string) *NodeSqliteDatabase {
	t.Helper()
	database, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return newNodeSqliteDatabase(&faultyConnection{connection: database, failing: statement})
}

func TestNodeSqliteTransactionOutcome(t *testing.T) {
	t.Run("rejects with the callback's own error after a rollback: the caller knows it never committed", func(t *testing.T) {
		database := faulty(t, "")
		own := errors.New("refused")
		mustDo(t, database.Exec("CREATE TABLE t (n INTEGER)"))
		err := database.Transaction(func(handle chordsqlite.SqliteExecutor) error {
			mustDo(t, handle.Run("INSERT INTO t VALUES (1)"))
			return own
		})
		if err != own { //nolint:errorlint // identity: the caller must get the callback's own error value
			t.Fatalf("error = %v, want the callback's own error", err)
		}
		rows, err := database.All("SELECT n FROM t")
		mustDo(t, err)
		if len(rows) != 0 {
			t.Fatalf("rows after a rolled-back transaction: %v", rows)
		}
	})

	for _, statement := range []string{"BEGIN IMMEDIATE", "COMMIT"} {
		t.Run("rejects with another error when "+statement+" fails", func(t *testing.T) {
			database := faulty(t, statement)
			err := database.Transaction(func(chordsqlite.SqliteExecutor) error { return nil })
			expectError(t, err, statement+" failed")
		})
	}

	t.Run("rejects with an AggregateError, not the callback's error, when its ROLLBACK fails", func(t *testing.T) {
		database := faulty(t, "ROLLBACK")
		own := errors.New("refused")
		err := database.Transaction(func(chordsqlite.SqliteExecutor) error { return own })
		if _, ok := errors.AsType[*AggregateError](err); !ok {
			t.Fatalf("error = %v, want an AggregateError", err)
		}
		if errors.Is(err, own) || err == own { //nolint:errorlint // identity: the aggregate must not be the callback's error
			t.Fatal("the aggregate is, or unwraps to, the callback's error")
		}
	})
}
