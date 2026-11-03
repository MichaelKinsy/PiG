package session_test

// Support of the ports of packages/durable/test/session-group-commit.test.ts: storage whose calls can be held in flight,
// refused, or rejected after their receipts, and the calls a test issues to the Session line.
//
// Go mapping decisions:
//   - Upstream's `await flush()` (one macrotask turn) is quiesce(calls...): it returns once every call named has entered, that is,
//     it is parked in a wait (a gate, its commit's outcome, the line) or has returned. A call that "has not settled after a
//     flush" is a call that is still parked.
//   - A commit extends the line synchronously upstream; a Go commit takes its position inside the callee, so a test that issues
//     several in order takes a line ticket (Session.TakeLineTicket) for each before it starts the goroutine that runs it.
//   - A caller's continuation (`promise.then(...)`) is the code after the Session call returns on its goroutine; settleOrder
//     records it there.

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// gcNote is a latest, initial-fork conversation document: one key per conversation, and one a deferred commit may hold.
var gcNote = defineDoc("gc.note", 1, latestScope(durable.ForkInitial), func() obj { return delta.JsonObjectOf("n", 0.0) })

var gcShared = defineDoc("gc.shared", 1, sessionScope, func() obj { return delta.JsonObjectOf("n", 0.0) })

// deferredForever is a deferred commit that waits for a reading or writing commit, a barrier, or close to store it.
var deferredForever = func() durable.CommitOptions {
	forever := positiveInfinity
	return durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred, WithinMs: &forever}}
}()

var positiveInfinity = func() float64 { var zero float64; return 1 / zero }()

// heldCall is one storage call held in flight.
type heldCall struct {
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
}

// Entered is closed when the held call arrived.
func (held *heldCall) Entered() <-chan struct{} { return held.entered }

// Release lets the held call go on.
func (held *heldCall) Release() { held.once.Do(func() { close(held.gate) }) }

// batchStorage is controlled storage whose Commit calls are recorded and can be held in flight, whose batches can be refused
// (failed before adoption) and whose calls can reject after their receipts.
type batchStorage struct {
	*sessiontest.ControlledStorage

	mu    sync.Mutex
	calls [][][]durable.StorageWrite
	// refuse refuses the next batch it matches, with refusal.
	refuse  func(writes []durable.StorageWrite) bool
	refusal error
	// rejectCall rejects the call with this index after its batches are stored, with rejection; -1 for none.
	rejectCall int
	rejection  error
	// latency delays each call by the duration it returns.
	latency func() time.Duration
	held    *heldCall
	// taskFailure fails the next Task read.
	taskFailure error
}

func newBatchStorage() *batchStorage {
	return &batchStorage{ControlledStorage: sessiontest.NewControlledStorage(), refusal: errors.New("refused"), rejectCall: -1, rejection: errors.New("connection lost")}
}

// hold holds the next call in flight until released.
func (storage *batchStorage) hold() *heldCall {
	held := &heldCall{entered: make(chan struct{}), gate: make(chan struct{})}
	storage.mu.Lock()
	storage.held = held
	storage.mu.Unlock()
	return held
}

func (storage *batchStorage) callCount() int {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	return len(storage.calls)
}

// lastCall is the newest call's batches.
func (storage *batchStorage) lastCall() [][]durable.StorageWrite {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	return storage.calls[len(storage.calls)-1]
}

// callSizes are the numbers of batches of the last n calls.
func (storage *batchStorage) callSizes(n int) []int {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	var sizes []int
	for _, call := range storage.calls[len(storage.calls)-n:] {
		sizes = append(sizes, len(call))
	}
	return sizes
}

func (storage *batchStorage) setRefuse(refuse func(writes []durable.StorageWrite) bool) {
	storage.mu.Lock()
	storage.refuse = refuse
	storage.mu.Unlock()
}

func (storage *batchStorage) setRejectCall(call int) {
	storage.mu.Lock()
	storage.rejectCall = call
	storage.mu.Unlock()
}

