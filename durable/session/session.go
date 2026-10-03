// Package session ports the pi-durable Session kernel: one mutation line,
// the loaded document tracker cache, transactions, document forks, and
// committed publication and observation.
//
// Ports packages/durable/src/session/session.ts
//
// Go mapping decisions:
//   - Upstream's Promise chain of line jobs is a FIFO ticket queue: each job
//     waits for its predecessor's completion, so jobs run in admission order.
//   - Deliveries upstream schedules with queueMicrotask run on a goroutine the
//     Session tracks. A started watch with no callback in flight takes the
//     committed frame into delivery before the publishing commit returns, as
//     upstream's drain microtask enters the listener before the commit's
//     caller resumes. A state attachment drains on the committing goroutine
//     after the Session line is free and before the commit returns.
//   - An AbortSignal is the context's cancellation; withoutAbortSignal is
//     context.WithoutCancel, which keeps context values.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// PoisonedError rejects every operation after a commit failed once Storage
// admitted it: memory may be behind durable state.
type PoisonedError struct{ Cause error }

func (err *PoisonedError) Error() string {
	return "Session is poisoned by a failed commit after storage admission; reopen it"
}

// Unwrap returns the failure that poisoned the Session.
func (err *PoisonedError) Unwrap() error { return err.Cause }

// errSessionClosed is upstream's "Session is closed".
var errSessionClosed = errors.New("Session is closed")

var _ durable.Session = (*SessionImpl)(nil)

// Hooks are the extension points a Harness overrides on its Session.
type Hooks struct {
	// ConversationCreated runs inside every transaction that creates or forks a conversation, after the
	// conversation record is staged. A plain Session stages nothing.
	ConversationCreated func(tx *Transaction, record durable.ConversationRecord) error
	// BeforeClose runs after close seals admission and before the line closes Storage.
	BeforeClose func()
}

type commitListener struct {
	listener func(ctx context.Context, publication durable.CommitPublication)
}

type closeListener struct{ listener func() }

// SessionImpl is the Session kernel: one mutation line, the loaded document
// tracker cache, and committed publication.
//
// Only committed state is observable. Every commit callback, preparation,
// Storage settlement, adoption, and publication runs while the line is held;
// asynchronous listeners run later.
type SessionImpl struct {
	storage   durable.Storage
	hooks     Hooks
	host      *transactionHost
	scheduler *pending

	lineMu   sync.Mutex
	tail     chan struct{}
	lineJobs int

	mu          sync.Mutex
	documents   map[string]*LoadedDocument
	commitOrder []*commitListener
	closeOrder  []*closeListener
	closing     chan struct{}
	// listenersRan is closed once the close listeners ran; set together with closing.
	listenersRan chan struct{}
	closeErr     error
	poison       error
}

// CreateSession opens a Session kernel over one storage backend.
func CreateSession(storage durable.Storage) *SessionImpl { return NewSessionImpl(storage, Hooks{}) }

// NewSessionImpl opens a Session kernel with extension hooks.
func NewSessionImpl(storage durable.Storage, hooks Hooks) *SessionImpl {
	session := &SessionImpl{storage: storage, hooks: hooks, scheduler: newPending(), documents: map[string]*LoadedDocument{}}
	session.host = &transactionHost{
		storage: storage,
		cached: func(addressId string) *LoadedDocument {
			session.mu.Lock()
			defer session.mu.Unlock()
			return session.documents[addressId]
		},
		load: session.loadDocument,
		install: func(document *LoadedDocument) {
			session.mu.Lock()
			session.documents[document.AddressId] = document
			session.mu.Unlock()
		},
		evict: func(addressId string, recordId durable.DocumentId) {
			session.mu.Lock()
			if cached := session.documents[addressId]; cached != nil && cached.Record.Id == recordId {
				delete(session.documents, addressId)
			}
			session.mu.Unlock()
		},
		conversationCreated: func(tx *Transaction, record durable.ConversationRecord) error {
			if hooks.ConversationCreated == nil {
				return nil
			}
			return hooks.ConversationCreated(tx, record)
		},
	}
	return session
}

