package session

// The cases of packages/durable/test/session-group-commit.test.ts that need Go to make a publication fail on purpose, which it
// cannot do by patching a prototype (`Transaction.prototype.adopt`, `adoptHeld`, `CachedStorage.prototype.published`):
//   - a transaction's adoption or a held commit's adoption fails because the storage's publication scope aborts the prepared
//     change the commit is about to adopt (a scope runs right before each publication, so this is a precise step, not a race);
//   - the read caches' write-through that throws is the writer's host throwing, tested on the Writer with its own host.
// The scope also holds the first publication, which stands in for upstream's call held in flight: the writer sends nothing more
// until it published, so what is admitted meanwhile is one call.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

var internalNote = durable.DefineDoc(durable.DocDefinition[durable.JsonObject]{
	CommonDocDefinition: durable.CommonDocDefinition[durable.JsonObject]{
		Kind: "gc.internal.note", Version: 1,
		CheckpointWhen: func(durable.JsonObject, []durable.Op, durable.CheckpointInfo) bool { return false },
		Initial:        func() durable.JsonObject { return delta.JsonObjectOf("n", 0.0) },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
})

// scopedMemory is memory storage with a publication scope: scope runs each publication.
type scopedMemory struct {
	*storage.MemoryStorage
	mu    sync.Mutex
	scope func(ctx context.Context, publish func())
}

func (memory *scopedMemory) Publishing(ctx context.Context, publish func()) {
	memory.mu.Lock()
	scope := memory.scope
	memory.mu.Unlock()
	if scope == nil {
		publish()
		return
	}
	scope(ctx, publish)
}

func (memory *scopedMemory) setScope(scope func(ctx context.Context, publish func())) {
	memory.mu.Lock()
	memory.scope = scope
	memory.mu.Unlock()
}

// abortPrepared aborts what tx prepared, so the adoption of its commit fails.
func abortPrepared(tx *Transaction) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	for _, document := range tx.documents {
		if document.change != nil {
			document.change.Abort()
		}
	}
}

type internalCall struct {
	id    uint64
	done  chan struct{}
	value any
	err   error
}

func (started *internalCall) wait() (any, error) {
	<-started.done
	return started.value, started.err
}

// enter blocks until the call is parked in a wait or returned.
func (started *internalCall) enter() { entered.Await(started.id, started.done) }

// commitTicketed issues a commit on the line now (a ticket) and runs it on its own goroutine.
func commitTicketed(kernel *SessionImpl, change func(tx *Transaction) (any, error), options ...durable.CommitOptions) *internalCall {
	ticket := kernel.TakeLineTicket()
	started := &internalCall{done: make(chan struct{})}
	ids := make(chan uint64, 1)
	go func() {
		ids <- goroutineid.Current()
		defer close(started.done)
		started.value, started.err = kernel.CommitWithTicket(ticket, context.Background(), change, TransactionScope{}, options...)
	}()
	started.id = <-ids
	return started
}

func internalBump(id durable.ConversationId, capture **Transaction) func(tx *Transaction) (any, error) {
	return func(tx *Transaction) (any, error) {
		if capture != nil {
			*capture = tx
		}
		note, err := tx.Doc(internalNote, id)
		if err != nil {
			return nil, err
		}
		n, _ := note.Get("n").(float64)
		return n, note.Set("n", n+1)
	}
}

func openInternal(t *testing.T, conversations int) (*SessionImpl, *scopedMemory, []durable.ConversationId) {
	t.Helper()
	memory := &scopedMemory{MemoryStorage: storage.NewMemoryStorage()}
	kernel := NewSessionImpl(memory, Hooks{}, nil, func() *float64 { ms := 600_000.0; return &ms })
	value, err := kernel.CommitWith(context.Background(), func(tx *Transaction) (any, error) {
		ids := make([]durable.ConversationId, 0, conversations)
		for range conversations {
			record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
			if err != nil {
				return nil, err
			}
			ids = append(ids, record.Id)
		}
		return ids, nil
	}, TransactionScope{})
	if err != nil {
		t.Fatal(err)
	}
	return kernel, memory, value.([]durable.ConversationId)
}

func isFailed(err error) (error, bool) {
	failed, ok := errors.AsType[*durable.SessionFailed](err)
	if !ok {
		return nil, false
	}
	return failed.Cause, true
}

