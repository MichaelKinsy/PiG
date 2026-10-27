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
//     listener before the code awaiting the commit's promise resumes.
//   - A stored commit is published inside Storage's adopt, as upstream does:
//     its telemetry events and the Session's own observers run there. The one
//     microtask upstream queues for subscribeCommits listeners, and the waits
//     and aborts the observers deferred (DeferUntilPublished), whose
//     continuations run after it, form one publication that runs when the
//     storage's synchronous step ends: on a goroutine armed by adopt as soon
//     as the adopting goroutine is parked in a wait (a storage that works after
//     adopt), or when Storage.Commit returns to the Session's guard
//     (finishPublication), which then waits for it. The Session never sees a
//     commit resolve before its publication ran.
//   - An AbortSignal is the context's cancellation; withoutAbortSignal is
//     context.WithoutCancel, which keeps context values.
package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
	"github.com/MichaelKinsy/PiG/telemetry"
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
	//
	// ctx carries the telemetry span the report is raised under (chord/context.Telemetry); context.Background() has none.
	Report func(ctx context.Context, err error, info durable.ReportInfo)
	// SetTimer arms the timers that evict idle documents (HarnessOptions.SetTimer); nil selects Go timers.
	SetTimer durable.SetTimer
	// ReadOnly refuses every commit with writes (HarnessOptions.ReadOnly).
	ReadOnly bool
	// PersistWithinMs is the default window of a deferred commit, read at each use (HarnessOptions.settings.progress.persistWithinMs);
	// nil selects the default, 1,000 ms.
	PersistWithinMs func() *float64
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
	// deferMu guards pub, the publication in progress.
	deferMu sync.Mutex
	pub     *publication
	storage *guardedStorage
	hooks   Hooks
	timers  durable.Timers
	// documentTimersUnavailable is set once the host declined a background timer: then none is kept, and the end of
	// each commit evicts what expired. Guarded by mu.
	documentTimersUnavailable bool
	host                      *transactionHost
	scheduler                 *chord.DeliveryScheduler

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
	internalOrder []*internalListener
	// holds are the documents with held changes (durable.CommitPersistence), by incarnation (record ID), each pinned in the
	// cache until they are stored; holdOrder is the same in the order they were first held. holdTimer is the armed store.
	holds      map[durable.DocumentId]*heldDocument
	holdOrder  []*heldDocument
	holdTimer  *holdTimer
	closeOrder []*closeListener
	closing    chan struct{}
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
	// DocumentRetentionMs is how long a loaded document that no watch observes stays in memory after
	// its last read or write; the next access loads it from Storage. Observed documents always stay. Nil selects the
	// default, ten minutes; 0 drops a document right after use; +Inf keeps documents. Anything else that is not a
	// number of milliseconds, 0 or more, means the default.
	DocumentRetentionMs *float64
	// SetTimer arms the timers that evict idle documents; nil selects Go timers (see durable.SetTimer).
	SetTimer durable.SetTimer
	// PersistWithinMs is the default window of a deferred commit (durable.CommitPersistence): how long it may stay in
	// memory before it is stored. Nil selects the default, 1,000 ms; +Inf waits for a reading or writing commit or close.
	// Anything else that is not a number of milliseconds, 0 or more, means the default.
	PersistWithinMs *float64
}

// CreateSession opens a Session kernel over one storage backend.
func CreateSession(storage durable.Storage, options ...CreateSessionOptions) *SessionImpl {
	var now func() float64
	var retention func() *float64
	var persistWithin func() *float64
	var setTimer durable.SetTimer
	if len(options) > 0 {
		now = options[0].Now
		setTimer = options[0].SetTimer
		if ms := options[0].DocumentRetentionMs; ms != nil {
			retention = func() *float64 { return ms }
		}
		if ms := options[0].PersistWithinMs; ms != nil {
			persistWithin = func() *float64 { return ms }
		}
	}
	return NewSessionImpl(storage, Hooks{SetTimer: setTimer, PersistWithinMs: persistWithin}, now, retention)
}

