package session_test

// Ports packages/durable/test/session-adoption.test.ts: the Session adopts and publishes a batch inside Storage.Commit, as
// Storage calls its adopt callback once when the batch is durable, and fails when Storage rejects after adopting or never
// adopts. Upstream's one microtask that dispatches the commit to subscribeCommits listeners, which runs between the
// storage's synchronous step and its asynchronous resolution, runs here when Storage.Commit returns to the Session
// (before the Session sees it resolve), so the storage's own "resolve" step precedes "host" in the order logs. The JSONL
// case of "publishes a JSONL commit before the storage reclaims its sidecar and resolves" has no Go counterpart for the
// same reason: Go runs the dispatch after the storage's reclaim. Upstream
// also spies on Transaction.prototype.adopt and .discard; Go cannot patch a method, so the adoption count is read from the
// storage's own order log and from the adopted snapshot, and "discard not called" is read from the Session failing with the
// storage's error while the adopted value stays visible.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

var adoptionCounterDoc = defineDoc("adoption.counter", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 0.0) })

type adoptionFault int

const (
	noFault adoptionFault = iota
	rejectAfterAdopt
	skipAdopt
	rejectBeforeAdopt
)

// faultyStorage is controlled storage that records adopt and its own resolution in its order log; its next commit
// misbehaves as fault says.
type faultyStorage struct {
	*sessiontest.ControlledStorage
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
		return faulty.ControlledStorage.Commit(ctx, writes, func(durable.Seq) {})
	case rejectBeforeAdopt:
		return 0, errors.New("disk full")
	}
	seq, err := faulty.ControlledStorage.Commit(ctx, writes, func(committed durable.Seq) {
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
	faulty := &faultyStorage{ControlledStorage: sessiontest.NewControlledStorage()}
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

var adoptionNoteDoc = defineDoc("adoption.note", 1, latestScope(durable.ForkInitial), func() obj { return delta.JsonObjectOf() })

func documentChangesOf(changes []durable.CommitChange) []durable.DocumentChange {
	var documents []durable.DocumentChange
	for _, change := range changes {
		if document, ok := change.(durable.DocumentChange); ok {
			documents = append(documents, document)
		}
	}
	return documents
}

func opsOf(documents []durable.DocumentChange) []any {
	var ops []any
	for _, document := range documents {
		ops = append(ops, document.Ops)
	}
	return ops
}

func TestPublicationAtAdoption(t *testing.T) {
	t.Run("runs its own observers inside adopt and host listeners when Storage returns, outside it", func(t *testing.T) {
		kernel, faulty := openAdoption()
		kernel.ObserveCommits(func(context.Context, session.ObservedCommit) {
			faulty.record(fmt.Sprintf("observed adopting=%v", faulty.Adopting()))
		})
		kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) {
			faulty.record(fmt.Sprintf("host adopting=%v", faulty.Adopting()))
		})
		must(t, bumpCounter(t, kernel))
		expectEqual(t, faulty.Order(), []string{"adopt", "observed adopting=true", "adopted", "resolve", "host adopting=false"})
		must(t, kernel.Close(ctx))
	})

	t.Run("calls the host listeners registered when its dispatch runs, and none for a commit adopted without one", func(t *testing.T) {
		kernel, faulty := openAdoption()
		var calls []string
		removed := kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) { calls = append(calls, "removed") })
		kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) { calls = append(calls, "kept") })
		faulty.AfterAdopt(func() {
			removed()
			kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) { calls = append(calls, "added") })
		})
		must(t, bumpCounter(t, kernel))
		expectEqual(t, calls, []string{"kept", "added"})
		// A Session with no host listener at adoption queues no dispatch, so one added before Storage resolves misses it.
		bare, bareFaulty := openAdoption()
		var late []durable.Seq
		bareFaulty.AfterAdopt(func() {
			bare.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) { late = append(late, publication.Seq) })
		})
		must(t, bumpCounter(t, bare))
		if len(late) != 0 {
			t.Fatalf("late %v, want none", late)
		}
		must(t, bumpCounter(t, bare))
		if len(late) != 1 {
			t.Fatalf("late %v, want one", late)
		}
		must(t, kernel.Close(ctx))
		must(t, bare.Close(ctx))
	})

	t.Run("fails the Session after Storage returned for a panic of its own observers; the others and hosts got the commit", func(t *testing.T) {
		kernel, faulty := openAdoption()
		bug := errors.New("observer bug")
		kernel.ObserveCommits(func(context.Context, session.ObservedCommit) {
			faulty.record("observed")
			panic(bug)
		})
		kernel.ObserveCommits(func(context.Context, session.ObservedCommit) { faulty.record("observed after") })
		kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) {
			faulty.record(fmt.Sprintf("host failed=%v", kernel.Failure() != nil))
		})
		kernel.SubscribeClose(func() { faulty.record("close") })
		must(t, bumpCounter(t, kernel))
		// adopted and resolve: adopt returned to Storage without panicking, and the Session failed only after that.
		expectEqual(t, faulty.Order(), []string{"adopt", "observed", "observed after", "adopted", "resolve", "host failed=false", "close"})
		<-kernel.Closed()
		if end := kernel.End(); end.Reason != durable.SessionEndFailed || !errors.Is(end.Error, bug) {
			t.Fatalf("end %+v", end)
		}
		faulty.Reopen()
		reopened := session.NewSessionImpl(faulty, session.Hooks{}, nil, nil)
		expectEqual(t, snapshot(t, reopened, adoptionCounterDoc), delta.JsonObjectOf("count", 1.0))
		must(t, reopened.Close(ctx))
	})

	t.Run("publishes a commit that Storage rejects after adopting it, then fails the Session", func(t *testing.T) {
		kernel, faulty := openAdoption()
		must(t, bumpCounter(t, kernel))
		var stored []durable.Seq
		kernel.ObserveCommits(func(context.Context, session.ObservedCommit) { faulty.record("observed") })
		kernel.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			faulty.record("host")
			stored = append(stored, publication.Seq)
		})
		faulty.mu.Lock()
		faulty.order = nil
		faulty.fault = rejectAfterAdopt
		faulty.mu.Unlock()
		expectErrorContains(t, bumpCounter(t, kernel), "reclaim failed")
		expectEqual(t, faulty.Order(), []string{"adopt", "observed", "adopted", "resolve", "host"})
		if len(stored) != 1 {
			t.Fatalf("stored %v", stored)
		}
		<-kernel.Closed()
		if end := kernel.End(); end.Reason != durable.SessionEndFailed {
			t.Fatalf("end %+v", end)
		}
		faulty.Reopen()
		reopened := session.NewSessionImpl(faulty, session.Hooks{}, nil, nil)
		expectEqual(t, snapshot(t, reopened, adoptionCounterDoc), delta.JsonObjectOf("count", 2.0))
		must(t, reopened.Close(ctx))
	})

	t.Run("keeps what host listeners get: exactly what was stored, held changes included", func(t *testing.T) {
		kernel, faulty := openAdoption()
		conversationId := sessiontest.CreateConversation(t, kernel)
		commit(t, kernel, func(tx durable.Tx) error { _, err := tx.Doc(adoptionNoteDoc, conversationId); return err })
		var observed []session.ObservedCommit
		var stored []durable.CommitPublication
		kernel.ObserveCommits(func(_ context.Context, publication session.ObservedCommit) {
			faulty.record("observed")
			observed = append(observed, publication)
		})
		kernel.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			faulty.record("host")
			stored = append(stored, publication)
		})
		faulty.mu.Lock()
		faulty.order = nil
		faulty.mu.Unlock()
		_, err := kernel.Commit(ctx, func(tx durable.Tx) (any, error) {
			return nil, mustDoc(t, tx, adoptionNoteDoc, conversationId).Set("a", 1)
		}, within(math.Inf(1)))
		must(t, err)
		// Held in memory: observed at once, nothing stored.
		expectEqual(t, faulty.Order(), []string{"observed"})
		commit(t, kernel, func(tx durable.Tx) error { return mustDoc(t, tx, adoptionNoteDoc, conversationId).Set("b", 2) })
		expectEqual(t, faulty.Order(), []string{"observed", "adopt", "observed", "adopted", "resolve", "host"})
		if len(observed) != 2 || len(stored) != 1 {
			t.Fatalf("observed %d stored %d", len(observed), len(stored))
		}
		expectEqual(t, opsOf(documentChangesOf(observed[1].Changes)), []any{[]durable.Op{{"s", []any{"b"}, 2}}})
		expectEqual(t, opsOf(documentChangesOf(stored[0].Changes)), []any{[]durable.Op{{"s", []any{"a"}, 1}, {"s", []any{"b"}, 2}}})
		if stored[0].Seq != observed[1].Seq {
			t.Fatalf("stored seq %d, observed seq %d", stored[0].Seq, observed[1].Seq)
		}
		must(t, kernel.Close(ctx))
	})

	t.Run("publishes a store of held changes to host listeners once its adopt returned", func(t *testing.T) {
		kernel, faulty := openAdoption()
		conversationId := sessiontest.CreateConversation(t, kernel)
		commit(t, kernel, func(tx durable.Tx) error { _, err := tx.Doc(adoptionNoteDoc, conversationId); return err })
		kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) {
			faulty.record(fmt.Sprintf("host adopting=%v", faulty.Adopting()))
		})
		_, err := kernel.Commit(ctx, func(tx durable.Tx) (any, error) {
			return nil, mustDoc(t, tx, adoptionNoteDoc, conversationId).Set("a", 1)
		}, within(math.Inf(1)))
		must(t, err)
		faulty.mu.Lock()
		faulty.order = nil
		faulty.mu.Unlock()
		must(t, kernel.UnloadDocuments())
		expectEqual(t, faulty.Order(), []string{"adopt", "adopted", "resolve", "host adopting=false"})
		must(t, kernel.Close(ctx))
	})

	t.Run("publishes a store of held changes that Storage rejects after adopting it, then fails the Session", func(t *testing.T) {
		kernel, faulty := openAdoption()
		conversationId := sessiontest.CreateConversation(t, kernel)
		commit(t, kernel, func(tx durable.Tx) error { _, err := tx.Doc(adoptionNoteDoc, conversationId); return err })
		var stored [][]any
		kernel.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			stored = append(stored, opsOf(documentChangesOf(publication.Changes)))
		})
		_, err := kernel.Commit(ctx, func(tx durable.Tx) (any, error) {
			return nil, mustDoc(t, tx, adoptionNoteDoc, conversationId).Set("a", 1)
		}, within(math.Inf(1)))
		must(t, err)
		faulty.mu.Lock()
		faulty.order = nil
		faulty.fault = rejectAfterAdopt
		faulty.mu.Unlock()
		expectErrorContains(t, kernel.UnloadDocuments(), "reclaim failed")
		expectEqual(t, faulty.Order(), []string{"adopt", "adopted", "resolve"})
		expectEqual(t, stored, [][]any{{[]durable.Op{{"s", []any{"a"}, 1}}}})
		<-kernel.Closed()
		if end := kernel.End(); end.Reason != durable.SessionEndFailed || end.Error == nil || end.Error.Error() != "reclaim failed" {
			t.Fatalf("end %+v", end)
		}
	})
}