// enqueue runs job on the mutation line after every previously admitted job. With admit, the Session must be usable
// when the job takes its ticket: upstream checks #assertUsable and enqueues in one synchronous turn, so a job that
// passed admission is always queued ahead of the close job and settles before Storage closes.
func enqueue[T any](session *SessionImpl, admit bool, job func() (T, error)) (T, error) {
	session.lineMu.Lock()
	if admit {
		if err := session.assertUsable(); err != nil {
			session.lineMu.Unlock()
			var zero T
			return zero, err
		}
	}
	previous := session.tail
	done := make(chan struct{})
	session.tail = done
	session.lineJobs++
	session.lineMu.Unlock()
	defer func() {
		session.lineMu.Lock()
		session.lineJobs--
		session.lineMu.Unlock()
		close(done)
	}()
	if previous != nil {
		<-previous
	}
	return job()
}

// assertUsable rejects an operation once close began. Upstream sets the closing flag and runs the close listeners in
// one synchronous turn, so no rejection is observable before the listeners ran; the Harness scheduler reads its own
// closing state, which its listener sets, to decide whether to report a rejected commit. Here a rejection waits for the
// listeners, which must not call Session operations.
func (session *SessionImpl) assertUsable() error {
	session.mu.Lock()
	closing, listenersRan := session.closing, session.listenersRan
	if closing != nil {
		session.mu.Unlock()
		<-listenersRan
		return errSessionClosed
	}
	defer session.mu.Unlock()
	return session.assertHealthyLocked()
}

func (session *SessionImpl) assertHealthy() error {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.assertHealthyLocked()
}

func (session *SessionImpl) assertHealthyLocked() error {
	if session.poison != nil {
		return &PoisonedError{Cause: session.poison}
	}
	return nil
}

// Commit runs one atomic transaction on the Session mutation line.
func (session *SessionImpl) Commit(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
	return session.CommitWith(ctx, func(tx *Transaction) (any, error) { return change(tx) }, TransactionScope{})
}

// CommitWith is the internal commit exposing the concrete transaction and its internal operations, such as the
// reserved-ID root bootstrap and task replacement. scope sets the default task conversation and the task attributed
// to appended entries.
func (session *SessionImpl) CommitWith(ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope) (any, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	result, err := enqueue(session, true, func() (any, error) { return session.runCommit(ctx, change, scope) })
	session.scheduler.runDrains()
	return result, err
}

// ReadOnLine runs a read-only job on the mutation line so multi-read derivations observe one committed state.
func (session *SessionImpl) ReadOnLine(job func() (any, error)) (any, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	return enqueue(session, true, func() (any, error) {
		if err := session.assertHealthy(); err != nil {
			return nil, err
		}
		return job()
	})
}

// LineDocument is a conversation document's current incarnation and value.
type LineDocument struct {
	Record  durable.DocumentRecord
	Version int
	Value   durable.JsonObject
}

// ConversationDocumentOnLine returns a conversation document's current incarnation and value for a job already
// running on the line (see ReadOnLine); nil when absent.
func (session *SessionImpl) ConversationDocumentOnLine(ctx context.Context, token durable.AnyDocToken, conversationId durable.ConversationId) (*LineDocument, error) {
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, []any{conversationId})
	if err != nil {
		return nil, err
	}
	loaded, err := session.loadDocument(ctx, definition, resolved.Id, resolved.Address)
	if err != nil || loaded == nil {
		return nil, err
	}
	if err := checkLoaded(definition, loaded); err != nil {
		return nil, err
	}
	return &LineDocument{Record: loaded.Record, Version: loaded.ValueVersion, Value: loaded.Tracker.Value()}, nil
}

func checkLoaded(definition *durable.AnyDocDefinition, loaded *LoadedDocument) error {
	if err := durable.CheckRecordScope(definition, loaded.Record.AsCreate()); err != nil {
		return err
	}
	return durable.CheckRecordVersion(definition, loaded.Record.AsCreate(), loaded.StoredVersion())
}