// NewSessionImpl opens a Session kernel with extension hooks. now is the wall clock in milliseconds for task times;
// nil selects the system clock. documentRetentionMs is read at each use and yields the document retention (see
// CreateSessionOptions.DocumentRetentionMs); nil selects the default.
func NewSessionImpl(storage durable.Storage, hooks Hooks, now func() float64, documentRetentionMs func() *float64) *SessionImpl {
	if now == nil {
		now = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	session := &SessionImpl{hooks: hooks, scheduler: chord.NewDeliveryScheduler(), documents: map[string]*LoadedDocument{}, holds: map[durable.DocumentId]*heldDocument{}, cache: newDocumentCache(), now: now, retentionSource: documentRetentionMs, failed: make(chan struct{}), closed: make(chan struct{})}
	session.timers = durable.HostTimers(hooks.SetTimer, func(err error) {
		session.report(unscoped, err, durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackSetTimer})
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

// unscoped is the context of a report raised where upstream's `report(error, info)` has no context argument: it carries no
// telemetry span, so the report is counted and no event is recorded.
//
//portlint:allow ctxbackground a report with no caller context records no span event; nothing is started from it
var unscoped = context.Background()

// listenerInfo is the default source of a report: a listener, which never fails what ran it (session.ts LISTENER).
var listenerInfo = durable.ReportInfo{Type: durable.ReportListenerError}

// Report is session.ts report with its default info: it hands a non-fatal failure, such as the error of a state or
// watch listener, to the host; a plain Session drops it.
func (session *SessionImpl) Report(err error) {
	session.report(unscoped, err, listenerInfo)
}

// ReportAs is session.ts report with info: it hands a non-fatal failure to the host; a plain Session drops it.
func (session *SessionImpl) ReportAs(ctx context.Context, err error, info durable.ReportInfo) {
	session.report(ctx, err, info)
}

// reportListener hands the error of a listener to the host.
func (session *SessionImpl) reportListener(err error) {
	session.report(unscoped, err, listenerInfo)
}

// reportListenerIn hands the error of a commit listener to the host under the telemetry of the commit's context.
func (session *SessionImpl) reportListenerIn(ctx context.Context) func(error) {
	return func(err error) { session.report(ctx, err, listenerInfo) }
}

// report hands the Session's failure, or the error of a listener, to the host; a plain Session drops it.
func (session *SessionImpl) report(ctx context.Context, err error, info durable.ReportInfo) {
	if session.hooks.Report != nil {
		session.hooks.Report(ctx, err, info)
	}
}

// Fail fails the Session with err: the first wins, and it is reported once. Admission ends, and the close listeners run
// now; they read the error from Failure. The Storage guard calls it; the Harness also calls it for an error in its
// scheduler's own commits.
func (session *SessionImpl) Fail(ctx context.Context, err error) { session.failIn(ctx, err) }

func (session *SessionImpl) fail(err error) { session.failIn(unscoped, err) }

// failIn fails the Session and reports it under the telemetry of ctx, which is where upstream's `fail(error, context)` records it.
func (session *SessionImpl) failIn(ctx context.Context, err error) {
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
	//portlint:allow ctxmapping the close is not the caller's work: ctx only names the span the failure is reported under, and may hold the failing commit's line ticket
	session.beginClose(context.WithoutCancel(context.Background()))
	session.report(ctx, err, durable.ReportInfo{Type: durable.ReportSessionFailed})
}

// Commit runs one atomic transaction on the Session mutation line. options.Persistence may defer storing it
// (durable.CommitPersistence).
func (session *SessionImpl) Commit(ctx context.Context, change func(tx durable.Tx) (any, error), options ...durable.CommitOptions) (any, error) {
	return session.CommitWith(ctx, func(tx *Transaction) (any, error) { return change(tx) }, TransactionScope{}, options...)
}

// admitCommit is the admission check of a commit: the Session must be usable, and the options valid. It returns how long
// the commit may stay in memory before it is stored; nil when it is immediate.
func (session *SessionImpl) admitCommit(options []durable.CommitOptions) (*float64, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	single, err := durable.SingleCommitOptions(options)
	if err != nil {
		return nil, err
	}
	return session.deferral(single.Persistence), nil
}

// CommitWith is the internal commit exposing the concrete transaction and its internal operations, such as the
// reserved-ID root bootstrap and task replacement. scope sets the default task conversation and the task attributed
// to appended entries.
func (session *SessionImpl) CommitWith(ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope, options ...durable.CommitOptions) (any, error) {
	withinMs, err := session.admitCommit(options)
	if err != nil {
		return nil, err
	}
	result, err := enqueue(session, true, lineadmission.From(ctx), func() (any, error) { return session.runCommit(ctx, change, scope, withinMs) })
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
			_, err := session.storage.Commit(context.WithoutCancel(ctx), batch, func(durable.Seq) {})
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
func (session *SessionImpl) CommitWithTicket(ticket *LineTicket, ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope, options ...durable.CommitOptions) (any, error) {
	if ticket == nil {
		return session.CommitWith(ctx, change, scope, options...)
	}
	result, err := func() (any, error) {
		defer ticket.Release()
		if ticket.previous != nil {
			<-ticket.previous
		}
		withinMs, err := session.admitCommit(options)
		if err != nil {
			return nil, err
		}
		return session.runCommit(ctx, change, scope, withinMs)
	}()
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

// WatchDocErased returns a watch of a document's committed value, or nil when absent. The watch follows the document's
// address (kind, scope, and key) from the incarnation found at acquisition: a retirement is a call with Retired set, the last
// value the watch was given and no ops, and a document created there later arrives on the same watch. Cancelling ctx before
// the watch is returned rejects with the cancellation cause; afterwards it ends the watch with reason cancelled.
func (session *SessionImpl) WatchDocErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.WatchHandle[durable.JsonObject], error) {
	watch, err := session.watchDoc(ctx, token, args)
	if err != nil || watch == nil {
		return nil, err
	}
	return watch, nil
}

// watchObserver is a document watch as the observer of its address: the Session feeds it, and its end is the watch's.
type watchObserver struct {
	watch *chord.Watch[durable.JsonObject]
}

func (observer watchObserver) Advance(ctx context.Context, change durable.WatchChange[durable.JsonObject]) {
	observer.watch.Advance(ctx, change)
}

func (observer watchObserver) End(failure error) { observer.watch.SourceEnded(failure) }

func (session *SessionImpl) watchDoc(ctx context.Context, token durable.AnyDocToken, args []any) (*chord.Watch[durable.JsonObject], error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		return nil, err
	}
	return attachDocument(session, ctx, definition, resolved, func(loaded *LoadedDocument) (*chord.Watch[durable.JsonObject], error) {
		observer, err := session.observeDocument(definition, loaded, func(value durable.JsonObject, detach func(), _ func(error)) durable.Observer[durable.JsonObject] {
			return watchObserver{NewSessionWatch(session, value, detach)}
		}, func(observer durable.Observer[durable.JsonObject]) { observer.(watchObserver).watch.EndAfterDelivery() })
		if err != nil {
			return nil, err
		}
		watch := observer.(watchObserver).watch
		if ctx.Done() != nil {
			watch.ObserveCancellation(ctx)
		}
		return watch, nil
	}, (*chord.Watch[durable.JsonObject]).Cancel)
}

// ObserveDocErased attaches a synchronous observer to a document address, atomically on the Session line, as
// WatchDocErased acquires a watch; the observation is nil when the address is absent. ctx governs only the attachment.
func (session *SessionImpl) ObserveDocErased(ctx context.Context, token durable.AnyDocToken, observer durable.Observer[durable.JsonObject], args ...any) (durable.Observation[durable.JsonObject], error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		return nil, err
	}
	return attachDocument(session, ctx, definition, resolved, func(loaded *LoadedDocument) (durable.Observation[durable.JsonObject], error) {
		var guarded *GuardedObserver[durable.JsonObject]
		_, err := session.observeDocument(definition, loaded, func(_ durable.JsonObject, detach func(), report func(error)) durable.Observer[durable.JsonObject] {
			guarded = GuardObserver(observer, detach, report)
			return guarded
		}, func(observer durable.Observer[durable.JsonObject]) { observer.End(nil) })
		if err != nil {
			return nil, err
		}
		return NewObservation(loaded.Tracker.Value(), guarded), nil
	}, durable.Observation[durable.JsonObject].Detach)
}

// attachDocument runs, on the line as one job, the load of the document at resolved and attach to it, so the attachment
// sees every commit after the value it starts from; it returns the zero value when the document is absent. Cancelling ctx
// meanwhile rejects with the cancellation cause, after cancel undid an attachment made.
func attachDocument[A comparable](session *SessionImpl, ctx context.Context, definition *durable.AnyDocDefinition, resolved durable.ResolvedAddress, attach func(*LoadedDocument) (A, error), cancel func(A)) (A, error) {
	var none A
	attached, err := enqueue(session, true, nil, func() (A, error) {
		if err := session.assertHealthy(); err != nil {
			return none, err
		}
		if ctx.Err() != nil {
			return none, context.Cause(ctx)
		}
		loaded, err := session.loadDocument(ctx, definition, resolved.Id, resolved.Address)
		if err != nil {
			return none, err
		}
		if ctx.Err() != nil {
			return none, context.Cause(ctx)
		}
		if loaded == nil {
			return none, nil
		}
		return attach(loaded)
	})
	if err != nil || attached == none {
		return none, err
	}
	if ctx.Err() != nil {
		cancel(attached)
		return none, context.Cause(ctx)
	}
	return attached, nil
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
		if timer := session.holdTimer; timer != nil {
			if timer.disarm != nil {
				timer.disarm()
			}
			session.holdTimer = nil
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
				// Last, after the writes at close (BeforeClose), while the trackers are still here. A failed Session writes
				// nothing; a failing store fails it, and the backend still closes.
				var unstored error
				if session.failure() == nil {
					unstored = session.storeHolds(cleanup, session.allHolds())
				}
				session.mu.Lock()
				session.commitOrder = nil
				session.internalOrder = nil
				clear(session.documents)
				session.cache.clearIdle()
				clear(session.cache.attached)
				clear(session.holds)
				session.holdOrder = nil
				session.mu.Unlock()
				if err := session.storage.Close(cleanup); err != nil {
					return struct{}{}, err
				}
				return struct{}{}, unstored
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
				session.report(unscoped, err, durable.ReportInfo{Type: durable.ReportSessionFailed})
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

// SubscribeCommits registers a synchronous listener for every stored commit. It runs once adopt returned (at once when the
// storage then waits, otherwise when Storage.Commit returns), on the line, before every wait the commit resolves and every abort it queues (in no order with watch listeners); a commit
// adopted while no listener was registered has no dispatch. It must not panic, block, or call Session operations.
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
				session.commitOrder = slices.Delete(session.commitOrder, index, index+1)
				return
			}
		}
	}, nil
}

