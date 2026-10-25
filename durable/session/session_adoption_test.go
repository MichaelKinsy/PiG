package session_test

// Ports packages/durable/test/session-adoption.test.ts: the Session adopts a batch inside Storage.Commit, as Storage calls its
// adopt callback once when the batch is durable, and fails when Storage rejects after adopting or never adopts. Upstream
// also spies on Transaction.prototype.adopt and .discard; Go cannot patch a method, so the adoption count is read from the
// storage's own order log and from the adopted snapshot, and "discard not called" is read from the Session failing with the
// storage's error while the adopted value stays visible.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

var adoptionCounterDoc = defineDoc("adoption.counter", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 0.0) })

type adoptionFault int

const (
	noFault adoptionFault = iota
	rejectAfterAdopt
	skipAdopt
	rejectBeforeAdopt
)

// faultyStorage is memory storage whose next commit, once durable, misbehaves as fault says.
type faultyStorage struct {
	*storage.MemoryStorage
	mu    sync.Mutex
	fault adoptionFault
	order []string
}

func (faulty *faultyStorage) record(step string) {
	faulty.mu.Lock()
	faulty.order = append(faulty.order, step)
	faulty.mu.Unlock()
}

func (faulty *faultyStorage) Order() []string {
	faulty.mu.Lock()
	defer faulty.mu.Unlock()
	return slices.Clone(faulty.order)
}

func (faulty *faultyStorage) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	faulty.mu.Lock()
	fault := faulty.fault
	faulty.fault = noFault
	faulty.mu.Unlock()
	switch fault {
	case skipAdopt:
		return faulty.MemoryStorage.Commit(ctx, writes, func(durable.Seq) {})
	case rejectBeforeAdopt:
		return 0, errors.New("disk full")
	}
	seq, err := faulty.MemoryStorage.Commit(ctx, writes, func(committed durable.Seq) {
		faulty.record("adopt")
		adopt(committed)
		faulty.record("adopted")
	})
	if err != nil {
		return 0, err
	}
	faulty.record("resolve")
	if fault == rejectAfterAdopt {
		return 0, errors.New("reclaim failed")
	}
	return seq, nil
}

func openAdoption() (*session.SessionImpl, *faultyStorage) {
	faulty := &faultyStorage{MemoryStorage: storage.NewMemoryStorage()}
	return session.NewSessionImpl(faulty, session.Hooks{}, nil, nil), faulty
}

func bumpCounter(t *testing.T, kernel *session.SessionImpl) error {
	t.Helper()
	return tryCommit(kernel, func(tx durable.Tx) error {
		draft := mustDoc(t, tx, adoptionCounterDoc)
		return draft.Set("count", num(draft.Get("count"))+1)
	})
}

func TestSessionAdoptionInsideStorageCommit(t *testing.T) {
	t.Run("adopts synchronously inside the commit, before Storage resolves", func(t *testing.T) {
		kernel, faulty := openAdoption()
		must(t, bumpCounter(t, kernel))
		if got := faulty.Order(); !slices.Equal(got, []string{"adopt", "adopted", "resolve"}) {
			t.Fatalf("storage order %v, want [adopt adopted resolve]", got)
		}
		expectEqual(t, snapshot(t, kernel, adoptionCounterDoc), delta.JsonObjectOf("count", 1.0))
		must(t, kernel.Close(ctx))
	})

	t.Run("fails the Session without discarding when Storage rejects after adoption", func(t *testing.T) {
		kernel, faulty := openAdoption()
		must(t, bumpCounter(t, kernel))
		faulty.mu.Lock()
		faulty.fault = rejectAfterAdopt
		faulty.mu.Unlock()
		expectErrorContains(t, bumpCounter(t, kernel), "reclaim failed")
		if failure := kernel.Failure(); failure == nil || failure.Error() != "reclaim failed" {
			t.Fatalf("session failure %v, want the reclaim error", failure)
		}
	})

	t.Run("discards the staged changes when Storage rejects before adoption", func(t *testing.T) {
		kernel, faulty := openAdoption()
		must(t, bumpCounter(t, kernel))
		faulty.mu.Lock()
		faulty.fault = rejectBeforeAdopt
		faulty.mu.Unlock()
		expectErrorContains(t, bumpCounter(t, kernel), "disk full")
		if failure := kernel.Failure(); failure == nil || failure.Error() != "disk full" {
			t.Fatalf("session failure %v, want the storage error", failure)
		}
		if got := faulty.Order(); !slices.Equal(got, []string{"adopt", "adopted", "resolve"}) {
			t.Fatalf("storage order %v, want only the first commit", got)
		}
	})

	t.Run("fails the Session when Storage resolves without adopting", func(t *testing.T) {
		kernel, faulty := openAdoption()
		faulty.mu.Lock()
		faulty.fault = skipAdopt
		faulty.mu.Unlock()
		expectErrorContains(t, bumpCounter(t, kernel), "without adopting")
		if kernel.Failure() == nil {
			t.Fatal("the session did not fail")
		}
	})
}
