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
//     committed frame into delivery before the publishing commit returns, and
//     a Harness wait awaits the deliveries scheduled before it resumes its
//     caller (AwaitDeliveriesEntered): upstream's drain microtask enters the
//     listener before the code awaiting the commit's promise resumes. A state
//     attachment drains on the committing goroutine after the Session line is
//     free and before the commit returns.
//   - An AbortSignal is the context's cancellation; withoutAbortSignal is
//     context.WithoutCancel, which keeps context values.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
)

// ErrSessionClosed is upstream's "Session is closed".
var ErrSessionClosed = errors.New("Session is closed")

var _ durable.Session = (*SessionImpl)(nil)

// Hooks are the extension points a Harness overrides on its Session.
type Hooks struct {
	// ConversationCreated runs inside every transaction that creates or forks a conversation, after the
	// conversation record is staged. A plain Session stages nothing.
	ConversationCreated func(tx *Transaction, record durable.ConversationRecord) error
	// BeforeClose runs after close seals admission and before the line closes Storage.
	BeforeClose func()
	// Report receives the Session's failure and the errors of listeners, which never fail what ran them; info says
	// which. A plain Session drops them.
	Report func(err error, info durable.ReportInfo)
	// SetTimer arms the timers that evict idle documents (HarnessOptions.SetTimer); nil selects Go timers.
	SetTimer durable.SetTimer
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
	// deferMu guards the publication in progress: its flag and the work listeners deferred past the listener loop.
	deferMu    sync.Mutex
	publishing bool
	deferred   []func()
	storage    *guardedStorage
	hooks      Hooks
	timers     durable.Timers
	// documentTimersUnavailable is set once the host declined a background timer: then none is kept, and the end of
	// each commit evicts what expired. Guarded by mu.
	documentTimersUnavailable bool
	host                      *transactionHost
	scheduler                 *pending

	lineMu   sync.Mutex
	tail     chan struct{}
	lineJobs int

	mu        sync.Mutex
	documents map[string]*LoadedDocument
	// cache is the retention bookkeeping of documents.
	cache documentCache
	// now is the wall clock in milliseconds; retentionSource is the document retention setting, read at each use.
	now             func() float64
	retentionSource func() *float64
	commitOrder     []*commitListener
	// internalOrder is the Session's own listeners, such as its scheduler's: they keep memory in step with storage, so a
	// failure in one fails the Session. They run before the host's.
	internalOrder []*commitListener
	closeOrder    []*closeListener
	closing       chan struct{}
	// listenersRan is closed once the close listeners ran; set together with closing.
	listenersRan chan struct{}
	closeErr     error
	// failureErr is the first error that failed the Session; failed is closed when it is set.
	failureErr error
	failed     chan struct{}
	// closed is closed once the Session ended, after close reached the backend; end is then set.
	closed chan struct{}
	end    durable.SessionEnd
}

// CreateSessionOptions is the optional argument of CreateSession.
type CreateSessionOptions struct {
	// Now is the wall clock in milliseconds for task times; the default is the system clock.
	Now func() float64
	// DocumentRetentionMs is how long a loaded document that no watch or document state observes stays in memory after
	// its last read or write; the next access loads it from Storage. Observed documents always stay. Nil selects the
	// default, ten minutes; 0 drops a document right after use; +Inf keeps documents. Anything else that is not a
	// number of milliseconds, 0 or more, means the default.
	DocumentRetentionMs *float64
	// SetTimer arms the timers that evict idle documents; nil selects Go timers (see durable.SetTimer).
	SetTimer durable.SetTimer
}

// CreateSession opens a Session kernel over one storage backend.
func CreateSession(storage durable.Storage, options ...CreateSessionOptions) *SessionImpl {
	var now func() float64
	var retention func() *float64
	var setTimer durable.SetTimer
	if len(options) > 0 {
		now = options[0].Now
		setTimer = options[0].SetTimer
		if ms := options[0].DocumentRetentionMs; ms != nil {
			retention = func() *float64 { return ms }
		}
	}
	return NewSessionImpl(storage, Hooks{SetTimer: setTimer}, now, retention)
}