// ObserveCommits observes every commit as memory adopts it (ObservedCommit), for the Session's own components, whose state
// follows memory. A panic leaves that state behind, so it fails the Session instead of being reported: once Storage.Commit
// returned, after the other observers and the host listeners got the commit. For a stored commit they run inside Storage's
// adopt, so they call no Storage method, run no task code, and contain every host callback; the waits they resolve and the
// aborts they queue (DeferUntilPublished) run after the host listeners.
func (session *SessionImpl) ObserveCommits(listener func(ctx context.Context, commit ObservedCommit)) func() {
	unsubscribe, err := session.observeCommits(listener)
	if err != nil {
		panic(err)
	}
	return unsubscribe
}

func (session *SessionImpl) observeCommits(listener func(ctx context.Context, commit ObservedCommit)) (func(), error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	entry := &internalListener{listener: listener}
	session.mu.Lock()
	session.internalOrder = append(session.internalOrder, entry)
	session.mu.Unlock()
	return func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		for index, candidate := range session.internalOrder {
			if candidate == entry {
				session.internalOrder = slices.Delete(session.internalOrder, index, index+1)
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

// ObserverSubscriptions reports the number of live observers of the Session's own (ObserveCommits): a document watch holds
// one, which sees a commit held in memory when it is adopted.
func (session *SessionImpl) ObserverSubscriptions() int {
	session.mu.Lock()
	defer session.mu.Unlock()
	return len(session.internalOrder)
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
				session.closeOrder = slices.Delete(session.closeOrder, index, index+1)
				return
			}
		}
	}, nil
}

