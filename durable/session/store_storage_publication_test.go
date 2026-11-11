package session_test

// pi: packages/durable/test/store-storage-commit.test.ts "publication"
//
// Ports "publishes each commit of a call in a host batch of its own, after the call's one transaction": a Session over a
// StoreStorage whose store has a host attached. It lives with the Session tests because the case needs the Session's writer
// (what it admits behind a call in flight is sent in the next call, and each commit of a call is published on its own).

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage/store"
)

// transactionCounter counts the backend transactions of its database.
type transactionCounter struct {
	sqlite.SqliteDatabase
	transactions atomic.Int64
}

func (db *transactionCounter) Transaction(work func(sqlite.SqliteExecutor) error) error {
	db.transactions.Add(1)
	return db.SqliteDatabase.Transaction(work)
}

// callHoldingStorage is a StoreStorage whose first Commit call waits for its gate and which records the number of batches of
// every call.
type callHoldingStorage struct {
	*store.StoreStorage
	mu      sync.Mutex
	calls   []int
	entered chan struct{}
	gate    chan struct{}
}

func (storage *callHoldingStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
	storage.mu.Lock()
	storage.calls = append(storage.calls, len(batches))
	first := len(storage.calls) == 1
	storage.mu.Unlock()
	if first {
		close(storage.entered)
		<-storage.gate
	}
	return storage.StoreStorage.Commit(ctx, batches, receive)
}

func (storage *callHoldingStorage) callSizes() []int {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	return slices.Clone(storage.calls)
}

func TestStoreStoragePublishesEachCommitOfACallInAHostBatchOfItsOwn(t *testing.T) {
	inner, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	db := &transactionCounter{SqliteDatabase: inner}
	outbox := true
	opened, err := chordstore.OpenStore(chordstore.SqliteBackend(db), ctx, chordstore.StoreOptions{Outbox: &outbox})
	if err != nil {
		t.Fatal(err)
	}
	chord := opened.(*chordstore.ChordStore)
	owned, err := store.Open(ctx, chord, "harness")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := durabletest.CommitWrites(ctx, owned, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}); err != nil {
		t.Fatal(err)
	}
	host, err := chordhost.CreateHost(chordhost.HostOptions{Store: chord, OnError: func(chordhost.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	storage := &callHoldingStorage{StoreStorage: owned, entered: make(chan struct{}), gate: make(chan struct{})}
	line := session.CreateSession(storage)
	var (
		mu     sync.Mutex
		events []string
	)
	line.FollowCommits(func(_ context.Context, commit session.ObservedCommit) {
		if commit.Memory {
			return
		}
		batch := chord.Batch()
		mu.Lock()
		events = append(events, fmt.Sprintf("publish %d running=%v", commit.Seq, batch.Running()))
		mu.Unlock()
		batch.Flushing(func(context.Context) error {
			mu.Lock()
			events = append(events, fmt.Sprintf("flush %d", commit.Seq))
			mu.Unlock()
			return nil
		})
	})
	appendEntry := func(kind string) func() error {
		return func() error {
			_, err := durable.Commit(ctx, line, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(durable.ROOT_CONVERSATION_ID, durable.EntryDraft{Kind: kind})
			})
			return err
		}
	}
	// Hold the first call, so the commits after it are sent together in the next one.
	var commits sync.WaitGroup
	failures := make(chan error, 4)
	start := func(kind string) {
		commits.Go(func() {
			if err := appendEntry(kind)(); err != nil {
				failures <- err
			}
		})
	}
	start("a")
	<-storage.entered
	for n := 1; n <= 3; n++ {
		start(fmt.Sprintf("b%d", n))
	}
	for session.QueuedParts(line) < 3 {
		runtime.Gosched()
	}
	before := db.transactions.Load()
	close(storage.gate)
	commits.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if calls := storage.callSizes(); !slices.Equal(calls, []int{1, 3}) {
		t.Fatalf("storage calls had %v batches, want [1 3]", calls)
	}
	if got := db.transactions.Load() - before; got != 2 {
		t.Fatalf("backend transactions = %d, want 2", got)
	}
	seq := chord.LastSeq()
	var want []string
	for _, each := range []int64{seq - 3, seq - 2, seq - 1, seq} {
		want = append(want, fmt.Sprintf("publish %d running=true", each), fmt.Sprintf("flush %d", each))
	}
	mu.Lock()
	got := slices.Clone(events)
	mu.Unlock()
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if err := line.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := chord.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
