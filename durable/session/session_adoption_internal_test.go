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

func (sabotaged *sabotagedStorage) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	sabotaged.mu.Lock()
	sabotage := sabotaged.sabotage
	sabotaged.sabotage = nil
	sabotaged.mu.Unlock()
	if sabotage != nil {
		sabotage()
	}
	seq, err := sabotaged.MemoryStorage.Commit(ctx, writes, func(committed durable.Seq) {
		sabotaged.record("adopt")
		adopt(committed)
		sabotaged.record("adopted")
	})
	if err != nil {
		return 0, err
	}
	sabotaged.record("resolve")
	return seq, nil
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
}
