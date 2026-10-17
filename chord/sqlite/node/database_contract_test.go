package node

// pi: packages/durable/src/storage/sqlite/database.ts

import (
	"errors"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Pi 1.2.0 packages/durable/test/sqlite-facade.test.ts:112 "commits work done through the transaction handle and closes idempotently" and the SqliteDatabase contract (database.ts): a failed callback rolls the transaction back and Transaction returns that same error; Close is idempotent.
// Pi source: packages/durable/src/storage/sqlite/database.ts (SqliteDatabase)
// mutation-checked: skipping the rollback fails it
// mutation-checked: zeroing the results of SqliteDatabase.Transaction fails it
// Pi: packages/durable/src/storage/sqlite/database.ts:5 (transaction)
func TestSqliteDatabaseContractRollsBackAFailedTransactionAndClosesIdempotently(t *testing.T) {
	var database chordsqlite.SqliteDatabase = mustOpenMemory(t)
	mustDo(t, database.Exec("CREATE TABLE contract_probe (value INTEGER)"))
	failure := errors.New("callback failed")
	err := database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
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
	mustDo(t, database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
		return transaction.Run("INSERT INTO contract_probe (value) VALUES (?)", 2)
	}))
	row, err := database.Get("SELECT value FROM contract_probe")
	mustDo(t, err)
	expectRow(t, row, chordsqlite.SqliteRow{"value": int64(2)})
	mustDo(t, database.Close())
	mustDo(t, database.Close())
}

// database.ts:21-28 (SqliteDatabase.transaction): when the callback rejects and the rollback fails, the adapter rejects with a different error, "for example an
// AggregateError", so a caller cannot take the callback error for a guaranteed rollback. Pi's node.ts:154 builds AggregateError([error, rollbackError],
// "SQLite transaction failed and rollback failed"): the callback error first, then the rollback failure. The rollback fails here because the callback already
// committed (the escaped COMMIT of Pi 1.2.0 sqlite-facade.test.ts:280 "does not preserve the callback error when rollback itself fails").
func TestSqliteDatabaseContractReportsBothErrorsWhenRollbackFails(t *testing.T) {
	database := mustOpenMemory(t)
	mustDo(t, database.Exec("CREATE TABLE rollback_pair (value INTEGER)"))
	failure := errors.New("callback failed after an escaped commit")
	err := database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
		mustDo(t, transaction.Exec("INSERT INTO rollback_pair (value) VALUES (1)"))
		mustDo(t, transaction.Exec("COMMIT"))
		return failure
	})
	aggregate, ok := errors.AsType[*AggregateError](err)
	if !ok {
		t.Fatalf("error = %v, want an AggregateError", err)
	}
	if errors.Is(err, failure) {
		t.Error("the aggregate unwraps to the callback error, which would read as a guaranteed rollback")
	}
	if aggregate.Error() != "SQLite transaction failed and rollback failed" {
		t.Errorf("message = %q", aggregate.Error())
	}
	if len(aggregate.Errors) != 2 || aggregate.Errors[0] != failure || aggregate.Errors[1] == nil { //nolint:errorlint // identity: the aggregate carries the callback's own error value first
		t.Fatalf("errors = %v, want the callback error then the rollback failure", aggregate.Errors)
	}
	mustDo(t, database.Close())
}