// SnapshotErased returns the committed value of a document, or nil when absent. It never creates the document.
func (session *SessionImpl) SnapshotErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.JsonObject, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	cached := session.documents[resolved.Id]
	session.mu.Unlock()
	loaded := cached
	if cached == nil || cached.ValueVersion != definition.Version {
		loaded, err = enqueue(session, true, func() (*LoadedDocument, error) {
			if err := session.assertHealthy(); err != nil {
				return nil, err
			}
			return session.loadDocument(ctx, definition, resolved.Id, resolved.Address)
		})
		if err != nil {
			return nil, err
		}
	}
	if loaded == nil {
		return nil, nil
	}
	if err := checkLoaded(definition, loaded); err != nil {
		return nil, err
	}
	return loaded.Tracker.Value(), nil
}

// DocumentStateErased returns a disposable read-only state of a document's committed value, or nil when absent.
func (session *SessionImpl) DocumentStateErased(ctx context.Context, token durable.AnyDocToken, args ...any) (*chord.AttachedReplicatedState[durable.JsonObject], error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		return nil, err
	}
	return enqueue(session, true, func() (*chord.AttachedReplicatedState[durable.JsonObject], error) {
		if err := session.assertHealthy(); err != nil {
			return nil, err
		}
		loaded, err := session.loadDocument(ctx, definition, resolved.Id, resolved.Address)
		if err != nil || loaded == nil {
			return nil, err
		}
		var source *CommittedStateSource[durable.JsonObject]
		detach, err := session.attachDocument(definition, loaded, func(value durable.JsonObject, release func()) documentObserver {
			source = newCommittedStateSource(value, release, session.scheduler)
			return stateObserver{source}
		})
		if err != nil {
			return nil, err
		}
		state, err := chord.AttachReplicatedStateSource[durable.JsonObject](source, chord.ReplicatedStateSourceOptions{})
		if err != nil {
			detach()
			return nil, err
		}
		return state, nil
	})
}

// WatchDocErased returns a watch of a document's committed value, or nil when absent. Cancelling ctx before the
// watch is returned rejects with the cancellation cause; afterwards it ends the watch with reason cancelled.
func (session *SessionImpl) WatchDocErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.WatchHandle[durable.JsonObject], error) {
	watch, err := session.watchDoc(ctx, token, args)
	if err != nil || watch == nil {
		return nil, err
	}
	return watch, nil
}

func (session *SessionImpl) watchDoc(ctx context.Context, token durable.AnyDocToken, args []any) (*CommittedWatch[durable.JsonObject], error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		return nil, err
	}
	watch, err := enqueue(session, true, func() (*CommittedWatch[durable.JsonObject], error) {
		if err := session.assertHealthy(); err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		loaded, err := session.loadDocument(ctx, definition, resolved.Id, resolved.Address)
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		if loaded == nil {
			return nil, nil
		}
		var watch *CommittedWatch[durable.JsonObject]
		_, err = session.attachDocument(definition, loaded, func(value durable.JsonObject, release func()) documentObserver {
			watch = newCommittedWatch(value, release, nil, session.scheduler)
			return watchObserver{watch}
		})
		if err != nil {
			return nil, err
		}
		return watch, nil
	})
	if err != nil || watch == nil {
		return nil, err
	}
	if ctx.Err() != nil {
		watch.Cancel()
		return nil, context.Cause(ctx)
	}
	if ctx.Done() != nil {
		watch.ObserveCancellation(ctx)
	}
	return watch, nil
}

