package node

// pi: packages/durable/src/storage/sqlite/node.ts

// Ports packages/durable/test/sqlite-facade.test.ts

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/chord/delta"
	chordcloudflare "github.com/MichaelKinsy/PiG/chord/sqlite/cloudflare"
	chordnode "github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	durablecloudflare "github.com/MichaelKinsy/PiG/durable/storage/sqlite/cloudflare"
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

// prepareCountingDatabaseSync counts statement compilations by SQL text, as Pi's PrepareCountingDatabaseSync subclass
// does by overriding prepare.
type prepareCountingDatabaseSync struct {
	*DatabaseSync
	mu            sync.Mutex
	prepareCounts map[string]int
	order         []string
}

func (database *prepareCountingDatabaseSync) count(sqlText string) {
	database.mu.Lock()
	defer database.mu.Unlock()
	if database.prepareCounts[sqlText] == 0 {
		database.order = append(database.order, sqlText)
	}
	database.prepareCounts[sqlText]++
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

func entryWrite(id durable.EntryId, kind string) durable.StorageWrite {
	return durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: kind}}
}

func rootWrite() durable.StorageWrite {
	return durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}
}

// Pi source: packages/durable/src/storage/sqlite/node.ts, packages/durable/src/storage/sqlite/storage.ts
// mutation-checked: zeroing the results of SqliteStorage.Commit, SqliteStorage.Conversation, SqliteStorage.Document, SqliteStorage.Entry, SqliteStorage.ScanEntries fails it
func TestPortableSQLiteFacadeSettlement(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/sqlite-facade.test.ts:82 "prepares each storage statement once per connection and reuses it across transactions"
	t.Run("prepares each storage statement once per connection and reuses it across transactions", func(t *testing.T) {
		raw, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
		mustDo(t, err)
		connection := &prepareCountingDatabaseSync{DatabaseSync: raw, prepareCounts: map[string]int{}}
		raw.ObservePrepare(connection.count)
		storage, err := durablesqlite.Open(NewNodeSqliteDatabase(raw))
		mustDo(t, err)
		_, err = storage.Commit(background, []durable.StorageWrite{rootWrite()}, func(durable.Seq) {})
		mustDo(t, err)
		writes := make([]durable.StorageWrite, 0, 100)
		for index := range 100 {
			writes = append(writes, entryWrite(durable.EntryId(index+2), "cached"))
		}
		_, err = storage.Commit(background, writes, func(durable.Seq) {})
		mustDo(t, err)
		_, err = storage.Commit(background, []durable.StorageWrite{entryWrite(102, "cached-again")}, func(durable.Seq) {})
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
		_, err = storage.Commit(background, []durable.StorageWrite{rootWrite()}, func(durable.Seq) {})
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

	// packages/durable/test/sqlite-facade.test.ts:124 (Pi c268154c)
	t.Run("reads storage commits in call order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			storage, err := durablesqlite.Open(database)
			mustDo(t, err)
			commit := start(func() (durable.Seq, error) {
				return storage.Commit(background, []durable.StorageWrite{rootWrite()}, func(durable.Seq) {})
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

	// Pi 1.2.0 packages/durable/test/sqlite-facade.test.ts:240 "lets admitted multi-query reads finish before storage closes"
	t.Run("lets admitted multi-query reads finish before storage closes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			database := mustOpenMemory(t)
			storage, err := durablesqlite.Open(database)
			mustDo(t, err)
			const entryId durable.EntryId = 2
			_, err = storage.Commit(background, []durable.StorageWrite{rootWrite(), entryWrite(entryId, "probe")}, func(durable.Seq) {})
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

	// Pi 1.2.0 packages/durable/test/sqlite-facade.test.ts:294 "awaits async transaction settlement and adopts IDs only after success"
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
				return storage.Commit(background, []durable.StorageWrite{entryWrite(100, "settled")}, func(durable.Seq) {})
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
				return storage.Commit(background, []durable.StorageWrite{entryWrite(200, "rejected")}, func(durable.Seq) {})
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

	// Pi 1.2.0 packages/durable/test/sqlite-facade.test.ts:346 "reads a document from one committed state while a commit replaces its base"
	t.Run("reads a document from one committed state while a commit replaces its base", func(t *testing.T) {
		const id durable.DocumentId = 5
		// Each yield count starts the commit at a different point of the read's record and revision queries.
		for yields := range 16 {
			storage, err := durablesqlite.Open(mustOpenMemory(t))
			mustDo(t, err)
			_, err = storage.Commit(background, []durable.StorageWrite{durable.DocumentCreateWrite{
				Record:  durable.DocumentCreate{Id: id, Kind: "replaced", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
				Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("value", 1)},
			}}, func(durable.Seq) {})
			mustDo(t, err)
			read := start(func() (*durable.StoredDocument, error) { return storage.Document(background, id, durable.CurrentPoint) })
			for range yields {
				yieldGoroutine()
			}
			replace := start(func() (durable.Seq, error) {
				return storage.Commit(background, []durable.StorageWrite{durable.DocumentChangeWrite{
					Id:      id,
					Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("value", 2)},
				}}, func(durable.Seq) {})
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
	_, err = storage.Commit(background, []durable.StorageWrite{rootWrite()}, func(durable.Seq) {})
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

// packages/durable/test/sqlite-facade.test.ts:88 (Pi c268154c) "re-exports the Chord driver classes and Node opener without
// wrapping them": the durable names are Chord's, so a database opened through either is the same type.
func TestDurableReExportsTheChordDriver(t *testing.T) {
	var database any = (*NodeSqliteDatabase)(nil)
	if _, ok := database.(*chordnode.NodeSqliteDatabase); !ok {
		t.Fatal("node.NodeSqliteDatabase is not Chord's NodeSqliteDatabase")
	}
	opened, err := OpenNodeSqliteDatabase(":memory:", NodeSqliteDatabaseOptions{})
	mustDo(t, err)
	var chordOpened any = opened
	if _, ok := chordOpened.(*chordnode.NodeSqliteDatabase); !ok {
		t.Fatal("node.OpenNodeSqliteDatabase does not return Chord's NodeSqliteDatabase")
	}
	mustDo(t, opened.Close())
	var cf any = (*durablecloudflare.DurableObjectSqliteDatabase)(nil)
	if _, ok := cf.(*chordcloudflare.DurableObjectSqliteDatabase); !ok {
		t.Fatal("cloudflare.DurableObjectSqliteDatabase is not Chord's DurableObjectSqliteDatabase")
	}
}
