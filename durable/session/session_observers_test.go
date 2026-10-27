package session_test

// Ports packages/durable/test/session-observers.test.ts: Session.observeDoc(), the synchronous observer of one document
// address that watches are built on. Upstream's three tests that read V8's WeakRef and gc() ("keeps no detached observer
// alive", "keeps no value of a document retired for good, while still attached") have no Go counterpart; what they protect,
// that a detached observer is referenced by nothing of the Session, is checked on the references themselves where Go
// can: the detached observer is no longer pinned (documentCacheSizes.attached) and gets no call.

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

var (
	observeSessionCounter = defineDoc("observe.session-counter", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 0) })
	// A latest, initial-fork conversation document, the kind a deferred commit may hold.
	observeConversationCounter = defineDoc("observe.conversation-counter", 1, latestScope(durable.ForkInitial), func() obj { return delta.JsonObjectOf("count", 0) })
)

// observerCall is one call of a recorded observer: an advance, with whether it ran inside the storage's adopt, or an end.
type observerCall struct {
	advance    *durable.WatchChange[obj]
	adopting   bool
	end        bool
	endFailure error
}

// recordingObserver records each call, and whether it ran inside the storage's adopt.
type recordingObserver struct {
	storage *sessiontest.ControlledStorage
	mu      sync.Mutex
	calls   []observerCall
}

func (observer *recordingObserver) Advance(_ context.Context, change durable.WatchChange[obj]) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.calls = append(observer.calls, observerCall{advance: &change, adopting: observer.storage.Adopting()})
}

func (observer *recordingObserver) End(failure error) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.calls = append(observer.calls, observerCall{end: true, endFailure: failure})
}

func (observer *recordingObserver) all() []observerCall {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]observerCall(nil), observer.calls...)
}

// reportedError is one report a ReportingSession kept.
type reportedError struct {
	err  error
	info durable.ReportInfo
}

type observerSession struct {
	storage *sessiontest.ControlledStorage
	session *session.SessionImpl
	mu      sync.Mutex
	reports []reportedError
}

func (opened *observerSession) reported() []reportedError {
	opened.mu.Lock()
	defer opened.mu.Unlock()
	return append([]reportedError(nil), opened.reports...)
}

// openObserving is a Session that keeps its reports, which a plain Session drops.
func openObserving() *observerSession {
	opened := &observerSession{storage: sessiontest.NewControlledStorage()}
	opened.session = session.NewSessionImpl(opened.storage, session.Hooks{Report: func(_ context.Context, err error, info durable.ReportInfo) {
		opened.mu.Lock()
		opened.reports = append(opened.reports, reportedError{err, info})
		opened.mu.Unlock()
	}}, nil, nil)
	return opened
}

func (opened *observerSession) recorder() *recordingObserver {
	return &recordingObserver{storage: opened.storage}
}

func (opened *observerSession) add(t *testing.T, by int) {
	t.Helper()
	commit(t, opened.session, func(tx durable.Tx) error {
		draft := mustDoc(t, tx, observeSessionCounter)
		return draft.Set("count", int(num(draft.Get("count")))+by)
	})
}

func (opened *observerSession) observe(t *testing.T, observer durable.Observer[obj], token durable.AnyDocToken, args ...any) durable.Observation[obj] {
	t.Helper()
	observation, err := opened.session.ObserveDocErased(ctx, token, observer, args...)
	must(t, err)
	return observation
}

func expectAdvance(t *testing.T, call observerCall, value obj, ops []durable.Op, retired, adopting bool) {
	t.Helper()
	if call.advance == nil || call.end {
		t.Fatalf("call %+v is not an advance", call)
	}
	expectEqual(t, call.advance.Value, value)
	expectEqual(t, call.advance.Ops, ops)
	if call.advance.Retired != retired || call.adopting != adopting {
		t.Fatalf("call %+v: retired %v adopting %v, want retired %v adopting %v", call, call.advance.Retired, call.adopting, retired, adopting)
	}
}