// NewSessionImpl opens a Session kernel with extension hooks. now is the wall clock in milliseconds for task times;
// nil selects the system clock. documentRetentionMs is read at each use and yields the document retention (see
// CreateSessionOptions.DocumentRetentionMs); nil selects the default.
func NewSessionImpl(storage durable.Storage, hooks Hooks, now func() float64, documentRetentionMs func() *float64) *SessionImpl {
	if now == nil {
		now = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	session := &SessionImpl{hooks: hooks, scheduler: newPending(), documents: map[string]*LoadedDocument{}, cache: newDocumentCache(), now: now, retentionSource: documentRetentionMs, failed: make(chan struct{}), closed: make(chan struct{})}
	session.timers = durable.HostTimers(hooks.SetTimer, func(err error) {
		session.report(err, durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackSetTimer})
	})
	session.storage = newGuardedStorage(storage, session)
	session.host = &transactionHost{
		storage: session.storage,
		now:     now,
		cached: func(addressId string) *LoadedDocument {
			session.mu.Lock()
			cached := session.documents[addressId]
			session.mu.Unlock()
			if cached != nil {
				session.touchDocument(addressId)
			}
			return cached
		},
		load: session.loadDocument,
		install: func(document *LoadedDocument) {
			session.mu.Lock()
			session.documents[document.AddressId] = document
			session.mu.Unlock()
			session.touchDocument(document.AddressId)
		},
		evict: func(addressId string, recordId durable.DocumentId) {
			session.mu.Lock()
			if cached := session.documents[addressId]; cached != nil && cached.Record.Id == recordId {
				session.dropDocumentLocked(addressId)
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
// passed admission is always queued ahead of the close job and settles before Storage closes. admitted, when non-nil,
// runs once the job holds its ticket and before it waits for earlier jobs: the point at which upstream's synchronous
// caller has extended the line's Promise chain.
func enqueue[T any](session *SessionImpl, admit bool, admitted func(), job func() (T, error)) (T, error) {
	session.lineMu.Lock()
	if admit {
		if listenersRan, err := session.usable(); err != nil {
			// Wait for the close listeners without the line mutex: a listener may need a lock whose holder is taking a
			// line ticket (the Harness scheduler's seal and kick).
			session.lineMu.Unlock()
			if listenersRan != nil {
				<-listenersRan
			}
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
	if admitted != nil {
		admitted()
	}
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
	listenersRan, err := session.usable()
	if listenersRan != nil {
		<-listenersRan
	}
	return err
}

// usable is assertUsable without the wait: once close began it returns the channel the rejection waits on.
func (session *SessionImpl) usable() (listenersRan <-chan struct{}, err error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closing != nil {
		if failure := session.assertHealthyLocked(); failure != nil {
			return session.listenersRan, failure
		}
		return session.listenersRan, ErrSessionClosed
	}
	return nil, session.assertHealthyLocked()
}

func (session *SessionImpl) assertHealthy() error {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.assertHealthyLocked()
}

func (session *SessionImpl) assertHealthyLocked() error {
	if session.failureErr != nil {
		return durable.NewSessionFailed(session.failureErr)
	}
	return nil
}

// Storage returns the Session's Storage behind its failure guard; every component of the Session reads and writes
// through it, also the reads off the Session line. A Harness passes it to its scheduler and context reads.
func (session *SessionImpl) Storage() durable.Storage { return session.storage.exposed() }

// failure returns the error that failed the Session, nil while it has not failed.
func (session *SessionImpl) failure() error {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.failureErr
}

// Failure returns the first error that failed the Session, or nil.
func (session *SessionImpl) Failure() error { return session.failure() }

// Failed returns a channel closed the moment the Session fails, before work underway has ended.
func (session *SessionImpl) Failed() <-chan struct{} { return session.failed }

// Closed returns a channel closed once the Session ended: after close reached the backend, or a failure ended it.
func (session *SessionImpl) Closed() <-chan struct{} { return session.closed }

// End returns how the Session ended once Closed is closed.
func (session *SessionImpl) End() durable.SessionEnd {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.end
}

// listenerInfo is the default source of a report: a listener, which never fails what ran it (session.ts LISTENER).
var listenerInfo = durable.ReportInfo{Type: durable.ReportListenerError}

// Report is session.ts report with its default info: it hands a non-fatal failure, such as the error of a state or
// watch listener, to the host; a plain Session drops it.
func (session *SessionImpl) Report(err error) { session.report(err, listenerInfo) }

// ReportAs is session.ts report with info: it hands a non-fatal failure to the host; a plain Session drops it.
func (session *SessionImpl) ReportAs(err error, info durable.ReportInfo) { session.report(err, info) }

// reportListener hands the error of a listener to the host.
func (session *SessionImpl) reportListener(err error) { session.report(err, listenerInfo) }

// report hands the Session's failure, or the error of a listener, to the host; a plain Session drops it.
func (session *SessionImpl) report(err error, info durable.ReportInfo) {
	if session.hooks.Report != nil {
		session.hooks.Report(err, info)
	}
}

// Fail fails the Session with err: the first wins, and it is reported once. Admission ends, and the close listeners run
// now; they read the error from Failure. The Storage guard calls it; the Harness also calls it for an error in its
// scheduler's own commits.
func (session *SessionImpl) Fail(err error) { session.fail(err) }

func (session *SessionImpl) fail(err error) {
	session.mu.Lock()
	if session.failureErr != nil {
		session.mu.Unlock()
		return
	}
	session.failureErr = err
	close(session.failed)
	session.mu.Unlock()
	// Seal first, so a report handler that calls back finds the Session failed. Closing runs the close listeners
	// synchronously; a failing backend close is the caller's to see, and here nobody waits.
	//portlint:allow ctxbackground session.ts's guarded-storage failure starts close with no caller to cancel it; nobody waits on this close
	session.beginClose(context.WithoutCancel(context.Background()))
	session.report(err, durable.ReportInfo{Type: durable.ReportSessionFailed})
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
	result, err := enqueue(session, true, lineadmission.From(ctx), func() (any, error) { return session.runCommit(ctx, change, scope) })
	session.scheduler.runDrains()
	return result, err
}

// WriteAtClose is for BeforeClose (session.ts writeAtClose): on the line, after every commit admitted before the seal has
// settled, it writes what writes returns then straight to Storage. Nothing is published, since the Session is closing; a
// failed Session writes nothing.
func (session *SessionImpl) WriteAtClose(ctx context.Context, writes func() []durable.StorageWrite) error {
	_, err := enqueue(session, false, nil, func() (struct{}, error) {
		if session.failure() != nil {
			return struct{}{}, nil
		}
		if batch := writes(); len(batch) > 0 {
			_, err := session.storage.Commit(context.WithoutCancel(ctx), batch)
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

// LineTicket is a position on the mutation line taken before its job runs.
type LineTicket struct {
	session  *SessionImpl
	previous chan struct{}
	done     chan struct{}
}

// TakeLineTicket takes the next line position now, or returns nil once close began.
func (session *SessionImpl) TakeLineTicket() *LineTicket {
	session.lineMu.Lock()
	defer session.lineMu.Unlock()
	session.mu.Lock()
	closing := session.closing != nil
	session.mu.Unlock()
	if closing {
		return nil
	}
	ticket := &LineTicket{session: session, previous: session.tail, done: make(chan struct{})}
	session.tail = ticket.done
	session.lineJobs++
	return ticket
}

// Release gives up the position. Jobs queued behind it keep waiting for the jobs queued before it.
func (ticket *LineTicket) Release() {
	if ticket.previous != nil {
		<-ticket.previous
	}
	ticket.session.lineMu.Lock()
	ticket.session.lineJobs--
	ticket.session.lineMu.Unlock()
	close(ticket.done)
}

// CommitWithTicket runs a commit at the ticket's position and releases it.
func (session *SessionImpl) CommitWithTicket(ticket *LineTicket, ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope) (any, error) {
	if ticket == nil {
		return session.CommitWith(ctx, change, scope)
	}
	result, err := func() (any, error) {
		defer ticket.Release()
		if ticket.previous != nil {
			<-ticket.previous
		}
		if err := session.assertUsable(); err != nil {
			return nil, err
		}
		return session.runCommit(ctx, change, scope)
	}()
	session.scheduler.runDrains()
	return result, err
}

// ReadWithTicket runs a read-only job at the ticket's position on the mutation line and releases it.
func (session *SessionImpl) ReadWithTicket(ticket *LineTicket, job func() (any, error)) (any, error) {
	if ticket == nil {
		return session.ReadOnLine(job)
	}
	defer ticket.Release()
	if ticket.previous != nil {
		<-ticket.previous
	}
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	return job()
}

// ReadOnLine runs a read-only job on the mutation line so multi-read derivations observe one committed state.
func (session *SessionImpl) ReadOnLine(job func() (any, error)) (any, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	return enqueue(session, true, nil, func() (any, error) {
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
	if cached != nil && cached.ValueVersion == definition.Version {
		session.touchDocument(resolved.Id)
	}
	loaded := cached
	if cached == nil || cached.ValueVersion != definition.Version {
		loaded, err = enqueue(session, true, nil, func() (*LoadedDocument, error) {
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
func (session *SessionImpl) DocumentStateErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.DocumentState[durable.JsonObject], error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		return nil, err
	}
	return enqueue(session, true, nil, func() (*chord.AttachedReplicatedState[durable.JsonObject], error) {
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
		state, err := chord.AttachReplicatedStateSource[durable.JsonObject](source, chord.ReplicatedStateSourceOptions{OnError: session.reportListener})
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
	watch, err := enqueue(session, true, nil, func() (*CommittedWatch[durable.JsonObject], error) {
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
			watch = NewCommittedWatch(session, value, release, nil)
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
	return enqueue(session, true, nil, func() (durable.JsonObject, error) {
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
	closing := session.beginClose(ctx)
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

// beginClose seals admission and runs the close listeners synchronously the first time it is called, and starts the
// close that settles admitted work before Storage closes; it returns the channel closed when that close ended.
func (session *SessionImpl) beginClose(ctx context.Context) <-chan struct{} {
	session.mu.Lock()
	if session.closing == nil {
		closing := make(chan struct{})
		listenersRan := make(chan struct{})
		session.closing = closing
		session.listenersRan = listenersRan
		if sweep := session.cache.sweep; sweep != nil {
			if sweep.disarm != nil {
				sweep.disarm()
			}
			session.cache.sweep = nil
		}
		listeners := session.closeOrder
		session.closeOrder = nil
		session.mu.Unlock()
		// Seal admission before anything else runs, then stop observers; admitted work settles before Storage
		// closes. Upstream runs the listeners synchronously and beforeClose on a later microtask, and the close
		// chain proceeds even when a listener throws.
		cleanup := context.WithoutCancel(ctx)
		//portlint:allow asyncorder joined: Close waits on closing, and listenersRan orders beforeClose after the synchronous listeners as session.ts:483-497 runs beforeClose on a later microtask
		go func() {
			defer close(closing)
			<-listenersRan
			if session.hooks.BeforeClose != nil {
				session.hooks.BeforeClose()
			}
			_, err := enqueue(session, false, nil, func() (struct{}, error) {
				session.mu.Lock()
				session.commitOrder = nil
				session.internalOrder = nil
				clear(session.documents)
				session.cache.clearIdle()
				clear(session.cache.attached)
				session.mu.Unlock()
				return struct{}{}, session.storage.Close(cleanup)
			})
			session.mu.Lock()
			session.closeErr = err
			reported := false
			// A Storage that cannot close is a failed one; an earlier failure stays the cause.
			if err != nil && session.failureErr == nil {
				session.failureErr = err
				reported = true
			}
			if session.failureErr == nil {
				session.end = durable.SessionEnd{Reason: durable.SessionEndClosed}
			} else {
				session.end = durable.SessionEnd{Reason: durable.SessionEndFailed, Error: session.failureErr}
			}
			session.mu.Unlock()
			if reported {
				session.report(err, durable.ReportInfo{Type: durable.ReportSessionFailed})
			}
			close(session.closed)
		}()
		func() {
			defer close(listenersRan)
			for _, listener := range listeners {
				contained(listener.listener, session.reportListener)
			}
		}()
		session.mu.Lock()
	}
	closing := session.closing
	session.mu.Unlock()
	return closing
}

// Contained is contained for the Harness: it runs a callback that must not fail its caller and hands a panic to onError.
// Ports session.ts contained.
func Contained(callback func(), onError func(error)) { contained(callback, onError) }

// contained runs a host callback that must not fail its caller: a panic goes to onError, which is not awaited.
func contained(callback func(), onError func(error)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				onError(err)
				return
			}
			onError(fmt.Errorf("%v", recovered))
		}
	}()
	callback()
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

// ObserveCommits is SubscribeCommits for the Session's own components, whose state follows each commit. A panic leaves
// that state behind storage, so it fails the Session instead of being reported. They run before the host's listeners.
func (session *SessionImpl) ObserveCommits(listener func(ctx context.Context, publication durable.CommitPublication)) func() {
	if err := session.assertUsable(); err != nil {
		panic(err)
	}
	entry := &commitListener{listener: listener}
	session.mu.Lock()
	session.internalOrder = append(session.internalOrder, entry)
	session.mu.Unlock()
	return func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		for index, candidate := range session.internalOrder {
			if candidate == entry {
				session.internalOrder = append(session.internalOrder[:index:index], session.internalOrder[index+1:]...)
				return
			}
		}
	}
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
	_, err := enqueue(session, false, nil, func() (struct{}, error) {
		session.mu.Lock()
		clear(session.documents)
		session.cache.clearIdle()
		session.mu.Unlock()
		return struct{}{}, nil
	})
	return err
}

// RunDeliveries runs the state drains that publishing queued, on the calling goroutine: the drain microtask of a publication
// that no commit's end ran, such as a task graph refresh a read on the line made (session.ts queueMicrotask of
// SessionSourceAttachment.publish).
func (session *SessionImpl) RunDeliveries() { session.scheduler.runDrains() }

// WaitDeliveries blocks until every scheduled watch and state delivery has drained.
func (session *SessionImpl) WaitDeliveries() { session.scheduler.wait() }

// AwaitDeliveriesEntered blocks until every watch delivery scheduled before the call has entered: its listener returned,
// is parked in a wait, or is blocked behind a lock, which a Go listener does where an upstream one awaits and which the
// caller may hold. A Harness wait that resumes its caller calls it, as upstream's drain microtask enters the listener
// before the code awaiting the wait's promise resumes; the listener a caller's own goroutine runs is not waited for, so
// a listener may itself wait for the Harness. It returns the cause of ctx when ctx ends the wait.
func (session *SessionImpl) AwaitDeliveriesEntered(ctx context.Context) error {
	return session.scheduler.awaitEntered(ctx)
}

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
			session.sweepDocuments()
		}
	}()
	result, err = change(tx)
	settled = true
	if err == nil {
		// A callback that caught a failed read must not commit, nor succeed as if it had.
		err = session.assertHealthy()
	}
	if err != nil {
		tx.settleFailure()
		session.sweepDocuments()
		return nil, err
	}
	writes, err := tx.settleSuccess()
	if err != nil {
		// Settlement aborted every change already.
		session.sweepDocuments()
		return nil, err
	}
	if len(writes) == 0 {
		tx.discard()
		session.sweepDocuments()
		return result, nil
	}
	// Once admitted, caller cancellation does not interrupt Storage settlement.
	seq, err := session.storage.Commit(context.WithoutCancel(ctx), writes)
	if err != nil {
		// The guard has failed the Session.
		tx.discard()
		return nil, err
	}
	documents, err := tx.adopt(seq)
	if err != nil {
		// Storage already committed; a failed adoption leaves memory behind durable state.
		session.fail(err)
		return nil, err
	}
	session.touchChanged(documents)
	session.publish(ctx, seq, writes, documents)
	session.sweepDocuments()
	return result, nil
}

func (session *SessionImpl) publish(ctx context.Context, seq durable.Seq, writes []durable.StorageWrite, documents []durable.DocumentCommitChange) {
	session.mu.Lock()
	internal := append([]*commitListener(nil), session.internalOrder...)
	listeners := append([]*commitListener(nil), session.commitOrder...)
	session.mu.Unlock()
	if len(listeners) == 0 && len(internal) == 0 {
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
	session.deferMu.Lock()
	session.publishing = true
	session.deferred = nil
	session.deferMu.Unlock()
	// The commit is durable: a failing listener neither fails the commit nor skips the others. Waiters that earlier
	// listeners settled wake once the loop ends, and later publications start clean.
	defer func() {
		session.deferMu.Lock()
		deferred := session.deferred
		session.publishing, session.deferred = false, nil
		session.deferMu.Unlock()
		for _, run := range deferred {
			run()
		}
	}()
	for _, listener := range internal {
		contained(func() { listener.listener(ctx, publication) }, session.fail)
	}
	for _, listener := range listeners {
		contained(func() { listener.listener(ctx, publication) }, session.reportListener)
	}
}

// DeferUntilPublished runs run once every listener of the publication in progress has run, and at once when none is.
// Upstream settles a waiter by resolving a promise inside a listener, and the awaiting code resumes after the
// synchronous listener loop; a goroutine woken inside a listener would race the listeners that follow, so waiters
// wake through this. Listeners call it from the publishing goroutine, as publications are serialized on the line.
func (session *SessionImpl) DeferUntilPublished(run func()) {
	session.deferMu.Lock()
	if !session.publishing {
		session.deferMu.Unlock()
		run()
		return
	}
	session.deferred = append(session.deferred, run)
	session.deferMu.Unlock()
}

// documentObserver is a committed state source or watch attached to one incarnation.
type documentObserver interface {
	Advance(ctx context.Context, value durable.JsonObject, ops []durable.Op)
	CloseSession(failure error)
	// frameContext strips cancellation for a state, whose frames carry none; a watch observes its own.
	frameContext(ctx context.Context) context.Context
}

type stateObserver struct {
	source *CommittedStateSource[durable.JsonObject]
}

func (observer stateObserver) Advance(ctx context.Context, value durable.JsonObject, ops []durable.Op) {
	observer.source.Advance(ctx, value, ops)
}
func (observer stateObserver) CloseSession(error) { observer.source.CloseSession() }
func (stateObserver) frameContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

type watchObserver struct {
	watch *CommittedWatch[durable.JsonObject]
}

func (observer watchObserver) Advance(ctx context.Context, value durable.JsonObject, ops []durable.Op) {
	observer.watch.Advance(ctx, value, ops)
}
func (observer watchObserver) CloseSession(failure error)              { observer.watch.CloseSession(failure) }
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
	// Attached, the incarnation stays cached; once its last observer detaches, or it retires, it is idle again.
	unpin := session.pinDocument(loaded.AddressId, loaded.Record.Id)
	detach := func() {
		detachMu.Lock()
		commit, closer := unsubscribeCommit, unsubscribeClose
		detachMu.Unlock()
		commit()
		closer()
		unpin()
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
			// A retired incarnation never changes again: its observers keep their last value but pin nothing.
			if document.Value == nil {
				unpin()
			}
			observer.Advance(observer.frameContext(ctx), document.Value, ops)
		}
	})
	if err != nil {
		unpin()
		return nil, err
	}
	closer, err := session.subscribeClose(func() { observer.CloseSession(session.failure()) })
	if err != nil {
		commit()
		unpin()
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
		session.touchDocument(addressId)
		return cached, nil
	}
	if cached != nil {
		session.dropDocumentLocked(addressId)
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
	session.touchDocument(addressId)
	return loaded, nil
}