func (storage *batchStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	storage.mu.Lock()
	call := len(storage.calls)
	storage.calls = append(storage.calls, batches)
	held := storage.held
	storage.held = nil
	latency := storage.latency
	storage.mu.Unlock()
	if held != nil {
		close(held.entered)
		<-held.gate
	}
	if latency != nil {
		time.Sleep(latency())
	}
	refuses := func(writes []durable.StorageWrite) bool {
		storage.mu.Lock()
		defer storage.mu.Unlock()
		if storage.refuse == nil || !storage.refuse(writes) {
			return false
		}
		storage.refuse = nil
		return true
	}
	storage.mu.Lock()
	refusal := storage.refusal
	storage.mu.Unlock()
	result, err := sessiontest.RefusingAt(storage.ControlledStorage.Commit, ctx, batches, receive, refuses, refusal)
	if err != nil {
		return result, err
	}
	storage.mu.Lock()
	rejectCall, rejection := storage.rejectCall, storage.rejection
	storage.mu.Unlock()
	if rejectCall == call {
		return durable.CommitResult{}, rejection
	}
	return result, nil
}

func (storage *batchStorage) Task(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	storage.mu.Lock()
	failure := storage.taskFailure
	storage.taskFailure = nil
	storage.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	return storage.ControlledStorage.Task(ctx, id)
}

// scopedStorage is batchStorage with a publication scope (durable.PublishingStorage).
type scopedStorage struct {
	*batchStorage
	scope func(ctx context.Context, publish func())
}

func (storage scopedStorage) Publishing(ctx context.Context, publish func()) {
	storage.scope(ctx, publish)
}

// call is one Session call a test issued from its own goroutine.
type call struct {
	id     chan uint64
	done   chan struct{}
	value  any
	err    error
	goroID uint64
}

// spawn runs fn on a new goroutine, after it extended the line (a line ticket was taken), and returns the call. fn makes its
// Session calls with the context it is given: it carries the call's Continuation, which the Session waits for, so the code that
// follows a call ran (a caller's `promise.then(...)`) before the next part of the storage call that released it is published.
func spawn(fn func(ctx ctxT) (any, error)) *call {
	return spawnContinuing(func(ctx ctxT, _ *session.Continuation) (any, error) { return fn(ctx) })
}

// spawnContinuing is spawn for a fn that says itself when its continuation has run up to a wait that is not the Session's.
func spawnContinuing(fn func(ctx ctxT, continuation *session.Continuation) (any, error)) *call {
	started := &call{id: make(chan uint64, 1), done: make(chan struct{})}
	go func() {
		started.id <- goroutineid.Current()
		callCtx, continuation := session.WithContinuation(ctx)
		started.value, started.err = fn(callCtx, continuation)
		// What follows the calls fn made has run.
		continuation.Entered()
		close(started.done)
	}()
	started.goroID = <-started.id
	return started
}

// enter blocks until the call is parked in a wait or returned.
func (started *call) enter() { entered.Await(started.goroID, started.done) }

// wait blocks until the call returned.
func (started *call) wait() (any, error) {
	<-started.done
	return started.value, started.err
}

// settled reports whether the call returned, once it entered: a parked call has not settled.
func (started *call) settled() bool {
	started.enter()
	select {
	case <-started.done:
		return true
	default:
		return false
	}
}

// quiesce returns once every call entered: upstream's `await flush()`, a macrotask turn in which what can run has run.
func quiesce(calls ...*call) {
	for _, started := range calls {
		started.enter()
	}
}

// awaitQueued returns once n parts are admitted behind the call in flight, which is what upstream's callbacks have all run and
// the three are admitted while the first call is in flight means. A job parked behind the line is not admitted yet, so entering
// its goroutine does not say it.
func (g *gc) awaitQueued(n int) {
	for session.QueuedParts(g.session) < n {
		runtime.Gosched()
	}
}

// awaitHop returns once the writer has published n commits and the Session waits for the caller of the commit it published
// last, to run its continuation, which the test holds, or, if it does not wait for it, once it published more: then the next
// part was published ahead of a continuation that Pi runs before it. The test releases the continuation after it.
func (g *gc) awaitHop(n int) {
	g.awaitHopAt(n, session.AwaitingCallers)
}

// awaitWakeHop is awaitHop for a wait that the publication of the nth commit woke, and the Session waits for it to have resumed.
func (g *gc) awaitWakeHop(n int) {
	g.awaitHopAt(n, session.AwaitingWakes)
}