func TestGroupCommitLeavesThePartsAfterOneWhoseAdoptionFailsUnpublished(t *testing.T) {
	kernel, memory, ids := openInternal(t, 4)
	a, b, c, d := ids[0], ids[1], ids[2], ids[3]
	var published []ObservedCommit
	var publishedMu sync.Mutex
	kernel.FollowCommits(func(_ context.Context, commit ObservedCommit) {
		publishedMu.Lock()
		published = append(published, commit)
		publishedMu.Unlock()
	})
	var (
		failingTx   *Transaction
		publication int
		entered     = make(chan struct{})
		release     = make(chan struct{})
	)
	// The first publication is held in its scope, as a call in flight; the third is the failing commit's, whose prepared change
	// is aborted right before it is adopted.
	memory.setScope(func(_ context.Context, publish func()) {
		publication++
		switch publication {
		case 1:
			close(entered)
			<-release
		case 3:
			abortPrepared(failingTx)
		}
		publish()
	})
	first := commitTicketed(kernel, internalBump(a, nil))
	<-entered
	before := commitTicketed(kernel, internalBump(b, nil))
	failing := commitTicketed(kernel, internalBump(c, &failingTx))
	after := commitTicketed(kernel, internalBump(d, nil))
	// Wait until the three callbacks ran and were admitted behind the held one: each is parked on its commit's outcome.
	before.enter()
	failing.enter()
	after.enter()
	close(release)
	if _, err := first.wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := before.wait(); err != nil {
		t.Fatal(err)
	}
	_, err := failing.wait()
	if err == nil {
		t.Fatal("the commit whose adoption failed resolved")
	}
	if _, isSessionFailed := isFailed(err); isSessionFailed {
		t.Fatalf("the commit whose adoption failed got SessionFailed %v, want its adoption error", err)
	}
	_, afterErr := after.wait()
	if cause, ok := isFailed(afterErr); !ok || cause != err {
		t.Fatalf("the part after it got %v, want SessionFailed caused by %v", afterErr, err)
	}
	publishedMu.Lock()
	if len(published) != 2 {
		t.Fatalf("%d commits published, want 2", len(published))
	}
	publishedMu.Unlock()
	<-kernel.Closed()
	if end := kernel.End(); end.Kind != durable.SessionEndFailed || end.Error != err {
		t.Fatalf("session ended %+v, want failed with %v", end, err)
	}
	// All four are durable: reopening recovers the unpublished one.
	memory.setScope(nil)
	memory.Reopen()
	reopened := NewSessionImpl(memory, Hooks{}, nil, nil)
	var notes []float64
	for _, id := range []durable.ConversationId{a, b, c, d} {
		value, err := reopened.SnapshotErased(context.Background(), internalNote, id)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := value.Value("n").(float64)
		notes = append(notes, n)
	}
	if !slices.Equal(notes, []float64{1, 1, 1, 1}) {
		t.Fatalf("notes %v, want [1 1 1 1]", notes)
	}
	if err := reopened.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGroupCommitFailsTheSessionForADeferredCommitMemoryCannotAdopt(t *testing.T) {
	for _, alone := range []bool{false, true} {
		name := "behind a stored commit"
		if alone {
			name = "alone, on a storage that runs nothing else"
		}
		t.Run(name, func(t *testing.T) {
			kernel, memory, ids := openInternal(t, 2)
			a, b := ids[0], ids[1]
			if _, err := kernel.CommitWith(context.Background(), internalBump(a, nil), TransactionScope{}); err != nil {
				t.Fatal(err)
			}
			if _, err := kernel.CommitWith(context.Background(), internalBump(b, nil), TransactionScope{}); err != nil {
				t.Fatal(err)
			}
			var published []ObservedCommit
			var publishedMu sync.Mutex
			kernel.FollowCommits(func(_ context.Context, commit ObservedCommit) {
				publishedMu.Lock()
				published = append(published, commit)
				publishedMu.Unlock()
			})
			var staged *Transaction
			memory.setScope(func(_ context.Context, publish func()) {
				// The held commit is the one whose transaction was captured: its adoption is what fails.
				if staged != nil {
					abortPrepared(staged)
				}
				publish()
			})
			forever := positiveInf()
			held := commitTicketed(kernel, internalBump(a, &staged), durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred, WithinMs: &forever}})
			var after *internalCall
			if !alone {
				after = commitTicketed(kernel, internalBump(b, nil))
			}
			_, err := held.wait()
			if err == nil {
				t.Fatal("the deferred commit whose adoption failed resolved")
			}
			if _, isSessionFailed := isFailed(err); isSessionFailed {
				t.Fatalf("the deferred commit got SessionFailed %v, want its adoption error", err)
			}
			if after != nil {
				if _, afterErr := after.wait(); afterErr == nil {
					t.Fatal("the commit after the failed one resolved")
				} else if _, ok := isFailed(afterErr); !ok {
					t.Fatalf("the commit after it got %v, want SessionFailed", afterErr)
				}
			}
			publishedMu.Lock()
			if len(published) != 0 {
				t.Fatalf("%d commits published, want none", len(published))
			}
			publishedMu.Unlock()
			<-kernel.Closed()
			if end := kernel.End(); end.Kind != durable.SessionEndFailed || end.Error != err {
				t.Fatalf("session ended %+v, want failed with %v", end, err)
			}
			if err := kernel.Close(context.Background()); err != nil && !errors.Is(err, err) {
				t.Fatal(err)
			}
			if sizes := kernel.LineSizes(); sizes != (LineSizes{}) {
				t.Fatalf("line sizes %+v, want all zero", sizes)
			}
		})
	}
}