// UnloadDocuments drops every loaded tracker on the mutation line, held changes stored first; later access cold-loads from
// Storage.
func (session *SessionImpl) UnloadDocuments() error {
	_, err := enqueue(session, false, nil, func() (struct{}, error) {
		if session.failure() == nil {
			if err := session.storeHolds(unscoped, session.allHolds()); err != nil {
				return struct{}{}, err
			}
		}
		session.mu.Lock()
		clear(session.documents)
		session.cache.clearIdle()
		session.mu.Unlock()
		return struct{}{}, nil
	})
	return err
}

// WaitDeliveries blocks until every scheduled watch delivery has drained.
func (session *SessionImpl) WaitDeliveries() { session.scheduler.Wait() }

// AwaitDeliveriesEntered blocks until every watch delivery scheduled before the call has entered: its listener returned,
// is parked in a wait, or is blocked behind a lock, which a Go listener does where an upstream one awaits and which the
// caller may hold. A Harness wait that resumes its caller calls it, as upstream's drain microtask enters the listener
// before the code awaiting the wait's promise resumes; the listener a caller's own goroutine runs is not waited for, so
// a listener may itself wait for the Harness. It returns the cause of ctx when ctx ends the wait.
func (session *SessionImpl) AwaitDeliveriesEntered(ctx context.Context) error {
	return session.scheduler.AwaitEntered(ctx)
}