func (g *gc) awaitHopAt(n int, awaiting func(*session.SessionImpl) int) {
	for {
		published := g.published.len()
		if published > n || published == n && awaiting(g.session) > 0 {
			return
		}
		runtime.Gosched()
	}
}

// awaitHopOrdered is awaitHop for the callers that record in log, one of which the test holds: it returns once the Session waits
// for it, or, if it does not wait, once the caller of a later commit recorded before it.
func (g *gc) awaitHopOrdered(log *order) {
	for len(log.snapshot()) == 0 && session.AwaitingCallers(g.session) == 0 {
		runtime.Gosched()
	}
}

// order records the names of calls in the order their callers resume, a rejection as `name!`.
type order struct {
	mu    sync.Mutex
	names []string
	holds map[string]chan struct{}
}

// hold makes the caller of the call named name slow to run its continuation: it resumes from the Session call and waits for
// release before it records. A Session that waits for the continuation of one caller before it releases the next is the only
// way no later caller records first.
func (recorded *order) hold(name string) (release func()) {
	gate := make(chan struct{})
	recorded.mu.Lock()
	if recorded.holds == nil {
		recorded.holds = map[string]chan struct{}{}
	}
	recorded.holds[name] = gate
	recorded.mu.Unlock()
	return func() { close(gate) }
}

func (recorded *order) record(name string, err error) {
	recorded.mu.Lock()
	gate := recorded.holds[name]
	recorded.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if err != nil {
		name += "!"
	}
	recorded.mu.Lock()
	recorded.names = append(recorded.names, name)
	recorded.mu.Unlock()
}

func (recorded *order) snapshot() []string {
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	return slices.Clone(recorded.names)
}

// gc is a Session over batchStorage with some conversations, and the commits its own observers got.
type gc struct {
	t         testing.TB
	storage   *batchStorage
	session   *session.SessionImpl
	ids       []durable.ConversationId
	published *publishedLog
}

// publishedLog collects what the Session's own observers (FollowCommits) saw.
type publishedLog struct {
	mu      sync.Mutex
	commits []session.ObservedCommit
}

func (log *publishedLog) all() []session.ObservedCommit {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.commits)
}

func (log *publishedLog) len() int { return len(log.all()) }

func (log *publishedLog) reset() {
	log.mu.Lock()
	log.commits = nil
	log.mu.Unlock()
}

// kinds are the kinds of the published commits: stored or memory.
func (log *publishedLog) kinds() []string {
	var kinds []string
	for _, commit := range log.all() {
		if commit.Memory {
			kinds = append(kinds, "memory")
		} else {
			kinds = append(kinds, "stored")
		}
	}
	return kinds
}

// entryKinds are the kinds of the entries the published commits hold, in order.
func (log *publishedLog) entryKinds() []string {
	var kinds []string
	for _, commit := range log.all() {
		if commit.Memory {
			continue
		}
		for _, change := range commit.Changes {
			if entry, ok := change.(durable.EntryWrite); ok {
				kinds = append(kinds, entry.Value.Kind)
			}
		}
	}
	return kinds
}

func retentionOf(ms float64) func() *float64 { return func() *float64 { return &ms } }

// openGC opens a Session over storage and creates conversations ownerless conversations in one commit.
func openGC(t testing.TB, conversations int) *gc {
	t.Helper()
	return openGCHooks(t, newBatchStorage(), conversations, func(storage *batchStorage) durable.Storage { return storage }, session.Hooks{})
}

// openGCHooks is openGC over wrap(storage), with a Session that has hooks.
func openGCHooks(t testing.TB, storage *batchStorage, conversations int, wrap func(*batchStorage) durable.Storage, hooks session.Hooks) *gc {
	t.Helper()
	kernel := session.NewSessionImpl(wrap(storage), hooks, nil, retentionOf(600_000))
	ids, err := kernel.Commit(ctx, func(tx durable.Tx) (any, error) {
		created := make([]durable.ConversationId, 0, conversations)
		for range conversations {
			record, err := tx.CreateConversation(ownerless())
			if err != nil {
				return nil, err
			}
			created = append(created, record.Id)
		}
		return created, nil
	})
	if err != nil {
		t.Fatalf("create conversations: %v", err)
	}
	published := &publishedLog{}
	kernel.FollowCommits(func(_ context.Context, commit session.ObservedCommit) {
		published.mu.Lock()
		published.commits = append(published.commits, commit)
		published.mu.Unlock()
	})
	return &gc{t: t, storage: storage, session: kernel, ids: ids.([]durable.ConversationId), published: published}
}

