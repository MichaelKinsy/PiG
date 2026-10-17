package node

// pi: packages/chord/src/sqlite/node.ts

// Ports packages/chord/test/sqlite/node.test.ts

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// future is a call started without waiting for it, the counterpart of an unawaited Promise.
type future[T any] struct {
	done  chan struct{}
	value T
	err   error
}

func start[T any](operation func() (T, error)) *future[T] {
	result := &future[T]{done: make(chan struct{})}
	go func() {
		defer close(result.done)
		result.value, result.err = operation()
	}()
	return result
}

func startErr(operation func() error) *future[struct{}] {
	return start(func() (struct{}, error) { return struct{}{}, operation() })
}

func (result *future[T]) await() (T, error) {
	<-result.done
	return result.value, result.err
}

func (result *future[T]) settled() bool {
	select {
	case <-result.done:
		return true
	default:
		return false
	}
}

func mustOpenMemory(t *testing.T) *NodeSqliteDatabase {
	t.Helper()
	database, err := OpenNodeSqliteDatabase(":memory:", NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func values(rows []chordsqlite.SqliteRow) []int64 {
	result := []int64{}
	for _, row := range rows {
		result = append(result, row["value"].(int64))
	}
	return result
}

// Pi source: packages/chord/src/sqlite/node.ts
func TestNodeSqliteDriver(t *testing.T) {

	// packages/chord/test/sqlite/node.test.ts (moved from packages/durable/test/sqlite-facade.test.ts in Pi c268154c):112 "commits work done through the transaction handle and closes idempotently"
	t.Run("commits work done through the transaction handle and closes idempotently", func(t *testing.T) {
		database := mustOpenMemory(t)
		mustDo(t, database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
			if err := transaction.Exec("CREATE TABLE async_probe (value INTEGER)"); err != nil {
				return err
			}
			return transaction.Run("INSERT INTO async_probe (value) VALUES (?)", 1)
		}))
		row, err := database.Get("SELECT value FROM async_probe")
		mustDo(t, err)
		expectRow(t, row, chordsqlite.SqliteRow{"value": int64(1)})
		mustDo(t, database.Close())
		mustDo(t, database.Close())
	})

	// packages/chord/test/sqlite/node.test.ts (moved from packages/durable/test/sqlite-facade.test.ts in Pi c268154c):123 "serializes concurrent transactions"
	t.Run("serializes concurrent transactions", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			mustDo(t, database.Exec("CREATE TABLE transaction_queue (value INTEGER)"))
			firstStarted := make(chan struct{})
			firstGate := make(chan struct{})
			first := startErr(func() error {
				return database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
					if err := transaction.Exec("INSERT INTO transaction_queue (value) VALUES (1)"); err != nil {
						return err
					}
					close(firstStarted)
					<-firstGate
					return nil
				})
			})
			<-firstStarted

			var secondStarted bool
			var mu sync.Mutex
			second := startErr(func() error {
				return database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
					mu.Lock()
					secondStarted = true
					mu.Unlock()
					return transaction.Exec("INSERT INTO transaction_queue (value) VALUES (2)")
				})
			})
			synctest.Wait()
			mu.Lock()
			if secondStarted {
				t.Fatal("second transaction started before the first settled")
			}
			mu.Unlock()

			close(firstGate)
			_, err := first.await()
			mustDo(t, err)
			_, err = second.await()
			mustDo(t, err)
			rows, err := database.All("SELECT value FROM transaction_queue ORDER BY value")
			mustDo(t, err)
			expectValues(t, values(rows), []int64{1, 2})
			mustDo(t, database.Close())
		})
	})

	// packages/chord/test/sqlite/node.test.ts (moved from packages/durable/test/sqlite-facade.test.ts in Pi c268154c):158 "runs operations in call order whether they start immediately or wait"
	t.Run("runs operations in call order whether they start immediately or wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			mustDo(t, database.Exec("CREATE TABLE call_order (value INTEGER)"))
			// Operations called during a transaction must neither see its uncommitted rows nor join its rollback.
			inserted := make(chan struct{})
			yield := make(chan struct{})
			transaction := startErr(func() error {
				return database.Transaction(func(handle chordsqlite.SqliteExecutor) error {
					if err := handle.Run("INSERT INTO call_order (value) VALUES (?)", 1); err != nil {
						return err
					}
					close(inserted)
					<-yield
					return errors.New("roll back")
				})
			})
			<-inserted
			beforeWrite := start(func() ([]chordsqlite.SqliteRow, error) {
				return database.All("SELECT value FROM call_order ORDER BY value")
			})
			synctest.Wait()
			write := startErr(func() error { return database.Run("INSERT INTO call_order (value) VALUES (?)", 2) })
			synctest.Wait()
			afterWrite := start(func() ([]chordsqlite.SqliteRow, error) {
				return database.All("SELECT value FROM call_order ORDER BY value")
			})
			synctest.Wait()
			if database.access.queued() != 3 {
				t.Fatalf("queued operations = %d, want 3", database.access.queued())
			}
			close(yield)
			_, err := transaction.await()
			expectError(t, err, "roll back")
			_, err = write.await()
			mustDo(t, err)
			before, err := beforeWrite.await()
			mustDo(t, err)
			expectValues(t, values(before), []int64{})
			after, err := afterWrite.await()
			mustDo(t, err)
			expectValues(t, values(after), []int64{2})
			mustDo(t, database.Close())
		})
	})
	// packages/chord/test/sqlite/node.test.ts (moved from packages/durable/test/sqlite-facade.test.ts in Pi c268154c):186 "queues ordinary operations behind an active transaction"
	t.Run("queues ordinary operations behind an active transaction", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			mustDo(t, database.Exec("CREATE TABLE operation_queue (value INTEGER)"))
			transactionStarted := make(chan struct{})
			transactionGate := make(chan struct{})
			pending := startErr(func() error {
				return database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
					if err := transaction.Exec("INSERT INTO operation_queue (value) VALUES (1)"); err != nil {
						return err
					}
					close(transactionStarted)
					<-transactionGate
					return nil
				})
			})
			<-transactionStarted

			write := startErr(func() error { return database.Exec("INSERT INTO operation_queue (value) VALUES (2)") })
			synctest.Wait()
			read := start(func() ([]chordsqlite.SqliteRow, error) {
				return database.All("SELECT value FROM operation_queue ORDER BY value")
			})
			synctest.Wait()
			if write.settled() {
				t.Fatal("write settled behind an active transaction")
			}
			if read.settled() {
				t.Fatal("read settled behind an active transaction")
			}

			close(transactionGate)
			_, err := pending.await()
			mustDo(t, err)
			_, err = write.await()
			mustDo(t, err)
			rows, err := read.await()
			mustDo(t, err)
			expectValues(t, values(rows), []int64{1, 2})
			mustDo(t, database.Close())
		})
	})

	// packages/chord/test/sqlite/node.test.ts (moved from packages/durable/test/sqlite-facade.test.ts in Pi c268154c):223 "queues database calls made synchronously by a transaction that started immediately"
	t.Run("queues database calls made synchronously by a transaction that started immediately", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			mustDo(t, database.Exec("CREATE TABLE barrier_probe (value INTEGER)"))
			var outside *future[struct{}]
			err := database.Transaction(func(handle chordsqlite.SqliteExecutor) error {
				// Misuse: this call must wait for the transaction instead of joining it.
				outside = startErr(func() error {
					return database.Run("INSERT INTO barrier_probe (value) VALUES (?)", 2)
				})
				synctest.Wait()
				if err := handle.Run("INSERT INTO barrier_probe (value) VALUES (?)", 1); err != nil {
					return err
				}
				return errors.New("roll back")
			})
			expectError(t, err, "roll back")
			_, err = outside.await()
			mustDo(t, err)
			rows, err := database.All("SELECT value FROM barrier_probe")
			mustDo(t, err)
			expectValues(t, values(rows), []int64{2})
			mustDo(t, database.Close())
		})
	})

	// packages/chord/test/sqlite/node.test.ts (moved from packages/durable/test/sqlite-facade.test.ts in Pi c268154c):265 "rejects a transaction handle used after its transaction settles"
	t.Run("rejects a transaction handle used after its transaction settles", func(t *testing.T) {
		database := mustOpenMemory(t)
		mustDo(t, database.Exec("CREATE TABLE stale_probe (value INTEGER)"))
		var handle chordsqlite.SqliteExecutor
		mustDo(t, database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
			handle = transaction
			return transaction.Run("INSERT INTO stale_probe (value) VALUES (?)", 1)
		}))
		const stale = "SQLite transaction handle is no longer active"
		expectError(t, handle.Exec("INSERT INTO stale_probe (value) VALUES (2)"), stale)
		expectError(t, handle.Run("INSERT INTO stale_probe (value) VALUES (?)", 3), stale)
		rows, err := database.All("SELECT value FROM stale_probe")
		mustDo(t, err)
		expectValues(t, values(rows), []int64{1})
		mustDo(t, database.Close())
	})

	// packages/chord/test/sqlite/node.test.ts (moved from packages/durable/test/sqlite-facade.test.ts in Pi c268154c):280 "does not preserve the callback error when rollback itself fails"
	t.Run("does not preserve the callback error when rollback itself fails", func(t *testing.T) {
		database := mustOpenMemory(t)
		mustDo(t, database.Exec("CREATE TABLE rollback_probe (value INTEGER)"))
		err := database.Transaction(func(transaction chordsqlite.SqliteExecutor) error {
			if err := transaction.Exec("INSERT INTO rollback_probe (value) VALUES (1)"); err != nil {
				return err
			}
			if err := transaction.Exec("COMMIT"); err != nil {
				return err
			}
			return errors.New("rejected after an escaped commit")
		})
		if _, ok := errors.AsType[*AggregateError](err); !ok {
			t.Fatalf("error = %v, want AggregateError", err)
		}
		row, err := database.Get("SELECT value FROM rollback_probe")
		mustDo(t, err)
		expectRow(t, row, chordsqlite.SqliteRow{"value": int64(1)})
		mustDo(t, database.Close())
	})

}

func expectError(t *testing.T, err error, messageIncludes string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), messageIncludes) {
		t.Fatalf("error = %v, want one containing %q", err, messageIncludes)
	}
}

func expectRow(t *testing.T, actual, expected chordsqlite.SqliteRow) {
	t.Helper()
	if !maps.Equal(actual, expected) {
		t.Fatalf("row = %#v, want %#v", actual, expected)
	}
}

func expectValues(t *testing.T, actual, expected []int64) {
	t.Helper()
	if !slices.Equal(actual, expected) {
		t.Fatalf("values = %v, want %v", actual, expected)
	}
}