func (session *SessionImpl) runCommit(ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope, withinMs *float64) (result any, err error) {
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
	if session.hooks.ReadOnly {
		// Refused before Storage: the Session stays healthy, and the caller sees why.
		tx.discard()
		session.sweepDocuments()
		return nil, durable.NewReadOnlySession("a commit with writes")
	}
	if withinMs != nil {
		if held, ok := tx.holdable(writes); ok {
			if err := session.hold(ctx, tx, held, *withinMs); err != nil {
				return nil, err
			}
			session.sweepDocuments()
			return result, nil
		}
	}
	// The last step before Storage: held changes leave their queue only once nothing but Storage can fail.
	var taken []*heldDocument
	session.mu.Lock()
	holding := len(session.holds) > 0
	session.mu.Unlock()
	if holding {
		writes, taken = tx.takeHeld(writes, session.heldOf)
	}
	// Storage adopts the batch synchronously once it is durable, before its next commit, and the commit is published in
	// the same step, so every view changes with memory. A panic here would reach Storage, so the callback keeps its own
	// error, and the first error of an own observer.
	var adoption *commitAdoption
	adopt := func(committed durable.Seq) {
		defer func() {
			if recovered := recover(); recovered != nil {
				adoption = &commitAdoption{err: panicError(recovered)}
			}
		}()
		documents, err := tx.adopt(committed)
		if err != nil {
			adoption = &commitAdoption{err: err}
			return
		}
		adoption = &commitAdoption{documents: documents, failure: session.publish(ctx, committed, writes, documents)}
	}
	// Once admitted, caller cancellation does not interrupt Storage settlement.
	_, err = session.storage.Commit(context.WithoutCancel(ctx), writes, adopt)
	if err != nil {
		// The guard has failed the Session with the storage error. A rejection after adoption leaves nothing staged to
		// discard, and the commit, durable, stays published; an own observer's error that would have failed the Session is
		// still reported.
		if adoption == nil {
			tx.discard()
		} else if adoption.err == nil && adoption.failure != nil {
			session.report(ctx, adoption.failure, listenerInfo)
		}
		return nil, err
	}
	if adoption == nil {
		err := errors.New("Storage committed without adopting the batch")
		session.failIn(ctx, err)
		return nil, err
	}
	if adoption.err != nil {
		// Storage already committed; a failed adoption leaves memory behind durable state.
		session.failIn(ctx, adoption.err)
		return nil, adoption.err
	}
	documents := adoption.documents
	if len(taken) > 0 {
		session.release(taken)
	}
	session.touchChanged(documents.stored)
	// The commit is durable and published, so it stands; memory behind an own observer's state fails the Session.
	if adoption.failure != nil {
		session.failIn(ctx, adoption.failure)
	}
	session.sweepDocuments()
	return result, nil
}

// commitAdoption is what the Session's adopt callback made of one batch: the document changes, the error that failed the
// adoption (nothing was published then), or the first error of an own observer that saw the published commit.
type commitAdoption struct {
	documents adoptedDocuments
	err       error
	failure   error
}

// panicError is the error a recovered panic value stands for.
func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}

// publication is the commit being published: the dispatch to SubscribeCommits listeners adopt queued, and the work the
// observers deferred until after it (DeferUntilPublished). It is run once, by whoever claims it: the goroutine armed in
// adopt once the adopting goroutine parked in a wait, or the Storage guard when Storage.Commit returned.
type publication struct {
	dispatch []func()
	deferred []func()
	claimed  bool
	// claim is closed when the publication is claimed, and done when it ran.
	claim, done chan struct{}
}

// beginPublication starts the publication of a commit: from here until it ran, the work its observers defer
// (DeferUntilPublished) waits.
func (session *SessionImpl) beginPublication() {
	session.deferMu.Lock()
	session.pub = &publication{claim: make(chan struct{}), done: make(chan struct{})}
	session.deferMu.Unlock()
}

// armPublication, the last step of adopt, lets the publication run as soon as the goroutine inside Storage's adopt is
// parked in a wait: upstream queues its one microtask there, which runs when the storage's synchronous step ends, also
// when the storage then works before it resolves (a JSONL reclaim). A storage that returns at once never parks:
// finishPublication runs it.
func (session *SessionImpl) armPublication() {
	session.deferMu.Lock()
	pub := session.pub
	session.deferMu.Unlock()
	if pub == nil || (len(pub.dispatch) == 0 && len(pub.deferred) == 0) {
		return
	}
	adopting := goroutineid.Current()
	//portlint:allow asyncorder joined: finishPublication waits for the publication to run, and Storage.Commit never returns to the Session before it
	go func() { //portlint:allow golifetime the goroutine ends when the publication is claimed (pub.claim closes), by it or by finishPublication, which waits for pub.done
		entered.Await(adopting, pub.claim)
		if session.claim(pub) {
			session.run(pub)
		}
	}()
}

