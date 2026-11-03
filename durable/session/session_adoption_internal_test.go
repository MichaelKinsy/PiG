package session

// Ports the fifth case of packages/durable/test/session-adoption.test.ts, "fails the Session with its own adoption error, which
// never reaches Storage". Upstream makes Transaction.prototype.adopt throw through a mock; Go cannot patch a method, so the
// storage aborts the transaction's prepared document change after the batch is staged, which makes the real adoption fail.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type sabotagedStorage struct {
	*storage.MemoryStorage
	mu       sync.Mutex
	sabotage func()
	order    []string
}

func (sabotaged *sabotagedStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
	sabotaged.mu.Lock()
	sabotage := sabotaged.sabotage
	sabotaged.sabotage = nil
	sabotaged.mu.Unlock()
	if sabotage != nil {
		sabotage()
	}
	result, err := sabotaged.MemoryStorage.Commit(ctx, batches, func(index int, committed durable.Seq) {
		sabotaged.record("adopt")
		receive(index, committed)
		sabotaged.record("adopted")
	})
	if err != nil {
		return durable.CommitResult{}, err
	}
	sabotaged.record("resolve")
	return result, nil
}

func (sabotaged *sabotagedStorage) record(step string) {
	sabotaged.mu.Lock()
	sabotaged.order = append(sabotaged.order, step)
	sabotaged.mu.Unlock()
}

func TestSessionFailsWithItsOwnAdoptionErrorWhichNeverReachesStorage(t *testing.T) {
	counter := durable.DefineDoc(durable.DocDefinition[durable.JsonObject]{
		CommonDocDefinition: durable.CommonDocDefinition[durable.JsonObject]{
			Kind: "adoption.counter", Version: 1,
			CheckpointWhen: func(durable.JsonObject, []durable.Op, durable.CheckpointInfo) bool { return false },
			Initial:        func() durable.JsonObject { return delta.JsonObjectOf("count", 0.0) },
		},
		DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
	})
	sabotaged := &sabotagedStorage{MemoryStorage: storage.NewMemoryStorage()}
	kernel := NewSessionImpl(sabotaged, Hooks{}, nil, nil)
	var staged *Transaction
	bump := func() error {
		_, err := kernel.CommitWith(context.Background(), func(tx *Transaction) (any, error) {
			staged = tx
			draft, err := tx.Doc(counter)
			if err != nil {
				return nil, err
			}
			current, _ := draft.Get("count").(float64)
			return nil, draft.Set("count", current+1)
		}, TransactionScope{})
		return err
	}
	if err := bump(); err != nil {
		t.Fatal(err)
	}
	sabotaged.mu.Lock()
	sabotaged.sabotage = func() {
		staged.mu.Lock()
		defer staged.mu.Unlock()
		for _, document := range staged.documents {
			if document.change != nil {
				document.change.Abort()
			}
		}
	}
	sabotaged.mu.Unlock()
	err := bump()
	if err == nil {
		t.Fatal("a commit whose adoption failed succeeded")
	}
	// Storage saw a normal adoption and resolved.
	sabotaged.mu.Lock()
	tail := slices.Clone(sabotaged.order[len(sabotaged.order)-3:])
	sabotaged.mu.Unlock()
	if !slices.Equal(tail, []string{"adopt", "adopted", "resolve"}) {
		t.Fatalf("storage order tail %v, want [adopt adopted resolve]", tail)
	}
	if failure := kernel.Failure(); failure == nil || !errors.Is(failure, err) || strings.Contains(failure.Error(), "without adopting") {
		t.Fatalf("session failure %v, want the adoption error %v", failure, err)
	}
	select {
	case <-kernel.Closed():
	case <-time.After(30 * time.Second):
		t.Fatal("the failed Session never ended")
	}
	if end := kernel.End(); end.Kind != durable.SessionEndFailed || !errors.Is(end.Error, err) {
		t.Fatalf("session end %+v, want failed with the adoption error %v", end, err)
	}
}

// rejectingHost is a fakeWriterHost whose storage rejects the call: after taking the batch's receipt (received) or before.
type rejectingHost struct {
	*fakeWriterHost
	received bool
	err      error
}

func (host *rejectingHost) Commit(_ context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	if host.received {
		receive(0, 1)
	}
	return durable.CommitResult{}, host.err
}

// Ports the discard assertions of packages/durable/test/session-adoption.test.ts, "fails the Session without discarding when
// Storage rejects after adoption" (`expect(discard).not.toHaveBeenCalled()`) and "discards the staged changes when Storage
// rejects before adoption" (`expect(discard).toHaveBeenCalledTimes(1)`). Upstream spies on Transaction.prototype.discard,
// which the Session hands the Writer as PartSpec.Discard (session.go: Discard: tx.discard); the Writer alone decides
// whether to call it, so the test counts a part's Discard on a real Writer.
func TestWriterDiscardsAPartOnlyWhenStorageRejectedBeforeItsReceipt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		received bool
		message  string
		discards int
		stored   int
	}{
		{name: "does not discard when Storage rejects after adoption", received: true, message: "reclaim failed", discards: 0, stored: 1},
		{name: "discards once when Storage rejects before adoption", received: false, message: "disk full", discards: 1, stored: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rejection := errors.New(tc.message)
			host := &rejectingHost{fakeWriterHost: &fakeWriterHost{}, received: tc.received, err: rejection}
			writer := NewWriter(host)
			var mu sync.Mutex
			discards, published := 0, 0
			part := writer.Admit(PartSpec{
				Index:  0,
				Writes: []durable.StorageWrite{durable.TaskWrite{}},
				Ctx:    context.Background(),
				Publish: func(durable.Seq) *PublishFailure {
					mu.Lock()
					published++
					mu.Unlock()
					return nil
				},
				Discard: func() {
					mu.Lock()
					discards++
					mu.Unlock()
				},
			})
			if err := part.Wait(); !errors.Is(err, rejection) {
				t.Fatalf("the part got %v, want %v", err, rejection)
			}
			mu.Lock()
			defer mu.Unlock()
			if discards != tc.discards || published != tc.stored {
				t.Fatalf("discards %d, publications %d, want %d and %d", discards, published, tc.discards, tc.stored)
			}
			if len(host.fails) != 1 || !errors.Is(host.fails[0].err, rejection) {
				t.Fatalf("session failures %+v, want one with %v", host.fails, rejection)
			}
		})
	}
}
