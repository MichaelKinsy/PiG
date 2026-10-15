package node

import (
	"errors"
	"testing"

	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// packages/durable/src/storage/sqlite/database.ts SqliteExecutor.all: every row with positional bindings, in statement order, through
// the database and through the transaction handle; a statement that yields no row gives an empty list and get gives none.
// mutation-checked: zeroing the results of SqliteExecutor.All fails it
// Pi: packages/durable/src/storage/sqlite/database.ts:7 (all)
// packages/durable/src/storage/sqlite/database.ts:15: all(sql, ...params) resolves every row; database.ts:36 transaction(callback) commits when the callback resolves.
func TestSqliteExecutorAllThroughDatabaseAndTransaction(t *testing.T) {
	opened := mustOpenMemory(t)
	var database durablesqlite.SqliteDatabase = opened
	t.Cleanup(func() { _ = database.Close() })
	var executor durablesqlite.SqliteExecutor = database
	mustDo(t, executor.Exec("CREATE TABLE numbers (value INTEGER NOT NULL)"))
	for _, value := range []int64{3, 1, 2} {
		mustDo(t, executor.Run("INSERT INTO numbers (value) VALUES (?)", value))
	}
	rows, err := executor.All("SELECT value FROM numbers WHERE value >= ? ORDER BY value", int64(2))
	mustDo(t, err)
	if got := values(rows); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("All with a binding = %v, want [2 3]", got)
	}
	rows, err = executor.All("SELECT value FROM numbers WHERE value > ?", int64(99))
	mustDo(t, err)
	if len(rows) != 0 {
		t.Fatalf("All with no match = %v, want no rows", rows)
	}
	found, err := executor.Get("SELECT value FROM numbers WHERE value > ? ORDER BY value", int64(2))
	mustDo(t, err)
	if found == nil {
		t.Fatal("Get with a match returns the first row")
	}
	row, err := executor.Get("SELECT value FROM numbers WHERE value > ?", int64(99))
	mustDo(t, err)
	if row != nil {
		t.Fatalf("Get with no match = %v, want nil", row)
	}
	mustDo(t, database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
		if err := transaction.Run("INSERT INTO numbers (value) VALUES (?)", int64(4)); err != nil {
			return err
		}
		inside, err := transaction.All("SELECT value FROM numbers ORDER BY value")
		if err != nil {
			return err
		}
		if got := values(inside); len(got) != 4 || got[3] != 4 {
			t.Errorf("All inside the transaction = %v, want the uncommitted row visible", got)
		}
		return nil
	}))
	// packages/durable/src/storage/sqlite/database.ts:23-36: a callback that rejects rolls the transaction back before the rejection reaches the caller.
	rollback := errors.New("rollback")
	if err := database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
		if err := transaction.Run("INSERT INTO numbers (value) VALUES (?)", int64(99)); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("Transaction error = %v, want the callback's error", err)
	}
	rows, err = executor.All("SELECT value FROM numbers WHERE value = ?", int64(99))
	mustDo(t, err)
	if len(rows) != 0 {
		t.Fatalf("a rolled back insert is visible: %v", rows)
	}
	// database.ts:27-28: close ends the database; later work fails.
	mustDo(t, database.Close())
	if _, err := executor.All("SELECT value FROM numbers"); err == nil {
		t.Fatal("a closed database must fail")
	}
}