// SnapshotAsOfErased returns the committed value of a rewindable conversation document as of the visible entry at;
// args are the conversation ID, then the family key for families.
func (session *SessionImpl) SnapshotAsOfErased(ctx context.Context, token durable.AnyDocToken, at durable.EntryId, args ...any) (durable.JsonObject, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		return nil, err
	}
	if resolved.Address.Scope.Kind != durable.ScopeConversation {
		return nil, errors.New("Session.snapshotAsOf() requires a conversation document")
	}
	conversationId := resolved.Address.Scope.ConversationId
	return enqueue(session, true, func() (durable.JsonObject, error) {
		if err := session.assertHealthy(); err != nil {
			return nil, err
		}
		storedEntry, err := session.storage.VisibleEntry(ctx, conversationId, at)
		if err != nil {
			return nil, err
		}
		if storedEntry == nil {
			return nil, fmt.Errorf("Entry %d is not visible from conversation %d", at, conversationId)
		}
		address := resolved.Address
		address.Scope = durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: storedEntry.Entry.ConversationId}
		point := durable.AtSeq(storedEntry.CommitSeq)
		record, err := session.storage.FindDocument(ctx, address, point)
		if err != nil || record == nil {
			return nil, err
		}
		stored, err := session.storage.Document(ctx, record.Id, point)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, fmt.Errorf("Historical document %d (%s) cannot be read", record.Id, record.Kind)
		}
		return materializeValue(definition, stored.Record.AsCreate(), stored.Version, stored.Value)
	})
}

// Close seals admission, settles admitted commits, then closes Storage. Close listeners run synchronously when close
// begins, before the BeforeClose hook. Cancelling ctx stops waiting without interrupting the close; an already
// cancelled ctx returns its cause at once, as upstream's awaitWithContext rejects for an aborted signal.
func (session *SessionImpl) Close(ctx context.Context) error {
	session.mu.Lock()
	if session.closing == nil {
		closing := make(chan struct{})
		listenersRan := make(chan struct{})
		session.closing = closing
		session.listenersRan = listenersRan
		listeners := session.closeOrder
		session.closeOrder = nil
		session.mu.Unlock()
		// Seal admission before anything else runs, then stop observers; admitted work settles before Storage
		// closes. Upstream runs the listeners synchronously and beforeClose on a later microtask, and the close
		// chain proceeds even when a listener throws.
		cleanup := context.WithoutCancel(ctx)
		go func() {
			defer close(closing)
			<-listenersRan
			if session.hooks.BeforeClose != nil {
				session.hooks.BeforeClose()
			}
			_, err := enqueue(session, false, func() (struct{}, error) {
				session.mu.Lock()
				session.commitOrder = nil
				clear(session.documents)
				session.mu.Unlock()
				return struct{}{}, session.storage.Close(cleanup)
			})
			session.mu.Lock()
			session.closeErr = err
			session.mu.Unlock()
		}()
		func() {
			defer close(listenersRan)
			for _, listener := range listeners {
				listener.listener()
			}
		}()
		session.mu.Lock()
	}
	closing := session.closing
	session.mu.Unlock()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	select {
	case <-closing:
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.closeErr
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// SubscribeCommits registers a synchronous post-adoption listener. It must not panic, block, or call Session
// operations.
func (session *SessionImpl) SubscribeCommits(listener func(ctx context.Context, publication durable.CommitPublication)) func() {
	unsubscribe, err := session.subscribeCommits(listener)
	if err != nil {
		panic(err)
	}
	return unsubscribe
}

func (session *SessionImpl) subscribeCommits(listener func(ctx context.Context, publication durable.CommitPublication)) (func(), error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	entry := &commitListener{listener: listener}
	session.mu.Lock()
	session.commitOrder = append(session.commitOrder, entry)
	session.mu.Unlock()
	return func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		for index, candidate := range session.commitOrder {
			if candidate == entry {
				session.commitOrder = append(session.commitOrder[:index:index], session.commitOrder[index+1:]...)
				return
			}
		}
	}, nil
}

// CommitSubscriptions reports the number of live commit subscriptions. Upstream tests count them by wrapping
// subscribeCommits (harness-lifecycle.test.ts:331); a Go method cannot be patched.
func (session *SessionImpl) CommitSubscriptions() int {
	session.mu.Lock()
	defer session.mu.Unlock()
	return len(session.commitOrder)
}

// LineJobs reports the number of admitted line jobs that have not finished, so a caller can wait until work is queued
// behind a held job without sleeping.
func (session *SessionImpl) LineJobs() int {
	session.lineMu.Lock()
	defer session.lineMu.Unlock()
	return session.lineJobs
}

// SubscribeClose registers a listener called synchronously when close begins. It must not panic, block, or call
// Session operations.
func (session *SessionImpl) SubscribeClose(listener func()) func() {
	unsubscribe, err := session.subscribeClose(listener)
	if err != nil {
		panic(err)
	}
	return unsubscribe
}

func (session *SessionImpl) subscribeClose(listener func()) (func(), error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	entry := &closeListener{listener: listener}
	session.mu.Lock()
	session.closeOrder = append(session.closeOrder, entry)
	session.mu.Unlock()
	return func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		for index, candidate := range session.closeOrder {
			if candidate == entry {
				session.closeOrder = append(session.closeOrder[:index:index], session.closeOrder[index+1:]...)
				return
			}
		}
	}, nil
}

