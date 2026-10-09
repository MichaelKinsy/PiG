package node

// pi: packages/durable/src/storage/sqlite/node.ts

// Ports packages/durable/test/sqlite-facade.test.ts

import (
	"context"
	"errors"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

var background = context.Background()

type settlementMode int

const (
	settleImmediately settlementMode = iota
	settleDelay
	settleReject
)

// controlledSettlementDatabase delays the settlement a caller observes after the delegate transaction has run.
type controlledSettlementDatabase struct {
	*NodeSqliteDatabase
	mu      sync.Mutex
	mode    settlementMode
	pending chan struct{}
}

func (database *controlledSettlementDatabase) Transaction(
	callback func(transaction durablesqlite.SqliteExecutor) error,
) error {
	database.mu.Lock()
	mode := database.mode
	database.mode = settleImmediately
	database.mu.Unlock()
	if mode == settleImmediately {
		return database.NodeSqliteDatabase.Transaction(callback)
	}
	settlement := database.NodeSqliteDatabase.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
		if err := callback(transaction); err != nil {
			return err
		}
		if mode == settleReject {
			return errors.New("controlled settlement rejection")
		}
		return nil
	})
	gate := make(chan struct{})
	database.mu.Lock()
	database.pending = gate
	database.mu.Unlock()
	<-gate
	return settlement
}

func (database *controlledSettlementDatabase) controlNextSettlement(t *testing.T, mode settlementMode) {
	t.Helper()
	database.mu.Lock()
	defer database.mu.Unlock()
	if database.pending != nil {
		t.Fatal("A settlement is already pending")
	}
	database.mode = mode
}

func (database *controlledSettlementDatabase) settle(t *testing.T) {
	t.Helper()
	database.mu.Lock()
	gate := database.pending
	database.pending = nil
	database.mu.Unlock()
	if gate == nil {
		t.Fatal("No settlement is pending")
	}
	close(gate)
}

// prepareCountingDatabaseSync counts statement compilations by SQL text.
type prepareCountingDatabaseSync struct {
	*DatabaseSync
	mu            sync.Mutex
	prepareCounts map[string]int
	order         []string
}

func (database *prepareCountingDatabaseSync) Prepare(sqlText string) (*StatementSync, error) {
	database.mu.Lock()
	if database.prepareCounts[sqlText] == 0 {
		database.order = append(database.order, sqlText)
	}
	database.prepareCounts[sqlText]++
	database.mu.Unlock()
	return database.DatabaseSync.Prepare(sqlText)
}

func (database *prepareCountingDatabaseSync) repeatedPrepares() []string {
	database.mu.Lock()
	defer database.mu.Unlock()
	repeated := []string{}
	for _, sqlText := range database.order {
		if database.prepareCounts[sqlText] > 1 {
			repeated = append(repeated, sqlText)
		}
	}
	return repeated
}

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
	database, err := OpenNodeSqliteDatabase(":memory:", NodeSqliteStorageOptions{})
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

func values(rows []durablesqlite.SqliteRow) []int64 {
	result := []int64{}
	for _, row := range rows {
		result = append(result, row["value"].(int64))
	}
	return result
}

func entryWrite(id durable.EntryId, kind string) durable.StorageWrite {
	return durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: kind}}
}

func rootWrite() durable.StorageWrite {
	return durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}
}