// claim takes the publication to run it; false when another goroutine has.
func (session *SessionImpl) claim(pub *publication) bool {
	session.deferMu.Lock()
	defer session.deferMu.Unlock()
	if pub.claimed {
		return false
	}
	pub.claimed = true
	close(pub.claim)
	return true
}

// finishPublication ends the publication of a commit Storage adopted, once Storage.Commit returned: unless the
// goroutine armed in adopt took it already, it runs the dispatch adopt queued with the listeners registered now, then the
// work the observers deferred: the waits they resolved and the aborts they queued. It returns once the publication ran. It
// does nothing for a commit that was not adopted.
func (session *SessionImpl) finishPublication() {
	session.deferMu.Lock()
	pub := session.pub
	session.deferMu.Unlock()
	if pub == nil {
		return
	}
	if session.claim(pub) {
		session.run(pub)
		return
	}
	<-pub.done
}

// run runs the dispatch of pub, then the work deferred past it, and ends the publication.
func (session *SessionImpl) run(pub *publication) {
	defer close(pub.done)
	for {
		session.deferMu.Lock()
		if len(pub.dispatch) == 0 && len(pub.deferred) == 0 {
			if session.pub == pub {
				session.pub = nil
			}
			session.deferMu.Unlock()
			return
		}
		var next func()
		if len(pub.dispatch) > 0 {
			next, pub.dispatch = pub.dispatch[0], pub.dispatch[1:]
		} else {
			next, pub.deferred = pub.deferred[0], pub.deferred[1:]
		}
		session.deferMu.Unlock()
		next()
	}
}

// endPublication runs the publication of a commit held in memory, which no Storage commit adopted.
func (session *SessionImpl) endPublication() { session.finishPublication() }

// publish publishes a stored commit, inside adopt: its telemetry events first, then the dispatch to SubscribeCommits
// listeners, queued before the waits and aborts the observers defer, then the Session's own observers. It never panics: it
// returns the first error of an own observer, which fails the Session once Storage.Commit returned.
func (session *SessionImpl) publish(ctx context.Context, seq durable.Seq, writes []durable.StorageWrite, documents adoptedDocuments) error {
	session.beginPublication()
	defer session.armPublication()
	// The watches the observers advance deliver once, for all the frames of this commit: upstream queues one microtask.
	defer session.scheduler.Hold()()
	session.mu.Lock()
	internal := append([]*internalListener(nil), session.internalOrder...)
	hosts := len(session.commitOrder) > 0
	session.mu.Unlock()
	observed := hosts || len(internal) > 0
	span := chordctx.Telemetry(ctx)
	traced := span != telemetry.NoopTelemetryContext
	var changes []durable.CommitChange
	if observed {
		changes = make([]durable.CommitChange, 0, len(writes)+len(documents.stored))
	}
	for _, staged := range writes {
		table, ok := staged.(durable.TableCommitChange)
		if !ok {
			continue
		}
		switch write := table.(type) {
		case durable.EntryWrite:
			if !traced {
				break
			}
			session.event(ctx, span, "pi.durable.entry", telemetry.SpanAttributes{
				telemetry.Attr("entry.id", float64(write.Value.Id)),
				telemetry.Attr("entry.kind", write.Value.Kind),
			})
		case durable.SubmissionWrite:
			if traced && (write.Value.Status == durable.SubmissionDone || write.Value.Status == durable.SubmissionUnanswered) {
				attributes := telemetry.SpanAttributes{
					telemetry.Attr("submission.id", float64(write.Value.Id)),
					telemetry.Attr("status", string(write.Value.Status)),
				}
				if write.Value.Answer != nil {
					attributes = attributes.Set("answer.entry_id", float64(*write.Value.Answer))
				}
				session.event(ctx, span, "pi.durable.submission", attributes)
			}
		}
		if observed {
			changes = append(changes, table)
		}
	}
	if !observed {
		return nil
	}
	tables := len(changes)
	for _, document := range documents.observed {
		changes = append(changes, document)
	}
	commit := ObservedCommit{Seq: seq, Changes: changes}
	if hosts {
		// One publication for both, unless the commit also stored held changes, which only host listeners get.
		stored := durable.CommitPublication{Seq: seq, Changes: changes}
		if documents.split {
			stored.Changes = append(slices.Clone(changes[:tables]), documentChanges(documents.stored)...)
		}
		session.notifyHosts(ctx, stored)
	}
	// The commit is durable: a failing observer neither fails the commit nor skips the others.
	var failure error
	for _, listener := range internal {
		contained(func() { listener.listener(ctx, commit) }, func(err error) {
			if failure == nil {
				failure = err
			}
		})
	}
	return failure
}