func TestObserveDoc(t *testing.T) {
	t.Run("returns nil for an absent address and attaches nothing", func(t *testing.T) {
		opened := openObserving()
		observer := opened.recorder()
		if observation := opened.observe(t, observer, observeSessionCounter); observation != nil {
			t.Fatalf("observation of an absent address = %v", observation)
		}
		opened.add(t, 1)
		if calls := observer.all(); len(calls) != 0 {
			t.Fatalf("calls %+v", calls)
		}
		if attached := opened.session.DocumentCacheSizes().Attached; attached != 0 {
			t.Fatalf("attached %d", attached)
		}
		must(t, opened.session.Close(ctx))
	})

	t.Run("starts at the committed value and advances inside adopt with each stored commit's ops", func(t *testing.T) {
		opened := openObserving()
		opened.add(t, 1)
		observer := opened.recorder()
		observation := opened.observe(t, observer, observeSessionCounter)
		expectEqual(t, observation.Value(), delta.JsonObjectOf("count", 1))
		opened.add(t, 2)
		calls := observer.all()
		if len(calls) != 1 {
			t.Fatalf("calls %+v", calls)
		}
		expectAdvance(t, calls[0], delta.JsonObjectOf("count", 3), []durable.Op{{"s", []any{"count"}, 3}}, false, true)
		observation.Detach()
		must(t, opened.session.Close(ctx))
	})

	t.Run("advances when memory adopts a held commit, before anything is stored", func(t *testing.T) {
		opened := openObserving()
		conversationId := sessiontest.CreateConversation(t, opened.session)
		commit(t, opened.session, func(tx durable.Tx) error { _, err := tx.Doc(observeConversationCounter, conversationId); return err })
		observer := opened.recorder()
		observation := opened.observe(t, observer, observeConversationCounter, conversationId)
		stored := len(opened.storage.Commits())
		_, err := opened.session.Commit(ctx, func(tx durable.Tx) (any, error) {
			return nil, mustDoc(t, tx, observeConversationCounter, conversationId).Set("count", 5)
		}, within(math.Inf(1)))
		must(t, err)
		if len(opened.storage.Commits()) != stored {
			t.Fatal("a held commit reached Storage")
		}
		calls := observer.all()
		if len(calls) != 1 {
			t.Fatalf("calls %+v", calls)
		}
		expectAdvance(t, calls[0], delta.JsonObjectOf("count", 5), []durable.Op{{"s", []any{"count"}, 5}}, false, false)
		observation.Detach()
		must(t, opened.session.Close(ctx))
	})

	t.Run("follows a conversation document's address: a retirement with the last value, then the new incarnation", func(t *testing.T) {
		opened := openObserving()
		conversationId := sessiontest.CreateConversation(t, opened.session)
		commit(t, opened.session, func(tx durable.Tx) error {
			return mustDoc(t, tx, observeConversationCounter, conversationId).Set("count", 2)
		})
		observer := opened.recorder()
		observation := opened.observe(t, observer, observeConversationCounter, conversationId)
		commit(t, opened.session, func(tx durable.Tx) error { return tx.RetireDoc(observeConversationCounter, conversationId) })
		// The address is absent now: a new attachment finds nothing, the existing one goes on.
		if another := opened.observe(t, opened.recorder(), observeConversationCounter, conversationId); another != nil {
			t.Fatalf("an attachment to the absent address = %v", another)
		}
		commit(t, opened.session, func(tx durable.Tx) error {
			return mustDoc(t, tx, observeConversationCounter, conversationId).Set("count", 9)
		})
		calls := observer.all()
		if len(calls) != 2 {
			t.Fatalf("calls %+v", calls)
		}
		expectAdvance(t, calls[0], observation.Value(), nil, true, true)
		if !same(calls[0].advance.Value, observation.Value()) {
			t.Fatal("a retirement carries the value the observer was given")
		}
		expectAdvance(t, calls[1], delta.JsonObjectOf("count", 9), []durable.Op{{"r", delta.JsonObjectOf("count", 9)}}, false, true)
		observation.Detach()
		must(t, opened.session.Close(ctx))
	})

	t.Run("gets nothing after detach, which is idempotent and unpins the document", func(t *testing.T) {
		opened := openObserving()
		opened.add(t, 1)
		observer := opened.recorder()
		observation := opened.observe(t, observer, observeSessionCounter)
		if attached := opened.session.DocumentCacheSizes().Attached; attached != 1 {
			t.Fatalf("attached %d", attached)
		}
		observation.Detach()
		observation.Detach()
		if attached := opened.session.DocumentCacheSizes().Attached; attached != 0 {
			t.Fatalf("attached %d after detach", attached)
		}
		opened.add(t, 1)
		must(t, opened.session.Close(ctx))
		if calls := observer.all(); len(calls) != 0 {
			t.Fatalf("calls %+v", calls)
		}
	})

	t.Run("ends once when the Session closes", func(t *testing.T) {
		opened := openObserving()
		opened.add(t, 1)
		observer := opened.recorder()
		opened.observe(t, observer, observeSessionCounter)
		must(t, opened.session.Close(ctx))
		calls := observer.all()
		if len(calls) != 1 || !calls[0].end || calls[0].endFailure != nil {
			t.Fatalf("calls %+v", calls)
		}
	})

	t.Run("ends with the error that failed the Session", func(t *testing.T) {
		opened := openObserving()
		opened.add(t, 1)
		observer := opened.recorder()
		opened.observe(t, observer, observeSessionCounter)
		failure := errors.New("disk gone")
		opened.storage.FailNextCommit(failure)
		expectErrorContains(t, tryCommit(opened.session, func(tx durable.Tx) error {
			return mustDoc(t, tx, observeSessionCounter).Set("count", 2)
		}), "disk gone")
		<-opened.session.Closed()
		calls := observer.all()
		if len(calls) != 1 || !calls[0].end || !errors.Is(calls[0].endFailure, failure) {
			t.Fatalf("calls %+v", calls)
		}
	})

	t.Run("reports a throwing observer, detaches it and ends it with the error; the commit and other observers go on", func(t *testing.T) {
		opened := openObserving()
		opened.add(t, 1)
		bug := errors.New("observer bug")
		throwing := &funcObserver{advance: func(durable.WatchChange[obj]) { panic(bug) }}
		opened.observe(t, throwing, observeSessionCounter)
		healthy := opened.recorder()
		opened.observe(t, healthy, observeSessionCounter)
		opened.add(t, 1)
		opened.add(t, 1)
		reports := opened.reported()
		if len(reports) != 1 || !errors.Is(reports[0].err, bug) || reports[0].info.Type != durable.ReportListenerError {
			t.Fatalf("reports %+v", reports)
		}
		if ends := throwing.endsSeen(); len(ends) != 1 || !errors.Is(ends[0], bug) {
			t.Fatalf("ends %v", ends)
		}
		if len(healthy.all()) != 2 {
			t.Fatalf("healthy calls %+v", healthy.all())
		}
		expectEqual(t, snapshot(t, opened.session, observeSessionCounter), delta.JsonObjectOf("count", 3))
		must(t, opened.session.Close(ctx))
		// Detached at its throw: the close does not end it again.
		if ends := throwing.endsSeen(); len(ends) != 1 {
			t.Fatalf("ends after close %v", ends)
		}
		if opened.session.Failure() != nil {
			t.Fatalf("session failure %v", opened.session.Failure())
		}
	})

	t.Run("attaches atomically on the line: a commit queued before the attachment is in its value", func(t *testing.T) {
		opened := openObserving()
		opened.add(t, 1)
		observer := opened.recorder()
		gate := opened.storage.HoldCommits()
		committing := make(chan struct{})
		go func() {
			defer close(committing)
			opened.add(t, 10)
		}()
		waitClosed(t, gate.Entered())
		type attached struct {
			observation durable.Observation[obj]
			err         error
		}
		result := make(chan attached, 1)
		go func() {
			observation, err := opened.session.ObserveDocErased(ctx, observeSessionCounter, observer)
			result <- attached{observation, err}
		}()
		// The attachment is queued behind the commit on the line.
		for opened.session.LineJobs() < 2 {
		}
		gate.Release()
		got := <-result
		must(t, got.err)
		<-committing
		expectEqual(t, got.observation.Value(), delta.JsonObjectOf("count", 11))
		if calls := observer.all(); len(calls) != 0 {
			t.Fatalf("calls %+v", calls)
		}
		got.observation.Detach()
		must(t, opened.session.Close(ctx))
	})

	t.Run("rejects an attachment whose context is cancelled, and attaches nothing", func(t *testing.T) {
		opened := openObserving()
		opened.add(t, 1)
		cancelled, cancel := context.WithCancelCause(ctx)
		cancel(errors.New("gone"))
		_, err := opened.session.ObserveDocErased(cancelled, observeSessionCounter, opened.recorder())
		expectErrorContains(t, err, "gone")
		if attached := opened.session.DocumentCacheSizes().Attached; attached != 0 {
			t.Fatalf("attached %d", attached)
		}
		must(t, opened.session.Close(ctx))
	})

	t.Run("rejects, never panics, on a closed Session or a missing scope ID", func(t *testing.T) {
		opened := openObserving()
		if _, err := opened.session.ObserveDocErased(ctx, observeConversationCounter, opened.recorder()); err == nil {
			t.Fatal("a missing scope ID was accepted")
		}
		must(t, opened.session.Close(ctx))
		_, err := opened.session.ObserveDocErased(ctx, observeSessionCounter, opened.recorder())
		expectErrorContains(t, err, "Session is closed")
	})
}

