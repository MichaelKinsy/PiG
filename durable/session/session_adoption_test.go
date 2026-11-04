package session_test

// Ports packages/durable/test/session-adoption.test.ts: Storage stores a batch and takes the Session's durability receipt
// (receive) once when the batch is durable; the Session adopts and publishes it once Storage resolved, and fails when
// Storage rejects after the receipt or never gives one. Upstream's one microtask that dispatches the commit to
// observeCommits observers runs here right after the publication's scope, before the commit resolves for its caller, so
// the order logs read "resolve" (Storage) before "observed" (the Session's own observers) and "host".
//
// Go cannot patch Transaction.prototype.adopt, so the adoption count is read from the storage's own order log and from the
// adopted snapshot. The discard assertions are TestWriterDiscardsAPartOnlyWhenStorageRejectedBeforeItsReceipt
// (session_adoption_internal_test.go), which counts a part's Discard on a real Writer, and the case where adoption itself
// throws is TestSessionFailsWithItsOwnAdoptionErrorWhichNeverReachesStorage in the same file.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
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

func (faulty *faultyStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
	faulty.mu.Lock()
	fault := faulty.fault
	faulty.fault = noFault
	faulty.mu.Unlock()
	switch fault {
	case skipAdopt:
		return faulty.ControlledStorage.Commit(ctx, batches, func(int, durable.Seq) {})
	case rejectBeforeAdopt:
		return durable.CommitResult{}, errors.New("disk full")
	}
	result, err := faulty.ControlledStorage.Commit(ctx, batches, func(index int, committed durable.Seq) {
		faulty.record("adopt")
		receive(index, committed)
		faulty.record("adopted")
	})
	if err != nil {
		return durable.CommitResult{}, err
	}
	faulty.record("resolve")
	if fault == rejectAfterAdopt {
		return durable.CommitResult{}, errors.New("reclaim failed")
	}
	return result, nil
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

func TestSessionAdoptionAfterStorageStoredACommit(t *testing.T) {
	t.Run("takes the durability receipt inside the commit, and adopts once Storage resolved", func(t *testing.T) {
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

func TestPublicationAfterStorage(t *testing.T) {
	t.Run("runs its own observers once Storage resolved, and host listeners one microtask later", func(t *testing.T) {
		kernel, faulty := openAdoption()
		kernel.FollowCommits(func(context.Context, session.ObservedCommit) {
			faulty.record(fmt.Sprintf("observed adopting=%v", faulty.Adopting()))
		})
		sessiontest.ObserveCommitsFunc(kernel, func(context.Context, durable.CommitPublication) {
			faulty.record(fmt.Sprintf("host adopting=%v", faulty.Adopting()))
		})
		must(t, bumpCounter(t, kernel))
		expectEqual(t, faulty.Order(), []string{"adopt", "adopted", "resolve", "observed adopting=false", "host adopting=false"})
		must(t, kernel.Close(ctx))
	})

	t.Run("calls the host listeners registered when its dispatch runs, and none for a commit published without one", func(t *testing.T) {
		kernel, _ := openAdoption()
		var calls []string
		removed := sessiontest.ObserveCommitsFunc(kernel, func(context.Context, durable.CommitPublication) { calls = append(calls, "removed") })
		sessiontest.ObserveCommitsFunc(kernel, func(context.Context, durable.CommitPublication) { calls = append(calls, "kept") })
		// During the publication, after the dispatch was queued.
		changed := false
		kernel.FollowCommits(func(context.Context, session.ObservedCommit) {
			if changed {
				return
			}
			changed = true
			removed()
			sessiontest.ObserveCommitsFunc(kernel, func(context.Context, durable.CommitPublication) { calls = append(calls, "added") })
		})
		must(t, bumpCounter(t, kernel))
		expectEqual(t, calls, []string{"kept", "added"})
		// A Session with no host listener at the publication queues no dispatch, so one added during it misses it.
		bare, _ := openAdoption()
		var late []durable.Seq
		added := false
		bare.FollowCommits(func(context.Context, session.ObservedCommit) {
			if added {
				return
			}
			added = true
			sessiontest.ObserveCommitsFunc(bare, func(_ context.Context, publication durable.CommitPublication) { late = append(late, publication.Seq) })
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
		kernel.FollowCommits(func(context.Context, session.ObservedCommit) {
			faulty.record("observed")
			panic(bug)
		})
		kernel.FollowCommits(func(context.Context, session.ObservedCommit) { faulty.record("observed after") })
		sessiontest.ObserveCommitsFunc(kernel, func(context.Context, durable.CommitPublication) {
			faulty.record(fmt.Sprintf("host failed=%v", kernel.Failure() != nil))
		})
		kernel.SubscribeClose(func() { faulty.record("close") })
		must(t, bumpCounter(t, kernel))
		// Published once Storage resolved; the Session failed only after the hosts got the commit.
		expectEqual(t, faulty.Order(), []string{"adopt", "adopted", "resolve", "observed", "observed after", "host failed=false", "close"})
		<-kernel.Closed()
		if end := kernel.End(); end.Kind != durable.SessionEndFailed || !errors.Is(end.Error, bug) {
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
		kernel.FollowCommits(func(context.Context, session.ObservedCommit) { faulty.record("observed") })
		sessiontest.ObserveCommitsFunc(kernel, func(_ context.Context, publication durable.CommitPublication) {
			faulty.record("host")
			stored = append(stored, publication.Seq)
		})
		faulty.mu.Lock()
		faulty.order = nil
		faulty.fault = rejectAfterAdopt
		faulty.mu.Unlock()
		expectErrorContains(t, bumpCounter(t, kernel), "reclaim failed")
		expectEqual(t, faulty.Order(), []string{"adopt", "adopted", "resolve", "observed", "host"})
		if len(stored) != 1 {
			t.Fatalf("stored %v", stored)
		}
		<-kernel.Closed()
		if end := kernel.End(); end.Kind != durable.SessionEndFailed {
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
		kernel.FollowCommits(func(_ context.Context, publication session.ObservedCommit) {
			faulty.record("observed")
			observed = append(observed, publication)
		})
		sessiontest.ObserveCommitsFunc(kernel, func(_ context.Context, publication durable.CommitPublication) {
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
		expectEqual(t, faulty.Order(), []string{"observed", "adopt", "adopted", "resolve", "observed", "host"})
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

	t.Run("publishes a store of held changes to host listeners once Storage resolved", func(t *testing.T) {
		kernel, faulty := openAdoption()
		conversationId := sessiontest.CreateConversation(t, kernel)
		commit(t, kernel, func(tx durable.Tx) error { _, err := tx.Doc(adoptionNoteDoc, conversationId); return err })
		sessiontest.ObserveCommitsFunc(kernel, func(context.Context, durable.CommitPublication) {
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
		sessiontest.ObserveCommitsFunc(kernel, func(_ context.Context, publication durable.CommitPublication) {
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
		if end := kernel.End(); end.Kind != durable.SessionEndFailed || end.Error == nil || end.Error.Error() != "reclaim failed" {
			t.Fatalf("end %+v", end)
		}
	})
}

// Own test: the publication step the Session's own observers get (chordctx.WithStep) ends once all of them ran, even when one
// panics, and before the host listeners' dispatch: a call an own observer parks in the step runs after the later own
// observers, and the host listeners see no live step.
func TestPublicationStepEndsAfterEveryOwnObserverEvenWhenOneOfThemPanics(t *testing.T) {
	kernel, faulty := openAdoption()
	kernel.FollowCommits(func(ctx context.Context, _ session.ObservedCommit) {
		if !chordctx.StepBatch(ctx, "step", func() { faulty.record("parked ran") }, func(items []func()) {
			for _, item := range items {
				item()
			}
		}) {
			faulty.record("no step")
		}
	})
	kernel.FollowCommits(func(context.Context, session.ObservedCommit) {
		faulty.record("observed after")
		panic(errors.New("observer bug"))
	})
	sessiontest.ObserveCommitsFunc(kernel, func(ctx context.Context, _ durable.CommitPublication) {
		if chordctx.StepBatch(ctx, "step-host", func() {}, func([]func()) {}) {
			faulty.record("host in a live step")
		}
		faulty.record("host")
	})
	must(t, bumpCounter(t, kernel))
	<-kernel.Closed()
	expectEqual(t, faulty.Order(), []string{"adopt", "adopted", "resolve", "observed after", "parked ran", "host"})
}