// event records a commit's telemetry event inside adopt. A panicking adapter is reported, as adopt must not panic.
func (session *SessionImpl) event(ctx context.Context, span telemetry.TelemetrySpan, name string, attributes telemetry.SpanAttributes) {
	contained(func() { span.AddEvent(name, attributes) }, func(err error) {
		session.report(ctx, err, durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackTelemetry})
	})
}

// notifyHosts queues, inside adopt, the dispatch of publication to the SubscribeCommits listeners: it runs once adopt
// returned (armPublication, finishPublication), with the listeners registered then, and a panic is reported.
func (session *SessionImpl) notifyHosts(ctx context.Context, publication durable.CommitPublication) {
	session.deferMu.Lock()
	session.pub.dispatch = append(session.pub.dispatch, func() {
		session.mu.Lock()
		listeners := append([]*commitListener(nil), session.commitOrder...)
		session.mu.Unlock()
		for _, listener := range listeners {
			contained(func() { listener.listener(ctx, publication) }, session.reportListenerIn(ctx))
		}
	})
	session.deferMu.Unlock()
}

// documentChanges is documents as commit changes.
func documentChanges(documents []durable.DocumentCommitChange) []durable.CommitChange {
	changes := make([]durable.CommitChange, 0, len(documents))
	for _, document := range documents {
		changes = append(changes, document)
	}
	return changes
}

// DeferUntilPublished runs run once the publication in progress has run every host listener, and at once when none is.
// Upstream settles a waiter by resolving a promise inside an observer, and the awaiting code resumes after the microtask
// that dispatches the commit to host listeners; a goroutine woken inside an observer would race them, so waiters wake
// through this, and so does the abort of a task the commit newly marks (upstream queues it in a microtask). Observers
// call it from the publishing goroutine, as publications are serialized on the line.
func (session *SessionImpl) DeferUntilPublished(run func()) {
	session.deferMu.Lock()
	if session.pub == nil {
		session.deferMu.Unlock()
		run()
		return
	}
	session.pub.deferred = append(session.pub.deferred, run)
	session.deferMu.Unlock()
}