// funcObserver is an observer made of functions.
type funcObserver struct {
	advance func(durable.WatchChange[obj])
	mu      sync.Mutex
	ends    []error
}

func (observer *funcObserver) Advance(_ context.Context, change durable.WatchChange[obj]) {
	if observer.advance != nil {
		observer.advance(change)
	}
}

func (observer *funcObserver) End(failure error) {
	observer.mu.Lock()
	observer.ends = append(observer.ends, failure)
	observer.mu.Unlock()
}

func (observer *funcObserver) endsSeen() []error {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]error(nil), observer.ends...)
}

func TestObserveDocDetachedDuringACommit(t *testing.T) {
	// retiring is a Session with a conversation counter at 1, and the commit that retires it and creates it again at 2.
	retiring := func(t *testing.T) (*observerSession, durable.ConversationId, func()) {
		opened := openObserving()
		conversationId := sessiontest.CreateConversation(t, opened.session)
		commit(t, opened.session, func(tx durable.Tx) error {
			return mustDoc(t, tx, observeConversationCounter, conversationId).Set("count", 1)
		})
		recreate := func() {
			commit(t, opened.session, func(tx durable.Tx) error {
				must(t, tx.RetireDoc(observeConversationCounter, conversationId))
				return mustDoc(t, tx, observeConversationCounter, conversationId).Set("count", 2)
			})
		}
		return opened, conversationId, recreate
	}

	t.Run("gets nothing more of the commit once it panics on the retirement, and pins nothing for the recreation", func(t *testing.T) {
		opened, conversationId, recreate := retiring(t)
		bug := errors.New("observer bug")
		var mu sync.Mutex
		var changes []durable.WatchChange[obj]
		observer := &funcObserver{advance: func(change durable.WatchChange[obj]) {
			mu.Lock()
			changes = append(changes, change)
			mu.Unlock()
			panic(bug)
		}}
		observation := opened.observe(t, observer, observeConversationCounter, conversationId)
		recreate()
		mu.Lock()
		if len(changes) != 1 || !changes[0].Retired || len(changes[0].Ops) != 0 || !same(changes[0].Value, observation.Value()) {
			t.Fatalf("changes %+v", changes)
		}
		mu.Unlock()
		if ends := observer.endsSeen(); len(ends) != 1 || !errors.Is(ends[0], bug) {
			t.Fatalf("ends %v", ends)
		}
		if reports := opened.reported(); len(reports) != 1 || !errors.Is(reports[0].err, bug) || reports[0].info.Type != durable.ReportListenerError {
			t.Fatalf("reports %+v", reports)
		}
		if attached := opened.session.DocumentCacheSizes().Attached; attached != 0 {
			t.Fatalf("attached %d", attached)
		}
		observation.Detach()
		if attached := opened.session.DocumentCacheSizes().Attached; attached != 0 {
			t.Fatalf("attached %d after detach", attached)
		}
		must(t, opened.session.Close(ctx))
	})

	t.Run("gets nothing more of the commit once it detaches in the retirement call, and pins nothing", func(t *testing.T) {
		opened, conversationId, recreate := retiring(t)
		var mu sync.Mutex
		var changes []durable.WatchChange[obj]
		var observation durable.Observation[obj]
		var attachedObservation sync.WaitGroup
		attachedObservation.Add(1)
		observer := &funcObserver{}
		observer.advance = func(change durable.WatchChange[obj]) {
			mu.Lock()
			changes = append(changes, change)
			mu.Unlock()
			observation.Detach()
		}
		observation = opened.observe(t, observer, observeConversationCounter, conversationId)
		recreate()
		mu.Lock()
		if len(changes) != 1 || !changes[0].Retired || !same(changes[0].Value, observation.Value()) {
			t.Fatalf("changes %+v", changes)
		}
		mu.Unlock()
		if attached := opened.session.DocumentCacheSizes().Attached; attached != 0 {
			t.Fatalf("attached %d", attached)
		}
		must(t, opened.session.Close(ctx))
		if ends := observer.endsSeen(); len(ends) != 0 {
			t.Fatalf("ends %v", ends)
		}
	})

	t.Run("gets nothing of the commit when another observer's call detached it first, and pins nothing", func(t *testing.T) {
		opened, conversationId, recreate := retiring(t)
		var second durable.Observation[obj]
		first := opened.observe(t, &funcObserver{advance: func(durable.WatchChange[obj]) { second.Detach() }}, observeConversationCounter, conversationId)
		detached := opened.recorder()
		second = opened.observe(t, detached, observeConversationCounter, conversationId)
		recreate()
		if calls := detached.all(); len(calls) != 0 {
			t.Fatalf("calls %+v", calls)
		}
		// Only the first observer pins the new incarnation; its detach leaves nothing pinned.
		first.Detach()
		if attached := opened.session.DocumentCacheSizes().Attached; attached != 0 {
			t.Fatalf("attached %d", attached)
		}
		must(t, opened.session.Close(ctx))
	})
}