func positiveInf() float64 { var zero float64; return 1 / zero }

// fakeWriterHost is the Session as a writer sees it: storage stored batches in one call, nothing is published through a scope,
// and what fails the Session is recorded.
type fakeWriterHost struct {
	mu        sync.Mutex
	nextSeq   durable.Seq
	failure   error
	fails     []fakeFail
	reports   []error
	published []durable.Seq
	// stored runs at each publication, right before the part's own publish; it may panic, as a failing cache write does.
	stored func(call int)
	calls  int
}

type fakeFail struct {
	err error
	at  int
	own []int
}

func (host *fakeWriterHost) Commit(_ context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	for index := range batches {
		host.mu.Lock()
		host.nextSeq++
		seq := host.nextSeq
		host.mu.Unlock()
		receive(index, seq)
	}
	return durable.CommitResult{Stored: len(batches)}, nil
}

func (host *fakeWriterHost) Stored([]durable.StorageWrite) {
	host.mu.Lock()
	host.calls++
	call, stored := host.calls, host.stored
	host.mu.Unlock()
	if stored != nil {
		stored(call)
	}
}

func (host *fakeWriterHost) Publishing(publish func(), _ context.Context) <-chan struct{} {
	publish()
	return nil
}

func (host *fakeWriterHost) Failure() error {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.failure
}

func (host *fakeWriterHost) Fail(err error, at int, own []int) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.failure != nil {
		return
	}
	host.failure = err
	host.fails = append(host.fails, fakeFail{err: err, at: at, own: own})
}

func (host *fakeWriterHost) Report(err error, _ context.Context) {
	host.mu.Lock()
	host.reports = append(host.reports, err)
	host.mu.Unlock()
}

func (host *fakeWriterHost) Hop() {}

func TestWriterFailsTheSessionForAThrowInTheReadCachesWriteThrough(t *testing.T) {
	// Upstream: "fails the Session for a throw in the read caches' write-through, and leaves the later parts unpublished".
	host := &fakeWriterHost{}
	failure := errors.New("cache write failed")
	host.stored = func(call int) {
		if call == 2 {
			panic(failure)
		}
	}
	writer := NewWriter(host)
	var published []int
	var publishedMu sync.Mutex
	spec := func(index int) PartSpec {
		return PartSpec{
			Index:  index,
			Writes: []durable.StorageWrite{durable.TaskWrite{}},
			Ctx:    context.Background(),
			Publish: func(durable.Seq) *PublishFailure {
				publishedMu.Lock()
				published = append(published, index)
				publishedMu.Unlock()
				return nil
			},
			Discard: func() {},
		}
	}
	// Three parts admitted while the writer is busy are one call: hold the writer with a part of its own.
	lead := writer.Admit(spec(0))
	if err := lead.Wait(); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	host.calls = 0
	host.mu.Unlock()
	parts := make([]*Part, 0, 3)
	for index := 1; index <= 3; index++ {
		parts = append(parts, writer.Admit(spec(index)))
	}
	if err := parts[0].Wait(); err != nil {
		t.Fatal(err)
	}
	if err := parts[1].Wait(); err != failure {
		t.Fatalf("the part whose cache write threw got %v, want %v", err, failure)
	}
	err := parts[2].Wait()
	if cause, ok := isFailed(err); !ok || cause != failure {
		t.Fatalf("the part after it got %v, want SessionFailed caused by %v", err, failure)
	}
	publishedMu.Lock()
	defer publishedMu.Unlock()
	if !slices.Equal(published, []int{0, 1}) {
		t.Fatalf("published parts %v, want [0 1]", published)
	}
	if len(host.fails) != 1 || host.fails[0].err != failure {
		t.Fatalf("session failures %+v, want one with %v", host.fails, failure)
	}
	if writer.Unpublished() != 0 {
		t.Fatalf("%d keys with an unpublished writer, want 0", writer.Unpublished())
	}
}