// UnloadDocuments drops every loaded tracker on the mutation line; later access cold-loads from Storage.
func (session *SessionImpl) UnloadDocuments() error {
	_, err := enqueue(session, false, func() (struct{}, error) {
		session.mu.Lock()
		clear(session.documents)
		session.mu.Unlock()
		return struct{}{}, nil
	})
	return err
}

// WaitDeliveries blocks until every scheduled watch and state delivery has drained.
func (session *SessionImpl) WaitDeliveries() { session.scheduler.wait() }

func (session *SessionImpl) runCommit(ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope) (result any, err error) {
	if err := session.assertHealthy(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	tx := newTransaction(session.host, ctx, scope)
	settled := false
	defer func() {
		if !settled {
			// The callback panicked: settle like a rejection, then keep panicking.
			tx.settleFailure()
		}
	}()
	result, err = change(tx)
	settled = true
	if err != nil {
		tx.settleFailure()
		return nil, err
	}
	writes, err := tx.settleSuccess()
	if err != nil {
		return nil, err
	}
	if len(writes) == 0 {
		tx.discard()
		return result, nil
	}
	// Once admitted, caller cancellation does not interrupt Storage settlement.
	seq, err := session.storage.Commit(context.WithoutCancel(ctx), writes)
	if err != nil {
		tx.discard()
		// Callback errors never reach this branch; StorageRejected alone guarantees that no batch effect committed.
		if _, ok := errors.AsType[*durable.StorageRejected](err); !ok {
			session.setPoison(err)
		}
		return nil, err
	}
	documents, err := tx.adopt(seq)
	if err != nil {
		// Storage already committed; a failed adoption leaves memory behind durable state.
		session.setPoison(err)
		return nil, err
	}
	session.publish(ctx, seq, writes, documents)
	return result, nil
}

func (session *SessionImpl) setPoison(err error) {
	session.mu.Lock()
	session.poison = err
	session.mu.Unlock()
}

func (session *SessionImpl) publish(ctx context.Context, seq durable.Seq, writes []durable.StorageWrite, documents []durable.DocumentCommitChange) {
	session.mu.Lock()
	listeners := append([]*commitListener(nil), session.commitOrder...)
	session.mu.Unlock()
	if len(listeners) == 0 {
		return
	}
	changes := make([]durable.CommitChange, 0, len(writes)+len(documents))
	for _, staged := range writes {
		if table, ok := staged.(durable.TableCommitChange); ok {
			changes = append(changes, table)
		}
	}
	for _, document := range documents {
		changes = append(changes, document)
	}
	publication := durable.CommitPublication{Seq: seq, Changes: changes}
	for _, listener := range listeners {
		listener.listener(ctx, publication)
	}
}

// documentObserver is a committed state source or watch attached to one incarnation.
type documentObserver interface {
	Advance(ctx context.Context, value durable.JsonObject, ops []durable.Op)
	CloseSession()
	// frameContext strips cancellation for a state, whose frames carry none; a watch observes its own.
	frameContext(ctx context.Context) context.Context
}

type stateObserver struct {
	source *CommittedStateSource[durable.JsonObject]
}

func (observer stateObserver) Advance(ctx context.Context, value durable.JsonObject, ops []durable.Op) {
	observer.source.Advance(ctx, value, ops)
}
func (observer stateObserver) CloseSession() { observer.source.CloseSession() }
func (stateObserver) frameContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

type watchObserver struct {
	watch *CommittedWatch[durable.JsonObject]
}

func (observer watchObserver) Advance(ctx context.Context, value durable.JsonObject, ops []durable.Op) {
	observer.watch.Advance(ctx, value, ops)
}
func (observer watchObserver) CloseSession()                           { observer.watch.CloseSession() }
func (watchObserver) frameContext(ctx context.Context) context.Context { return ctx }

// attachDocument attaches an observer to one committed incarnation: check the definition, then forward this
// incarnation's committed changes and close. The returned detach removes both subscriptions.
func (session *SessionImpl) attachDocument(definition *durable.AnyDocDefinition, loaded *LoadedDocument, create func(value durable.JsonObject, release func()) documentObserver) (func(), error) {
	if err := checkLoaded(definition, loaded); err != nil {
		return nil, err
	}
	var (
		detachMu          sync.Mutex
		unsubscribeCommit = func() {}
		unsubscribeClose  = func() {}
	)
	detach := func() {
		detachMu.Lock()
		commit, closer := unsubscribeCommit, unsubscribeClose
		detachMu.Unlock()
		commit()
		closer()
	}
	observer := create(loaded.Tracker.Value(), detach)
	observed := loaded.ValueVersion
	recordId := loaded.Record.Id
	commit, err := session.subscribeCommits(func(ctx context.Context, publication durable.CommitPublication) {
		for _, change := range publication.Changes {
			document, ok := change.(durable.DocumentChange)
			if !ok || document.Record.Id != recordId {
				continue
			}
			ops := observedOperations(&observed, document)
			// A migration-only base changes nothing for an observer of the new version.
			if len(ops) == 0 {
				continue
			}
			observer.Advance(observer.frameContext(ctx), document.Value, ops)
		}
	})
	if err != nil {
		return nil, err
	}
	closer, err := session.subscribeClose(observer.CloseSession)
	if err != nil {
		commit()
		return nil, err
	}
	detachMu.Lock()
	unsubscribeCommit, unsubscribeClose = commit, closer
	detachMu.Unlock()
	return detach, nil
}

// observedOperations returns the operations an observer applies for one committed change. An observer hydrated
// under another definition version holds a differently shaped value, so it receives the new value as a root
// replacement instead of operations for that shape.
func observedOperations(observed *int, change durable.DocumentChange) []durable.Op {
	if change.Value == nil {
		return retirementOperations()
	}
	if *change.Version == *observed {
		return change.Ops
	}
	*observed = *change.Version
	return []durable.Op{{"r", change.Value}}
}

func (session *SessionImpl) loadDocument(ctx context.Context, definition *durable.AnyDocDefinition, addressId string, address durable.DocumentAddress) (*LoadedDocument, error) {
	session.mu.Lock()
	cached := session.documents[addressId]
	// A tracker serves only tokens of the version its value was materialized for; others reload from Storage.
	if cached != nil && cached.ValueVersion == definition.Version {
		session.mu.Unlock()
		return cached, nil
	}
	if cached != nil {
		delete(session.documents, addressId)
	}
	session.mu.Unlock()
	record, err := session.storage.FindDocument(ctx, address, durable.CurrentPoint)
	if err != nil || record == nil {
		return nil, err
	}
	stored, err := session.storage.Document(ctx, record.Id, durable.CurrentPoint)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("Current document %d (%s) cannot be read", record.Id, record.Kind)
	}
	value, err := materializeValue(definition, stored.Record.AsCreate(), stored.Version, stored.Value)
	if err != nil {
		return nil, err
	}
	loaded := &LoadedDocument{
		AddressId:       addressId,
		Record:          stored.Record,
		ValueVersion:    definition.Version,
		Tracker:         delta.Track(value),
		storedVersion:   stored.Version,
		deltasSinceBase: stored.DeltasSinceBase,
	}
	session.mu.Lock()
	session.documents[addressId] = loaded
	session.mu.Unlock()
	return loaded, nil
}
