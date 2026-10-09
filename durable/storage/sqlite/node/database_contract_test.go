package node

import (
	"errors"
	"testing"

	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// upstream: packages/durable/test/sqlite-facade.test.ts "commits work done through the transaction handle and closes idempotently" and the SqliteDatabase contract (database.ts): a failed callback rolls the transaction back and Transaction returns that same error; Close is idempotent.
// Pi source: packages/durable/src/storage/sqlite/database.ts (SqliteDatabase)
// mutation-checked: skipping the rollback fails it
// mutation-checked: zeroing the results of SqliteDatabase.Transaction fails it
// Pi: packages/durable/src/storage/sqlite/database.ts:5 (transaction)
func TestSqliteDatabaseContractRollsBackAFailedTransactionAndClosesIdempotently(t *testing.T) {
	var database durablesqlite.SqliteDatabase = mustOpenMemory(t)
	mustDo(t, database.Exec("CREATE TABLE contract_probe (value INTEGER)"))
	failure := errors.New("callback failed")
	err := database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
		if runErr := transaction.Run("INSERT INTO contract_probe (value) VALUES (?)", 1); runErr != nil {
			return runErr
		}
		return failure
	})
	if err != failure { //nolint:errorlint // identity: Transaction must return the callback's own error value, not a wrapper or an equal error
		t.Fatalf("Transaction returned %v, want the callback's own error", err)
	}
	rows, err := database.All("SELECT value FROM contract_probe")
	mustDo(t, err)
	if len(rows) != 0 {
		t.Fatalf("rows after a rolled-back transaction: %v", rows)
	}
	mustDo(t, database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
		return transaction.Run("INSERT INTO contract_probe (value) VALUES (?)", 2)
	}))
	row, err := database.Get("SELECT value FROM contract_probe")
	mustDo(t, err)
	expectRow(t, row, durablesqlite.SqliteRow{"value": int64(2)})
	mustDo(t, database.Close())
	mustDo(t, database.Close())
}