// observeDocument follows the address of the incarnation loaded, after checking the definition, with the observer create
// makes from its value, the idempotent detach, and the report of a listener error; it returns that observer. The observer
// gets each committed change there, a retirement as a Retired change with the last value it was given and no ops (a change
// in the retiring commit is not published), and a later incarnation as a root replacement. When the commit makes the owner
// task of a task document terminal, no later commit can change the address: after its changes the commit subscription and
// the pin go, and ownerEnded tells the observer, which stays subscribed to the Session's close until it ends (its detach
// drops that). End gets the Session's close. The current incarnation is pinned in the cache. The detach runs before End;
// once it ran, nothing more of the commit underway reaches the observer or pins anything for it.
func (session *SessionImpl) observeDocument(
	definition *durable.AnyDocDefinition,
	loaded *LoadedDocument,
	create func(value durable.JsonObject, detach func(), report func(error)) durable.Observer[durable.JsonObject],
	ownerEnded func(observer durable.Observer[durable.JsonObject]),
) (durable.Observer[durable.JsonObject], error) {
	if err := checkLoaded(definition, loaded); err != nil {
		return nil, err
	}
	// The closures below keep only the address, as the cache's ID and first record, which the cache shares while the
	// document is cached: loaded holds its incarnation's tracker, which a retirement frees.
	addressId := loaded.AddressId
	address := durable.DocumentAddress{Kind: loaded.Record.Kind, Scope: loaded.Record.Scope, Key: loaded.Record.Key}
	var (
		mu sync.Mutex
		// incarnation is the incarnation followed, with ok false while the address is absent.
		incarnation       = loaded.Record.Id
		followed          = true
		value             = loaded.Tracker.Value()
		version           = loaded.ValueVersion
		attached          = true
		unpin             = session.pinDocument(addressId, incarnation)
		unsubscribeCommit = func() {}
		unsubscribeClose  = func() {}
	)
	detach := func() {
		mu.Lock()
		if !attached {
			mu.Unlock()
			return
		}
		attached = false
		commit, closer, release := unsubscribeCommit, unsubscribeClose, unpin
		mu.Unlock()
		commit()
		closer()
		release()
	}
	observer := create(loaded.Tracker.Value(), detach, session.reportListener)
	isAttached := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attached
	}
	commit, err := session.observeCommits(func(ctx context.Context, publication ObservedCommit) {
		// Listeners run from a copy of the set: another observer's call may have detached this one.
		if !isAttached() {
			return
		}
		ownerEnds := false
		for _, change := range publication.Changes {
			var document durable.DocumentChange
			switch typed := change.(type) {
			case durable.TaskWrite:
				if address.Scope.Kind == durable.ScopeTask && typed.Value.Id == address.Scope.TaskId && typed.Value.State.Status == durable.TaskTerminal {
					ownerEnds = true
				}
				continue
			case durable.DocumentChange:
				document = typed
			default:
				continue
			}
			mu.Lock()
			current := followed && document.Record.Id == incarnation
			mu.Unlock()
			// The incarnation followed is at the address by definition; only another record needs the address compared.
			if !current && !atAddress(document.Record, address) {
				continue
			}
			switch {
			case document.Value == nil:
				if !current {
					continue
				}
				mu.Lock()
				last := value
				followed, value = false, nil
				release := unpin
				mu.Unlock()
				release()
				observer.Advance(ctx, durable.WatchChange[durable.JsonObject]{Value: last, Retired: true})
			case !current || *document.Version != version:
				// A new incarnation at the address, or a value of another definition version, whose shape the observer's
				// value does not have: shown whole.
				if !current {
					mu.Lock()
					previous := unpin
					incarnation, followed = document.Record.Id, true
					mu.Unlock()
					previous()
					pinned := session.pinDocument(addressId, document.Record.Id)
					mu.Lock()
					if attached {
						unpin = pinned
						mu.Unlock()
					} else {
						mu.Unlock()
						pinned()
					}
				}
				mu.Lock()
				version, value = *document.Version, document.Value
				mu.Unlock()
				observer.Advance(ctx, durable.WatchChange[durable.JsonObject]{Value: document.Value, Ops: []durable.Op{{"r", document.Value}}})
			default:
				// A migration-only base changes nothing for an observer of the new version.
				if len(document.Ops) == 0 {
					continue
				}
				mu.Lock()
				value = document.Value
				mu.Unlock()
				observer.Advance(ctx, durable.WatchChange[durable.JsonObject]{Value: document.Value, Ops: document.Ops})
			}
			// The observer may detach in its call, or panic and be detached: the rest of the commit is not its.
			if !isAttached() {
				return
			}
		}
		if !ownerEnds {
			return
		}
		mu.Lock()
		unsubscribe, release := unsubscribeCommit, unpin
		mu.Unlock()
		unsubscribe()
		release()
		ownerEnded(observer)
	})
	if err != nil {
		unpin()
		return nil, err
	}
	closer, err := session.subscribeClose(func() {
		detach()
		observer.End(session.failure())
	})
	if err != nil {
		commit()
		unpin()
		return nil, err
	}
	mu.Lock()
	unsubscribeCommit, unsubscribeClose = commit, closer
	mu.Unlock()
	return observer, nil
}

// atAddress reports whether record is an incarnation at address.
func atAddress(record durable.DocumentRecord, address durable.DocumentAddress) bool {
	if record.Kind != address.Kind || (record.Key == nil) != (address.Key == nil) || (record.Key != nil && *record.Key != *address.Key) {
		return false
	}
	if record.Scope.Kind != address.Scope.Kind {
		return false
	}
	switch record.Scope.Kind {
	case durable.ScopeConversation:
		return record.Scope.ConversationId == address.Scope.ConversationId
	case durable.ScopeTask:
		return record.Scope.TaskId == address.Scope.TaskId
	}
	return true
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
	session.mu.Unlock()
	if cached != nil {
		// The reload must find what memory holds: held changes are stored before their tracker goes.
		if hold := session.heldOf(cached.Record.Id); hold != nil {
			if err := session.storeHolds(ctx, []*heldDocument{hold}); err != nil {
				return nil, err
			}
		}
		session.mu.Lock()
		session.dropDocumentLocked(addressId)
		session.mu.Unlock()
	}
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