// commitAsync issues a commit on the Session line now and runs it on its own goroutine; name, when set, records the caller's
// resumption in log.
func (g *gc) commitAsync(change func(tx durable.Tx) (any, error), options ...durable.CommitOptions) *call {
	return g.commitNamed(nil, "", change, options...)
}

func (g *gc) commitNamed(log *order, name string, change func(tx durable.Tx) (any, error), options ...durable.CommitOptions) *call {
	ticket := g.session.TakeLineTicket()
	return spawn(func(ctx ctxT) (any, error) {
		value, err := g.session.CommitWithTicket(ticket, ctx, func(tx *session.Transaction) (any, error) { return change(tx) }, session.TransactionScope{}, options...)
		if log != nil {
			log.record(name, err)
		}
		return value, err
	})
}

// readAsync issues a read job on the line now.
func (g *gc) readAsync(job func(storage durable.Storage) (any, error)) *call {
	return g.readNamed(nil, "", job)
}

func (g *gc) readNamed(log *order, name string, job func(storage durable.Storage) (any, error)) *call {
	ticket := g.session.TakeLineTicket()
	return spawn(func(ctx ctxT) (any, error) {
		value, err := g.session.ReadWithTicketCtx(ticket, ctx, job)
		if log != nil {
			log.record(name, err)
		}
		return value, err
	})
}

// bumpChange increments conversation id's note and returns the value it read.
func bumpChange(id durable.ConversationId) func(tx durable.Tx) (any, error) {
	return func(tx durable.Tx) (any, error) {
		note, err := tx.Doc(gcNote, id)
		if err != nil {
			return nil, err
		}
		n := num(note.Get("n"))
		return n, note.Set("n", n+1)
	}
}

// bump issues the increment of conversation id's note.
func (g *gc) bump(id durable.ConversationId, options ...durable.CommitOptions) *call {
	return g.commitAsync(bumpChange(id), options...)
}

func (g *gc) bumpNamed(log *order, name string, id durable.ConversationId, options ...durable.CommitOptions) *call {
	return g.commitNamed(log, name, bumpChange(id), options...)
}

// appendChange appends an entry of kind to conversation id.
func appendChange(id durable.ConversationId, kind string) func(tx durable.Tx) (any, error) {
	return func(tx durable.Tx) (any, error) {
		entry, err := tx.AppendEntry(id, durable.EntryDraft{Kind: kind})
		return entry, err
	}
}

// notes are the note values a reopened Session over storage reads, per conversation.
func (g *gc) notes(ids ...durable.ConversationId) []float64 {
	g.t.Helper()
	g.storage.Reopen()
	reopened := session.NewSessionImpl(g.storage, session.Hooks{}, nil, nil)
	values := make([]float64, 0, len(ids))
	for _, id := range ids {
		value, err := reopened.SnapshotErased(ctx, gcNote, id)
		if err != nil {
			g.t.Fatalf("snapshot note %d: %v", id, err)
		}
		values = append(values, num(value.Value("n")))
	}
	must(g.t, reopened.Close(ctx))
	return values
}

// closed waits for the Session to end and returns how it ended.
func (g *gc) closed() durable.SessionEnd {
	<-g.session.Closed()
	return g.session.End()
}

func (g *gc) close() {
	g.t.Helper()
	must(g.t, g.session.Close(ctx))
}

// expectOutcomes requires the names in recorded to be exactly want.
func expectNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// isSessionFailed reports whether err is Pi's SessionFailed, and its cause.
func isSessionFailed(err error) (cause error, ok bool) {
	failed, ok := errors.AsType[*durable.SessionFailed](err)
	if !ok {
		return nil, false
	}
	return failed.Cause, true
}