// Pi source: packages/durable/src/storage/sqlite/node.ts, packages/durable/src/storage/sqlite/storage.ts
// mutation-checked: zeroing the results of NodeSqliteDatabase.Transaction, SqliteStorage.Commit, SqliteStorage.Conversation, SqliteStorage.Document, SqliteStorage.Entry, SqliteStorage.ScanEntries fails it
func TestPortableSQLiteFacadeSettlement(t *testing.T) {
	t.Run("prepares each storage statement once per connection and reuses it across transactions", func(t *testing.T) {
		raw, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
		mustDo(t, err)
		connection := &prepareCountingDatabaseSync{DatabaseSync: raw, prepareCounts: map[string]int{}}
		storage, err := durablesqlite.Open(newNodeSqliteDatabase(connection))
		mustDo(t, err)
		_, err = storage.Commit(background, []durable.StorageWrite{rootWrite()})
		mustDo(t, err)
		writes := make([]durable.StorageWrite, 0, 100)
		for index := range 100 {
			writes = append(writes, entryWrite(durable.EntryId(index+2), "cached"))
		}
		_, err = storage.Commit(background, writes)
		mustDo(t, err)
		_, err = storage.Commit(background, []durable.StorageWrite{entryWrite(102, "cached-again")})
		mustDo(t, err)
		first, err := storage.Entry(background, 2)
		mustDo(t, err)
		if first == nil || first.Entry.Kind != "cached" {
			t.Fatalf("entry 2 = %+v", first)
		}
		last, err := storage.Entry(background, 102)
		mustDo(t, err)
		if last == nil || last.Entry.Kind != "cached-again" {
			t.Fatalf("entry 102 = %+v", last)
		}
		_, err = storage.Commit(background, []durable.StorageWrite{rootWrite()})
		expectError(t, err, "ID 1 already belongs to conversation")
		first, err = storage.Entry(background, 2)
		mustDo(t, err)
		if first == nil || first.Entry.Kind != "cached" {
			t.Fatalf("entry 2 after rejection = %+v", first)
		}
		if repeated := connection.repeatedPrepares(); len(repeated) != 0 {
			t.Fatalf("repeated prepares: %q", repeated)
		}
		mustDo(t, storage.Close(background))
	})

	t.Run("commits work done through the transaction handle and closes idempotently", func(t *testing.T) {
		database := mustOpenMemory(t)
		mustDo(t, database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
			if err := transaction.Exec("CREATE TABLE async_probe (value INTEGER)"); err != nil {
				return err
			}
			return transaction.Run("INSERT INTO async_probe (value) VALUES (?)", 1)
		}))
		row, err := database.Get("SELECT value FROM async_probe")
		mustDo(t, err)
		expectRow(t, row, durablesqlite.SqliteRow{"value": int64(1)})
		mustDo(t, database.Close())
		mustDo(t, database.Close())
	})

	t.Run("serializes concurrent transactions", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			mustDo(t, database.Exec("CREATE TABLE transaction_queue (value INTEGER)"))
			firstStarted := make(chan struct{})
			firstGate := make(chan struct{})
			first := startErr(func() error {
				return database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
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
				return database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
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

	t.Run("runs operations in call order whether they start immediately or wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			mustDo(t, database.Exec("CREATE TABLE call_order (value INTEGER)"))
			// Operations called during a transaction must neither see its uncommitted rows nor join its rollback.
			inserted := make(chan struct{})
			yield := make(chan struct{})
			transaction := startErr(func() error {
				return database.Transaction(func(handle durablesqlite.SqliteExecutor) error {
					if err := handle.Run("INSERT INTO call_order (value) VALUES (?)", 1); err != nil {
						return err
					}
					close(inserted)
					<-yield
					return errors.New("roll back")
				})
			})
			<-inserted
			beforeWrite := start(func() ([]durablesqlite.SqliteRow, error) {
				return database.All("SELECT value FROM call_order ORDER BY value")
			})
			synctest.Wait()
			write := startErr(func() error { return database.Run("INSERT INTO call_order (value) VALUES (?)", 2) })
			synctest.Wait()
			afterWrite := start(func() ([]durablesqlite.SqliteRow, error) {
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

			storage, err := durablesqlite.Open(database)
			mustDo(t, err)
			commit := start(func() (durable.Seq, error) {
				return storage.Commit(background, []durable.StorageWrite{rootWrite()})
			})
			synctest.Wait()
			read := start(func() (*durable.ConversationRecord, error) {
				return storage.Conversation(background, durable.ROOT_CONVERSATION_ID)
			})
			_, err = commit.await()
			mustDo(t, err)
			conversation, err := read.await()
			mustDo(t, err)
			if conversation == nil || *conversation != (durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}) {
				t.Fatalf("conversation = %+v", conversation)
			}
			mustDo(t, storage.Close(background))
		})
	})

	t.Run("queues ordinary operations behind an active transaction", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			mustDo(t, database.Exec("CREATE TABLE operation_queue (value INTEGER)"))
			transactionStarted := make(chan struct{})
			transactionGate := make(chan struct{})
			pending := startErr(func() error {
				return database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
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
			read := start(func() ([]durablesqlite.SqliteRow, error) {
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

	t.Run("queues database calls made synchronously by a transaction that started immediately", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			mustDo(t, database.Exec("CREATE TABLE barrier_probe (value INTEGER)"))
			var outside *future[struct{}]
			err := database.Transaction(func(handle durablesqlite.SqliteExecutor) error {
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

	t.Run("lets admitted multi-query reads finish before storage closes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			storage, err := durablesqlite.Open(database)
			mustDo(t, err)
			const entryId durable.EntryId = 2
			_, err = storage.Commit(background, []durable.StorageWrite{rootWrite(), entryWrite(entryId, "probe")})
			mustDo(t, err)
			// Hold the database so the admitted reads stay in flight while close begins.
			hold := make(chan struct{})
			holding := make(chan struct{})
			holder := startErr(func() error {
				return database.Transaction(func(durablesqlite.SqliteExecutor) error {
					close(holding)
					<-hold
					return nil
				})
			})
			<-holding
			scan := start(func() (durable.Page[durable.EntryRecord, durable.Cursor], error) {
				return storage.ScanEntries(background, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 10, nil)
			})
			entry := start(func() (*durable.EntryAt, error) {
				return storage.VisibleEntry(background, durable.ROOT_CONVERSATION_ID, entryId)
			})
			head := start(func() (*durable.EntryRecord, error) {
				return storage.FindLatestHeadMarker(background, durable.ROOT_CONVERSATION_ID, nil)
			})
			synctest.Wait()
			closed := startErr(func() error { return storage.Close(background) })
			synctest.Wait()
			// A repeated close settles only when the database is closed.
			repeated := startErr(func() error { return storage.Close(background) })
			synctest.Wait()
			if closed.settled() || repeated.settled() {
				t.Fatal("close settled before admitted reads finished")
			}
			close(hold)
			_, err = holder.await()
			mustDo(t, err)
			page, err := scan.await()
			mustDo(t, err)
			if ids := entryIds(page.Items); !slices.Equal(ids, []durable.EntryId{entryId}) {
				t.Fatalf("scan ids = %v", ids)
			}
			found, err := entry.await()
			mustDo(t, err)
			if found == nil || found.Entry.Kind != "probe" {
				t.Fatalf("entry = %+v", found)
			}
			marker, err := head.await()
			mustDo(t, err)
			if marker != nil {
				t.Fatalf("head marker = %+v", marker)
			}
			_, closeErr := closed.await()
			_, repeatedErr := repeated.await()
			mustDo(t, closeErr)
			if !errors.Is(repeatedErr, closeErr) {
				t.Fatalf("repeated close = %v, first close = %v", repeatedErr, closeErr)
			}
			_, err = storage.ScanEntries(background, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 10, nil)
			expectError(t, err, "SqliteStorage is closed")
		})
	})

	t.Run("rejects a transaction handle used after its transaction settles", func(t *testing.T) {
		database := mustOpenMemory(t)
		mustDo(t, database.Exec("CREATE TABLE stale_probe (value INTEGER)"))
		var handle durablesqlite.SqliteExecutor
		mustDo(t, database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
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

	t.Run("does not preserve a guaranteed rejection when rollback itself fails", func(t *testing.T) {
		database := mustOpenMemory(t)
		mustDo(t, database.Exec("CREATE TABLE rollback_probe (value INTEGER)"))
		err := database.Transaction(func(transaction durablesqlite.SqliteExecutor) error {
			if err := transaction.Exec("INSERT INTO rollback_probe (value) VALUES (1)"); err != nil {
				return err
			}
			if err := transaction.Exec("COMMIT"); err != nil {
				return err
			}
			return durable.NewStorageRejected("rejected after an escaped commit", nil)
		})
		if _, ok := errors.AsType[*AggregateError](err); !ok {
			t.Fatalf("error = %v, want AggregateError", err)
		}
		if _, ok := errors.AsType[*durable.StorageRejected](err); ok {
			t.Fatal("rollback failure still reads as a StorageRejected rollback")
		}
		row, err := database.Get("SELECT value FROM rollback_probe")
		mustDo(t, err)
		expectRow(t, row, durablesqlite.SqliteRow{"value": int64(1)})
		mustDo(t, database.Close())
	})

	t.Run("awaits async transaction settlement and adopts IDs only after success", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := &controlledSettlementDatabase{NodeSqliteDatabase: mustOpenMemory(t)}
			database.controlNextSettlement(t, settleDelay)
			opening := start(func() (*durablesqlite.SqliteStorage, error) { return durablesqlite.Open(database) })
			synctest.Wait()
			if opening.settled() {
				t.Fatal("open settled before its migration transaction")
			}
			database.settle(t)
			storage, err := opening.await()
			mustDo(t, err)

			database.controlNextSettlement(t, settleDelay)
			committing := start(func() (durable.Seq, error) {
				return storage.Commit(background, []durable.StorageWrite{entryWrite(100, "settled")})
			})
			synctest.Wait()
			if committing.settled() {
				t.Fatal("commit settled before its transaction")
			}
			expectMint(t, storage, 2)
			database.settle(t)
			seq, err := committing.await()
			mustDo(t, err)
			if seq != 1 {
				t.Fatalf("seq = %d, want 1", seq)
			}
			expectMint(t, storage, 101)

			database.controlNextSettlement(t, settleReject)
			rejected := start(func() (durable.Seq, error) {
				return storage.Commit(background, []durable.StorageWrite{entryWrite(200, "rejected")})
			})
			synctest.Wait()
			expectMint(t, storage, 102)
			database.settle(t)
			_, err = rejected.await()
			expectError(t, err, "controlled settlement rejection")
			expectMint(t, storage, 103)
			missing, err := storage.Entry(background, 200)
			mustDo(t, err)
			if missing != nil {
				t.Fatalf("rejected entry = %+v", missing)
			}
			mustDo(t, storage.Close(background))
		})
	})

	t.Run("reads a document from one committed state while a commit replaces its base", func(t *testing.T) {
		const id durable.DocumentId = 5
		// Each yield count starts the commit at a different point of the read's record and revision queries.
		for yields := range 16 {
			storage, err := durablesqlite.Open(mustOpenMemory(t))
			mustDo(t, err)
			_, err = storage.Commit(background, []durable.StorageWrite{durable.DocumentCreateWrite{
				Record:  durable.DocumentCreate{Id: id, Kind: "replaced", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
				Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("value", 1)},
			}})
			mustDo(t, err)
			read := start(func() (*durable.StoredDocument, error) { return storage.Document(background, id, durable.CurrentPoint) })
			for range yields {
				yieldGoroutine()
			}
			replace := start(func() (durable.Seq, error) {
				return storage.Commit(background, []durable.StorageWrite{durable.DocumentChangeWrite{
					Id:      id,
					Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("value", 2)},
				}})
			})
			stored, err := read.await()
			mustDo(t, err)
			_, err = replace.await()
			mustDo(t, err)
			value := stored.Value.Value("value")
			if value != float64(1) && value != float64(2) {
				t.Fatalf("value = %#v, want 1 or 2", value)
			}
			mustDo(t, storage.Close(background))
		}
	})
}

func expectError(t *testing.T, err error, messageIncludes string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), messageIncludes) {
		t.Fatalf("error = %v, want one containing %q", err, messageIncludes)
	}
}

func expectRow(t *testing.T, actual, expected durablesqlite.SqliteRow) {
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

func expectMint(t *testing.T, storage *durablesqlite.SqliteStorage, expected int64) {
	t.Helper()
	id, err := storage.MintId()
	mustDo(t, err)
	if id != expected {
		t.Fatalf("minted %d, want %d", id, expected)
	}
}

func entryIds(entries []durable.EntryRecord) []durable.EntryId {
	ids := make([]durable.EntryId, len(entries))
	for index, entry := range entries {
		ids[index] = entry.Id
	}
	return ids
}

func yieldGoroutine() { runtime.Gosched() }

// Pi source: packages/durable/src/storage/sqlite/node.ts:136 (`new NodeSqliteDatabase(database: DatabaseSync)`)
// mutation-checked: zeroing the result of NewNodeSqliteDatabase fails it
func TestNewNodeSqliteDatabaseAdaptsADatabaseSync(t *testing.T) {
	raw, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
	mustDo(t, err)
	database := NewNodeSqliteDatabase(raw)
	storage, err := durablesqlite.Open(database)
	mustDo(t, err)
	_, err = storage.Commit(background, []durable.StorageWrite{rootWrite()})
	mustDo(t, err)
	conversation, err := storage.Conversation(background, durable.ROOT_CONVERSATION_ID)
	mustDo(t, err)
	if conversation == nil || conversation.Id != durable.ROOT_CONVERSATION_ID {
		t.Fatalf("a commit through the adapter must be readable back, got %+v", conversation)
	}
	probe, err := raw.Prepare("SELECT count(*) AS tables FROM sqlite_master WHERE type = 'table'")
	mustDo(t, err)
	row, err := probe.Get()
	mustDo(t, err)
	if tables, _ := row["tables"].(int64); tables == 0 {
		t.Fatalf("the adapter must create its tables in the connection it was given, found %v", row)
	}
	mustDo(t, database.Close())
	if err := database.Close(); err != nil {
		t.Fatalf("closing twice must be a no-op, got %v", err)
	}
}
