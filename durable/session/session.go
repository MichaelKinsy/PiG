// Package session ports the pi-durable Session kernel: one mutation line, the group-commit writer that stores what the line
// admits and publishes it in line order, the loaded document tracker cache, transactions, document forks, and committed
// publication and observation.
//
// Ports packages/durable/src/session/session.ts
//
// Go mapping decisions:
//   - Upstream's Promise chain of line jobs is a FIFO ticket queue (slot): each job waits for its predecessor's release, so jobs
//     run in admission order, each on its caller's goroutine, one at a time. A job releases the line when its callback,
//     settlement, and logical admission are done (it has handed its commit to the writer); it does not wait for the commit.
//   - Group commit (docs/group-commit.md): a commit with writes joins the writer's pending batch (Writer.Admit) and the line goes
//     on. The writer, one goroutine it owns, sends everything admitted while no call is in flight as one Storage.Commit call,
//     off the line, and publishes the parts of a call in line order. A job's reads wait on the gate of the newest unpublished
//     writer of their key (Touch, AwaitTouch, GatedStorage); a gate wait blocks the job's goroutine, never the writer.
//   - Pi's event loop gives two guarantees that Go's scheduler does not, and the Session keeps them with an explicit handshake
//     (resumption.go) that the waits of the Session signal, never by reading the state of goroutines, at any number of them:
//     (1) a publication queues one dispatch to the host's observers, which runs a microtask later, before every wait the commit
//     resolves and every abort it queues: here the publication runs the dispatch right after the part's own observers
//     (inside the Storage's publication scope when it has one), then the work the observers deferred (DeferUntilPublished:
//     wakes of waits, aborts), and waits until the registered waits those wakes readied have resumed (Park, Await);
//     (2) outcomes are delivered in line order, and the continuations of what a publication resolved start before the next
//     part of the call is published (a macrotask hop): a delivery goroutine the Session owns releases the callers in line
//     order, waiting for each released caller to have resumed from its wait, and the writer's hop before the next part waits
//     for that delivery goroutine to be idle. "Started" is as far as the Session can know for code that is not its own: a
//     caller that needs its continuation ordered up to its next wait brings a Continuation (WithContinuation).
//   - A job's outcome is delivered in line order (the fence): a no-write job, a read, and a callback's own error wait for the
//     outcomes before them, and a commit for its own publication. CommitInternal's jobs are exempt when they wrote nothing and
//     waited at no gate. At most maxOutstanding admitted jobs have no outcome delivered; beyond it the line waits.
//   - An AbortSignal is the context's cancellation; withoutAbortSignal is context.WithoutCancel, which keeps context values. A
//     gate wait is not cancellable, as upstream's: a gate always opens, by a publication or by the Session's failure.
package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// ErrSessionClosed is upstream's "Session is closed".
var ErrSessionClosed = errors.New("Session is closed")

var _ durable.Session = (*SessionImpl)(nil)

// maxOutstanding is the number of jobs admitted to the line whose outcome is not delivered yet, beyond which the line waits. A
// count, not bytes: what waits is bounded by what one commit can hold (tool output by its output limits), times the count.
const maxOutstanding = 256

// Hooks are the extension points a Harness overrides on its Session.
type Hooks struct {
	// ConversationCreated runs inside every transaction that creates or forks a conversation, after the conversation record is
	// staged. A plain Session stages nothing.
	ConversationCreated func(tx *Transaction, record durable.ConversationRecord) error
	// MembershipKeys returns the keys a commit writes besides its records' (Transaction.WriteKeys), at its logical admission,
	// on the line: a Harness adds the ownership memberships its task changes write. A plain Session adds none.
	MembershipKeys func(tx *Transaction) ([]string, error)
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
	observer *GuardedCommitObserver
}

type closeListener struct{ listener func() }

// lineJob is a job on the line: its index in line order, and whether a touch of it waited for an unpublished writer.
type lineJob struct {
	index int
	// hit and ends are guarded by SessionImpl.jobMu.
	hit bool
	// ends are called once its outcome settles: its commit published, or never to be (OnJobEnd).
	ends []func()
}

// delivery is the outcome of one job with an outcome to deliver, in the line-ordered fence. Its fields are guarded by
// SessionImpl.fenceMu until released is closed.
type delivery struct {
	index int
	// resumption is acknowledged by the job's caller once it resumed from waiting for released: the delivery goroutine waits for
	// it before it releases the next caller.
	resumption
	ready bool
	// skip is set when the caller takes its outcome at once (an exempt job) or the position was never used: nothing to deliver.
	skip bool
	// outcome is what the job settled with; final is what its caller gets, written before released is closed.
	outcome  error
	final    error
	released chan struct{}
}

// slot is a position on the line.
type slot struct {
	index    int
	previous chan struct{}
	done     chan struct{}
	job      *lineJob
	// delivery is nil for a maintenance job, which has no outcome to deliver.
	delivery *delivery
}

// ran is a line job's result: its value, delivered once after, the part its commit admitted, settles, if it has one.
type ran[T any] struct {
	value T
	after *Part
}

// SessionImpl is the Session kernel: one mutation line, the group-commit writer, the loaded document tracker cache, and
// committed publication.
//
// Only committed state is observable: a commit's callback runs while earlier commits' storage calls are in flight, but a job
// that reads what an unpublished commit wrote waits for it, and everything a commit makes visible happens at its publication.
type SessionImpl struct {
	// deferMu guards pub, the publication in progress.
	deferMu sync.Mutex
	pub     *publication
	storage *guardedStorage
	// cached is the record cache under the guard, which the writer applies each batch to at its publication; nil for a
	// read-only Session, whose Storage another process writes.
	cached *CachedStorage
	// tails is the newest entry and head marker per conversation; nil for a read-only Session.
	tails *ConversationTails
	// gated is the Storage jobs on the line read through: each read waits while its key has an unpublished writer.
	gated  durable.Storage
	writer *Writer
	hooks  Hooks
	timers durable.Timers
	// documentTimersUnavailable is set once the host declined a background timer: then none is kept, and the end of
	// each commit evicts what expired. Guarded by mu.
	documentTimersUnavailable bool
	host                      *transactionHost
	scheduler                 *chord.DeliveryScheduler
	// lifetime is cancelled when the Session ended: it bounds the waits for goroutines to enter.
	lifetime    context.Context
	endLifetime context.CancelFunc

	lineMu   sync.Mutex
	tail     chan struct{}
	lineJobs int
	// nextIndex is the line index of the next job; outcomes are delivered in this order.
	nextIndex int

	// jobMu guards current, the job on the line, and the hit and ends of every job. It is a small lock of its own: a touch
	// takes only it and the writer's.
	jobMu   sync.Mutex
	current *lineJob

	// parkMu guards parks, the waits of the Session registered before they block (Park), which a publication waits for once it
	// readied them. awaitingWakes is the number of those the writer waits for now, awaitingCallers the number of released commit
	// callers the delivery goroutine waits for.
	parkMu          sync.Mutex
	parks           map[*Parked]struct{}
	awaitingWakes   atomic.Int32
	awaitingCallers atomic.Int32

	// fenceMu guards the fence: the queue of deliveries in line order, the delivery goroutine's state, the backpressure
	// counters, and the failure's boundary.
	fenceMu    sync.Mutex
	fence      []*delivery
	delivering bool
	idle       *sync.Cond
	// delivering is set while the delivery goroutine runs; idle is signalled when it ends.
	// outstanding counts the jobs that started on the line and have no outcome delivered yet (maxOutstanding); room is closed
	// when it drops below it.
	outstanding   int
	room          chan struct{}
	lastDelivered int
	// failAt is the line index whose outcome the failure is: every later job's outcome is SessionFailed, except ownOutcomes.
	failAt *int
	// ownOutcomes are later jobs whose outcome is their own error all the same: the other parts of a storage call that rejected.
	ownOutcomes map[int]bool

	mu        sync.Mutex
	documents map[string]*LoadedDocument
	// taskDocuments are the current documents of each live task this Session created, so the commit that ends a task retires
	// them without a scan. An entry starts with its task's creation and goes when the task is terminal: bounded by live tasks.
	taskDocuments map[durable.TaskId]*taskDocuments
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

// taskDocuments are the current documents of one live task, in the order they were indexed.
type taskDocuments struct {
	order   []durable.DocumentId
	records map[durable.DocumentId]durable.DocumentRecord
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
	// PersistWithinMs is the default window of a deferred commit (durable.CommitPersistence): how long it may stay in memory before it is stored.
	// Nil selects the default, 1,000 ms; +Inf waits for a reading or writing commit or close.
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
	session := &SessionImpl{hooks: hooks, scheduler: chord.NewDeliveryScheduler(), documents: map[string]*LoadedDocument{}, taskDocuments: map[durable.TaskId]*taskDocuments{}, holds: map[durable.DocumentId]*heldDocument{}, cache: newDocumentCache(), now: now, retentionSource: documentRetentionMs, failed: make(chan struct{}), closed: make(chan struct{}), lastDelivered: -1, ownOutcomes: map[int]bool{}}
	session.idle = sync.NewCond(&session.fenceMu)
	//portlint:allow ctxbackground the Session's own lifetime: it bounds the waits for goroutines to enter and ends with the Session, not with a caller
	session.lifetime, session.endLifetime = context.WithCancel(context.Background())
	session.timers = durable.HostTimers(hooks.SetTimer, func(err error) {
		session.report(unscoped, err, durable.ReportInfo{Kind: durable.ReportHostError, Callback: durable.HostCallbackSetTimer})
	})
	// A read-only Session shows a Storage another process writes, so nothing it reads stays current.
	backend := storage
	if !hooks.ReadOnly {
		session.cached = NewCachedStorage(storage)
		session.tails = NewConversationTails()
		backend = session.cached
	}
	session.storage = newGuardedStorage(backend, storage, session)
	session.gated = session.storage.gate(NewGatedStorage(session.storage, session.AwaitTouch))
	session.writer = NewWriter(writerHost{session})
	session.host = &transactionHost{
		storage: session.gated,
		touch:   session.AwaitTouch,
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
		taskDocuments: session.taskDocumentsOf,
		latestEntryId: session.LatestEntryId,
		latestHeadMarker: func(ctx context.Context, conversationId durable.ConversationId) (*durable.EntryRecord, error) {
			return session.LatestHeadMarker(ctx, conversationId, nil)
		},
	}
	return session
}

// writerHost is the Session as its writer sees it.
type writerHost struct{ session *SessionImpl }

func (host writerHost) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	return host.session.storage.Commit(ctx, batches, receive)
}

func (host writerHost) Stored(writes []durable.StorageWrite) {
	if host.session.cached != nil {
		host.session.cached.Published(writes)
	}
	if host.session.tails != nil {
		host.session.tails.Apply(writes)
	}
}

// Publishing runs one part's publication inside the Storage's own scope (Storage.Publishing), such as a store host's batch;
// directly without one. It returns nil when it ran at once, else a channel closed once it ran. The publication queues the
// dispatch to the host's observers and the work the observers deferred; they run when it ended, still inside the scope when
// the Storage runs the publication later. A Storage whose scope panics instead of running it is reported, and the publication
// runs directly: the commit is stored, so it must be published.
func (host writerHost) Publishing(publish func(), ctx context.Context) <-chan struct{} {
	session := host.session
	session.beginPublication()
	run := func() {
		defer session.endPublication()
		publish()
	}
	scope := session.storage.publishing()
	if scope == nil {
		run()
		return nil
	}
	done := make(chan struct{})
	var once sync.Once
	runOnce := func() { once.Do(func() { defer close(done); run() }) }
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				session.report(ctx, panicError(recovered), listenerInfo)
				runOnce()
			}
		}()
		scope(ctx, runOnce)
	}()
	select {
	case <-done:
		return nil
	default:
		return done
	}
}

// Hop is the macrotask between two publications of a call: it returns once the callers of the commits published so far have
// been released in line order and have resumed, and the watch deliveries scheduled by the publications have run the synchronous
// part of their listeners (entered, see AwaitDeliveriesEntered), so a listener sees one call per part as in Pi.
//
// pig divergence (D224): a listener that blocks on a wait the host cannot see (a channel, a lock, I/O) without calling
// chord.DeliveryEntered holds this hop, and with it every later publication of the Session, until it returns; Pi's listener awaits and
// nothing waits for it.
func (host writerHost) Hop() {
	session := host.session
	session.awaitDeliveriesIdle()
	// A failed wait means the Session ended: nothing more is published.
	_ = session.scheduler.AwaitEntered(session.lifetime)
}

func (host writerHost) Failure() error { return host.session.failure() }

func (host writerHost) Fail(err error, at int, own []int) {
	host.session.failFrom(unscoped, err, &at, own)
}

func (host writerHost) Report(err error, ctx context.Context) {
	host.session.report(ctx, err, listenerInfo)
}

// takeSlotLocked takes the next line position with lineMu held. A fenced job also takes its place in the fence.
func (session *SessionImpl) takeSlotLocked(fenced bool) *slot {
	taken := &slot{index: session.nextIndex, previous: session.tail, done: make(chan struct{})}
	taken.job = &lineJob{index: taken.index}
	session.nextIndex++
	session.tail = taken.done
	session.lineJobs++
	if fenced {
		taken.delivery = &delivery{index: taken.index, resumption: newResumption(nil), released: make(chan struct{})}
		session.fenceMu.Lock()
		session.fence = append(session.fence, taken.delivery)
		session.fenceMu.Unlock()
	}
	return taken
}

// admitSlot takes the next line position. With admit, the Session must be usable when the job takes its position: upstream
// checks assertUsable and enqueues in one synchronous turn, so a job that passed admission is always queued ahead of the close
// job and settles before Storage closes.
func (session *SessionImpl) admitSlot(admit, fenced bool) (*slot, error) {
	session.lineMu.Lock()
	if admit {
		if listenersRan, err := session.usable(); err != nil {
			// Wait for the close listeners without the line mutex: a listener may need a lock whose holder is taking a
			// line ticket (the Harness scheduler's seal and kick).
			session.lineMu.Unlock()
			if listenersRan != nil {
				await(listenersRan)
			}
			return nil, err
		}
	}
	taken := session.takeSlotLocked(fenced)
	session.lineMu.Unlock()
	return taken, nil
}

// releaseLine releases the line to the next job.
func (session *SessionImpl) releaseLine(s *slot) {
	session.lineMu.Lock()
	session.lineJobs--
	session.lineMu.Unlock()
	close(s.done)
}

// onLine runs work as job s holds the line, after its predecessor, and releases the line when work returned.
func onLine[T any](session *SessionImpl, s *slot, work func(index int) (T, error)) (T, error) {
	if s.previous != nil {
		await(s.previous)
	}
	session.jobMu.Lock()
	session.current = s.job
	session.jobMu.Unlock()
	defer func() {
		session.jobMu.Lock()
		session.current = nil
		session.jobMu.Unlock()
		session.releaseLine(s)
	}()
	return work(s.index)
}

// lineCall is what a job on the line takes from the context of its caller.
type lineCall struct {
	// admitted, when non-nil, runs once the job holds its position and before it waits for earlier jobs: the point at which
	// upstream's synchronous caller has extended the line's Promise chain (internal/lineadmission).
	admitted func()
	// continuation, when non-nil, is the caller's Continuation (WithContinuation).
	continuation *Continuation
}

// lineCallOf is the lineCall of a caller with context ctx.
func lineCallOf(ctx context.Context) lineCall {
	return lineCall{admitted: lineadmission.From(ctx), continuation: continuationOf(ctx)}
}

// runFenced runs a job with an outcome on the line: after its predecessor, within the backpressure bound. The line goes on once
// the job's work returned; the job's caller then waits for its outcome, which is delivered in line order once its commit, if it
// admitted one, is published. call.admitted runs once the job holds its position and before it waits for earlier jobs. With
// exempt, a job that admitted no commit and waited at no gate is delivered as soon as it has its outcome: it is equivalent to
// having run before every unpublished commit. The caller acknowledges its resumption once it is released, which the delivery
// goroutine waits for before it releases the next one.
func runFenced[T any](session *SessionImpl, s *slot, exempt bool, call lineCall, work func(index int) (ran[T], error)) (T, error) {
	var zero T
	delivery := s.delivery
	delivery.continuation = call.continuation
	if call.admitted != nil {
		call.admitted()
	}
	var result ran[T]
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				// The callback panicked: its outcome settles like a rejection, so the jobs after it are delivered, then it keeps
				// panicking. It never waits for its release, so it has resumed as far as anybody can tell.
				session.settleJob(s, false, panicError(recovered))
				delivery.acknowledge()
				panic(recovered)
			}
		}()
		if s.previous != nil {
			// Waiting for the line is a wait of the Session: a continuation, and a watch delivery, that goes on to it has entered.
			hold(call.continuation, s.previous)
		}
		result, err = onLine(session, s, func(index int) (ran[T], error) {
			session.waitRoom()
			return work(index)
		})
	}()
	switch {
	case err != nil:
		session.settleJob(s, exempt, err)
	case result.after != nil:
		result.after.OnSettled(func(outcome error) { session.settleJob(s, false, outcome) })
	default:
		session.settleJob(s, exempt, nil)
	}
	session.fenceMu.Lock()
	skip := delivery.skip
	outcome := delivery.outcome
	session.fenceMu.Unlock()
	var final error
	if skip {
		// Delivered at once, ahead of the outcomes before it.
		session.delivered(s.index, false)
		final = session.poison(s.index, outcome)
	} else {
		hold(call.continuation, delivery.released)
		delivery.resume()
		final = delivery.final
	}
	if final != nil {
		return zero, final
	}
	return result.value, nil
}

// settleJob settles job s's outcome: what waited for it (OnJobEnd) runs, and the job is ready to be delivered. With exemptClean,
// a job that waited at no gate is delivered at once by its caller.
func (session *SessionImpl) settleJob(s *slot, exemptClean bool, outcome error) {
	session.ended(s.job)
	session.jobMu.Lock()
	hit := s.job.hit
	session.jobMu.Unlock()
	delivery := s.delivery
	session.fenceMu.Lock()
	delivery.outcome = outcome
	delivery.ready = true
	if exemptClean && !hit {
		delivery.skip = true
	}
	session.fenceMu.Unlock()
	session.kickDeliver()
}

// poison is the outcome job index delivers: outcome, or SessionFailed when it comes after the failure's boundary without an
// outcome of its own.
func (session *SessionImpl) poison(index int, outcome error) error {
	session.fenceMu.Lock()
	poisoned := session.failAt != nil && index > *session.failAt && !session.ownOutcomes[index]
	session.fenceMu.Unlock()
	if poisoned {
		if failure := session.failure(); failure != nil {
			return durable.NewSessionFailed(failure)
		}
	}
	return outcome
}

// kickDeliver starts the delivery goroutine when the head of the fence is ready to be delivered and none runs.
func (session *SessionImpl) kickDeliver() {
	session.fenceMu.Lock()
	if session.delivering || len(session.fence) == 0 || !session.fence[0].ready {
		session.fenceMu.Unlock()
		return
	}
	session.delivering = true
	// The delivery goroutine ends by itself once the head of the fence is not ready; the fence's own state tells when none runs.
	session.fenceMu.Unlock()
	//portlint:allow golifetime deliverLoop is the drain of Pi's #fence chain (session.ts:1630-1641): it clears session.delivering and broadcasts idle when the head is not ready, which awaitDeliveriesIdle and awaitFenceEmpty (Close) wait for
	go session.deliverLoop()
}

// deliverLoop is the delivery goroutine: it releases the callers of the jobs at the head of the fence, in line order, as long as
// they are ready, and waits for each released caller to have resumed from its wait before it releases the next: upstream
// delivers outcomes in line order, and the continuation of one starts before the next.
func (session *SessionImpl) deliverLoop() {
	// The loop ends, and says so, when the head of the fence is not ready.
	for {
		session.fenceMu.Lock()
		if len(session.fence) == 0 || !session.fence[0].ready {
			session.delivering = false
			session.idle.Broadcast()
			session.fenceMu.Unlock()
			return
		}
		next := session.fence[0]
		session.fence = session.fence[1:]
		skip := next.skip
		session.fenceMu.Unlock()
		if skip {
			continue
		}
		session.delivered(next.index, true)
		next.final = session.poison(next.index, next.outcome)
		close(next.released)
		// The caller acknowledges as the first thing it does after its wait returned, holding nothing: the continuation of
		// this outcome started before the next is released.
		session.awaitResumed(&next.resumption, &session.awaitingCallers)
	}
}

// awaitDeliveriesIdle returns once the delivery goroutine released every caller that is ready, and each has resumed.
func (session *SessionImpl) awaitDeliveriesIdle() {
	session.fenceMu.Lock()
	for session.delivering {
		session.idle.Wait()
	}
	session.fenceMu.Unlock()
}

// awaitFenceEmpty returns once every job admitted so far has its outcome delivered: the fence is empty and the delivery
// goroutine idle.
func (session *SessionImpl) awaitFenceEmpty() {
	session.fenceMu.Lock()
	for session.delivering || len(session.fence) > 0 {
		session.idle.Wait()
	}
	session.fenceMu.Unlock()
}

// waitRoom takes one of the maxOutstanding places for a job that starts on the line; it waits, holding the line, while none is
// free.
func (session *SessionImpl) waitRoom() {
	for {
		session.fenceMu.Lock()
		if session.outstanding < maxOutstanding {
			session.outstanding++
			session.fenceMu.Unlock()
			return
		}
		if session.room == nil {
			session.room = make(chan struct{})
		}
		room := session.room
		session.fenceMu.Unlock()
		await(room)
	}
}

// delivered notes that job index has its outcome: the line may go on past maxOutstanding.
func (session *SessionImpl) delivered(index int, fenced bool) {
	session.fenceMu.Lock()
	if fenced {
		session.lastDelivered = index
	}
	session.outstanding--
	if session.room != nil && session.outstanding < maxOutstanding {
		close(session.room)
		session.room = nil
	}
	session.fenceMu.Unlock()
}

// ended runs what waited for job's outcome to settle (OnJobEnd).
func (session *SessionImpl) ended(job *lineJob) {
	session.jobMu.Lock()
	ends := job.ends
	job.ends = nil
	session.jobMu.Unlock()
	for _, end := range ends {
		end()
	}
}

// enqueue runs job on the mutation line after every previously admitted job, as a job with an outcome and no commit: a read.
// With admit, the Session must be usable when the job takes its position. call.admitted, when non-nil, runs once the job holds its
// position and before it waits for earlier jobs.
func enqueue[T any](session *SessionImpl, admit bool, call lineCall, job func() (T, error)) (T, error) {
	s, err := session.admitSlot(admit, true)
	if err != nil {
		var zero T
		return zero, err
	}
	return runFenced(session, s, false, call, func(int) (ran[T], error) {
		value, err := job()
		return ran[T]{value: value}, err
	})
}

// maintain runs a job of the Session's own upkeep on the line: no outcome to deliver, so no fence and no SessionFailed.
func maintain[T any](session *SessionImpl, work func(index int) (T, error)) (T, error) {
	s, _ := session.admitSlot(false, false)
	return onLine(session, s, work)
}

// enqueueDetached queues upkeep job on the mutation line without waiting for it: it takes its position now, so it runs before
// any job queued later, and runs on its own goroutine once the earlier jobs settled.
func (session *SessionImpl) enqueueDetached(job func(index int)) {
	s, _ := session.admitSlot(false, false)
	//portlint:allow golifetime the job is a line job: it runs once the earlier jobs settled and releases its slot (onLine), which every later job and Close wait for, as Pi's #tail promise chain does (session.ts:258)
	go func() {
		_, _ = onLine(session, s, func(index int) (struct{}, error) {
			job(index)
			return struct{}{}, nil
		})
	}()
}

// assertUsable rejects an operation once close began. Upstream sets the closing flag and runs the close listeners in
// one synchronous turn, so no rejection is observable before the listeners ran; the Harness scheduler reads its own
// closing state, which its listener sets, to decide whether to report a rejected commit. Here a rejection waits for the
// listeners, which must not call Session operations.
func (session *SessionImpl) assertUsable() error {
	listenersRan, err := session.usable()
	if listenersRan != nil {
		await(listenersRan)
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

// AssertHealthy is internal: SessionFailed once the Session failed, nil otherwise (session.ts assertHealthy).
func (session *SessionImpl) AssertHealthy() error { return session.assertHealthy() }

func (session *SessionImpl) assertHealthyLocked() error {
	if session.failureErr != nil {
		return durable.NewSessionFailed(session.failureErr)
	}
	return nil
}

// Storage returns the Session's Storage behind its failure guard; every component of the Session reads and writes
// through it, also the reads off the Session line. A Harness passes it to its scheduler and context reads.
func (session *SessionImpl) Storage() durable.Storage { return session.storage.exposed() }

// LineStorage is internal: the Storage jobs on the line read through (Touch): each read waits while its key has an unpublished
// writer, so a job never observes state an earlier job admitted and the writer has not published. Off the line, use Storage
// (session.ts lineStorage).
func (session *SessionImpl) LineStorage() durable.Storage { return session.gated }

// failure returns the error that failed the Session, nil while it has not failed.
func (session *SessionImpl) failure() error {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.failureErr
}

// Failure returns the first error that failed the Session, or nil.
func (session *SessionImpl) Failure() error { return session.failure() }

// ErrSessionClosedEnd is the error a synchronous observer fails with when the Session closes without having failed (session.ts
// closeEnds: "session closed"); an adapter following the Session reads it as a reason to follow the next one. Each close
// makes its own error wrapping it, as Pi makes a new Error per close.
var ErrSessionClosedEnd = errors.New("session closed")

// CloseEnds is how a Session's close ends what follows it (session.ts closeEnds, decision "Slice 8 S1"), made once per close.
// An app's watch ends ended; a synchronous observer (an adapter following the Session, which may open it again) fails with
// ErrSessionClosedEnd, so it follows the next Session. A failed Session ends both failed with its error.
func CloseEnds(failure error) (watch, observer durable.WatchEnd) {
	if failure != nil {
		failed := durable.WatchEnd{Kind: durable.WatchFailed, Error: failure}
		return failed, failed
	}
	return durable.WatchEnd{Kind: durable.WatchEnded}, durable.WatchEnd{Kind: durable.WatchFailed, Error: fmt.Errorf("%w", ErrSessionClosedEnd)}
}

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
var listenerInfo = durable.ReportInfo{Kind: durable.ReportListenerError}

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
// scheduler's own commits. The job on the line is the one whose outcome the failure is: later jobs' outcomes become
// SessionFailed.
func (session *SessionImpl) Fail(ctx context.Context, err error) {
	session.failFrom(ctx, err, nil, nil)
}

func (session *SessionImpl) fail(err error) { session.failFrom(unscoped, err, nil, nil) }

// failFrom fails the Session and reports it under the telemetry of ctx, which is where upstream's `fail(error, context)`
// records it. at is the line index of the job whose outcome the failure is, by default the job on the line, else the last
// delivered; own are later jobs whose outcome is their own error all the same.
func (session *SessionImpl) failFrom(ctx context.Context, err error, at *int, own []int) {
	session.mu.Lock()
	if session.failureErr != nil {
		session.mu.Unlock()
		return
	}
	session.failureErr = err
	close(session.failed)
	session.mu.Unlock()
	session.jobMu.Lock()
	var running *int
	if session.current != nil {
		index := session.current.index
		running = &index
	}
	session.jobMu.Unlock()
	session.fenceMu.Lock()
	switch {
	case at != nil:
		session.failAt = at
	case running != nil:
		session.failAt = running
	default:
		last := session.lastDelivered
		session.failAt = &last
	}
	for _, index := range own {
		session.ownOutcomes[index] = true
	}
	session.fenceMu.Unlock()
	// Seal first, so a report handler that calls back finds the Session failed. Closing runs the close listeners
	// synchronously; a failing backend close is the caller's to see, and here nobody waits.
	//portlint:allow ctxbackground session.ts's guarded-storage failure starts close with no caller to cancel it; nobody waits on this close
	//portlint:allow ctxmapping the close is not the caller's work: ctx only names the span the failure is reported under, and may hold the failing commit's line ticket
	session.beginClose(context.WithoutCancel(context.Background()))
	session.report(ctx, err, durable.ReportInfo{Kind: durable.ReportStorageFailed})
}

// Touch is internal, on the line: the gate of the newest unpublished writer of key (for AllKeys, of anything unpublished); nil
// when there is nothing to wait for. A job's every read touches its key first, so it never observes state an earlier job
// admitted and the writer has not published. A hit marks the job on the line (session.ts touch).
//
// It takes only the writer's own lock and the job lock, never one a publication holds across its observers, so it is safe to
// call while holding a lock an observer takes. Once the gate fired, AssertHealthy checks the Session; AwaitTouch does both.
func (session *SessionImpl) Touch(key string) <-chan struct{} {
	var gate <-chan struct{}
	if key == AllKeys {
		gate = session.writer.Drained()
	} else {
		gate = session.writer.Gate(key)
	}
	if gate == nil {
		return nil
	}
	session.jobMu.Lock()
	if session.current != nil {
		session.current.hit = true
	}
	session.jobMu.Unlock()
	return gate
}

// AwaitTouch is Touch as a blocking call, which also checks that the Session is healthy once the gate opened. It returns at once
// when key has no unpublished writer. A gate always opens, by a publication or by the Session's failure, so the wait is not
// cancellable (the context is only the caller's, as GatedStorage's touch passes it).
func (session *SessionImpl) AwaitTouch(ctx context.Context, key string) error {
	gate := session.Touch(key)
	if gate == nil {
		return nil
	}
	hold(continuationOf(ctx), gate)
	return session.assertHealthy()
}

// Drained is internal: a channel closed once every part admitted so far is published or never will be; nil when none is
// pending (session.ts writer.drained, the gate of AllKeys).
func (session *SessionImpl) Drained() <-chan struct{} { return session.writer.Drained() }

// awaitDrained blocks until every part admitted so far is published or never will be: the `*` barrier.
func (session *SessionImpl) awaitDrained() {
	if drained := session.writer.Drained(); drained != nil {
		await(drained)
	}
}

// OnJobEnd is internal, on the line: it calls end once the outcome of the job on the line settles, its commit published or never
// to be; at once off the line (session.ts onJobEnd). end runs on the goroutine that settles the outcome, so it must be quick and
// never block.
func (session *SessionImpl) OnJobEnd(end func()) {
	session.jobMu.Lock()
	job := session.current
	if job != nil {
		job.ends = append(job.ends, end)
	}
	session.jobMu.Unlock()
	if job == nil {
		end()
	}
}

// LineSizes is what the line holds, for tests that bound memory: all three are zero once the line is idle.
type LineSizes struct {
	// Outstanding is the number of jobs whose outcome is not delivered yet.
	Outstanding int
	// Unpublished is the number of keys with an unpublished writer.
	Unpublished int
	// Reads is the number of record reads in flight in the cache.
	Reads int
}

// LineSizes reports the sizes of the line's bookkeeping (session.ts lineSizes).
func (session *SessionImpl) LineSizes() LineSizes {
	session.fenceMu.Lock()
	outstanding := session.outstanding
	session.fenceMu.Unlock()
	sizes := LineSizes{Outstanding: outstanding, Unpublished: session.writer.Unpublished()}
	if session.cached != nil {
		sizes.Reads = session.cached.ReadsInFlight()
	}
	return sizes
}

// touchEntries waits, on the line, for what an unpublished commit wrote of a conversation's entries, its appends and its
// creation, as reading them from Storage fails for a conversation not stored yet.
func (session *SessionImpl) touchEntries(ctx context.Context, conversationId durable.ConversationId) error {
	if err := session.AwaitTouch(ctx, ConversationKey(conversationId)); err != nil {
		return err
	}
	return session.AwaitTouch(ctx, AppendKey(conversationId))
}

// LatestEntryId is internal, on the Session line: the newest entry ID visible from conversationId, nil when it has none. Known
// from the Session's own commits when it owns its Storage, so it costs no Storage read after the first.
func (session *SessionImpl) LatestEntryId(ctx context.Context, conversationId durable.ConversationId) (*durable.EntryId, error) {
	if err := session.touchEntries(ctx, conversationId); err != nil {
		return nil, err
	}
	read := func() (*durable.EntryId, error) {
		page, err := session.storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: conversationId}, 1, nil)
		if err != nil || len(page.Items) == 0 {
			return nil, err
		}
		id := page.Items[0].Id
		return &id, nil
	}
	if session.tails == nil {
		return read()
	}
	return session.tails.Latest(conversationId, read)
}

// KnownLatestEntryId is internal: the newest entry ID of conversationId as known from the Session's commits right now, without
// reading. known is false when it is not known (and always for a read-only Session); a nil id is a conversation with no
// entries.
func (session *SessionImpl) KnownLatestEntryId(conversationId durable.ConversationId) (id *durable.EntryId, known bool) {
	if session.tails == nil {
		return nil, false
	}
	latest, state := session.tails.Peek(conversationId)
	switch state {
	case TailKnown:
		return &latest, true
	case TailNone:
		return nil, true
	}
	return nil, false
}

// LatestHeadMarker is internal, on the Session line: the newest visible head marker of conversationId at or before atOrBefore,
// any when nil; known from the Session's own commits as LatestEntryId is.
func (session *SessionImpl) LatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, atOrBefore *durable.EntryId) (*durable.EntryRecord, error) {
	if err := session.touchEntries(ctx, conversationId); err != nil {
		return nil, err
	}
	read := func() (*HeadMarker, error) {
		return session.storage.FindLatestHeadMarker(ctx, conversationId, atOrBefore)
	}
	if session.tails == nil {
		return read()
	}
	return session.tails.Marker(conversationId, atOrBefore, read)
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
// to appended entries. It returns once the commit is stored and published and every earlier job's outcome is delivered.
func (session *SessionImpl) CommitWith(ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope, options ...durable.CommitOptions) (any, error) {
	return session.commitWith(ctx, change, scope, false, options)
}

// CommitInternal is internal: CommitWith for a commit of the scheduler's own whose outcome goes only to scheduler code. When it
// writes nothing and waited at no gate, it is equivalent to having run before every unpublished commit, so its outcome is
// delivered at once, ahead of theirs. One that writes is never exempt: its outcome waits for its own publication and every
// earlier outcome, so no invocation starts on a reservation that is not durable (session.ts commitInternal).
func (session *SessionImpl) CommitInternal(ctx context.Context, change func(tx *Transaction) (any, error)) (any, error) {
	return session.commitWith(ctx, change, TransactionScope{}, true, nil)
}

func (session *SessionImpl) commitWith(ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope, exempt bool, options []durable.CommitOptions) (any, error) {
	withinMs, err := session.admitCommit(options)
	if err != nil {
		return nil, err
	}
	s, err := session.admitSlot(true, true)
	if err != nil {
		return nil, err
	}
	return runFenced(session, s, exempt, lineCallOf(ctx), func(index int) (ran[any], error) {
		return session.runCommit(ctx, change, scope, withinMs, index)
	})
}

// WriteAtClose is for BeforeClose (session.ts writeAtClose): on the line, after every commit admitted before the seal has
// settled and been published, it writes what writes returns then through the writer, which waits for it. Nothing is
// published, since the Session is closing; a failed Session writes nothing.
func (session *SessionImpl) WriteAtClose(ctx context.Context, writes func() []durable.StorageWrite) error {
	_, err := maintain(session, func(index int) (struct{}, error) {
		session.awaitDrained()
		if session.failure() != nil {
			return struct{}{}, nil
		}
		batch := writes()
		if len(batch) == 0 {
			return struct{}{}, nil
		}
		part := session.writer.Admit(PartSpec{
			Index:   index,
			Writes:  batch,
			Ctx:     context.WithoutCancel(ctx),
			Publish: func(durable.Seq) *PublishFailure { return nil },
			Discard: func() {},
		})
		return struct{}{}, part.Wait()
	})
	return err
}

// LineTicket is a position on the mutation line taken before its job runs.
type LineTicket struct {
	session *SessionImpl
	slot    *slot
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
	return &LineTicket{session: session, slot: session.takeSlotLocked(true)}
}

// Release gives up the position. Jobs queued behind it keep waiting for the jobs queued before it.
func (ticket *LineTicket) Release() {
	s := ticket.slot
	if s.previous != nil {
		await(s.previous)
	}
	// Nothing to deliver: the position stays in the fence only so that the deliveries after it are not held up by it.
	delivery := s.delivery
	ticket.session.fenceMu.Lock()
	delivery.skip, delivery.ready = true, true
	ticket.session.fenceMu.Unlock()
	ticket.session.releaseLine(s)
	ticket.session.kickDeliver()
}

// CommitWithTicket runs a commit at the ticket's position and releases it.
func (session *SessionImpl) CommitWithTicket(ticket *LineTicket, ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope, options ...durable.CommitOptions) (any, error) {
	return session.commitWithTicket(ticket, ctx, change, scope, false, options)
}

// CommitInternalWithTicket is CommitInternal at the ticket's position.
func (session *SessionImpl) CommitInternalWithTicket(ticket *LineTicket, ctx context.Context, change func(tx *Transaction) (any, error)) (any, error) {
	return session.commitWithTicket(ticket, ctx, change, TransactionScope{}, true, nil)
}

func (session *SessionImpl) commitWithTicket(ticket *LineTicket, ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope, exempt bool, options []durable.CommitOptions) (any, error) {
	if ticket == nil {
		return session.commitWith(ctx, change, scope, exempt, options)
	}
	return runFenced(session, ticket.slot, exempt, lineCall{continuation: continuationOf(ctx)}, func(index int) (ran[any], error) {
		withinMs, err := session.admitCommit(options)
		if err != nil {
			return ran[any]{}, err
		}
		return session.runCommit(ctx, change, scope, withinMs, index)
	})
}

// ReadWithTicket runs a read-only job at the ticket's position on the mutation line and releases it. The job reads through
// storage, whose reads wait for the publication of what the line admitted before.
func (session *SessionImpl) ReadWithTicket(ticket *LineTicket, job func(storage durable.Storage) (any, error)) (any, error) {
	return session.readWithTicket(ticket, lineCall{}, job)
}

// ReadWithTicketCtx is ReadWithTicket for a caller whose ctx carries a Continuation (WithContinuation).
func (session *SessionImpl) ReadWithTicketCtx(ticket *LineTicket, ctx context.Context, job func(storage durable.Storage) (any, error)) (any, error) {
	return session.readWithTicket(ticket, lineCallOf(ctx), job)
}

func (session *SessionImpl) readWithTicket(ticket *LineTicket, call lineCall, job func(storage durable.Storage) (any, error)) (any, error) {
	if ticket == nil {
		return session.readOnLine(call, job)
	}
	return runFenced(session, ticket.slot, false, lineCall{continuation: call.continuation}, func(int) (ran[any], error) {
		if err := session.assertUsable(); err != nil {
			return ran[any]{}, err
		}
		value, err := job(session.gated)
		return ran[any]{value: value}, err
	})
}

// OwnsTransaction is internal: whether tx is the transaction of one of this Session's commits; it returns that
// transaction (session.ts `ownsTransaction`).
func (session *SessionImpl) OwnsTransaction(tx durable.Tx) (*Transaction, bool) {
	return transactionOwnedBy(tx, session.host)
}

// ReadOnLine runs a read-only job on the mutation line so multi-read derivations observe one committed state. It reads through
// storage, a view whose reads wait for the publication of what the line admitted before; its outcome comes after theirs
// (session.ts readOnLine).
func (session *SessionImpl) ReadOnLine(job func(storage durable.Storage) (any, error)) (any, error) {
	return session.readOnLine(lineCall{}, job)
}

// ReadOnLineCtx is ReadOnLine for a caller whose ctx carries a line admission callback (internal/lineadmission): it runs
// once the job holds its position on the line, as a synchronous caller has extended upstream's Promise chain.
func (session *SessionImpl) ReadOnLineCtx(ctx context.Context, job func(storage durable.Storage) (any, error)) (any, error) {
	return session.readOnLine(lineCallOf(ctx), job)
}

func (session *SessionImpl) readOnLine(call lineCall, job func(storage durable.Storage) (any, error)) (any, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	return enqueue(session, true, call, func() (any, error) {
		if err := session.assertHealthy(); err != nil {
			return nil, err
		}
		return job(session.gated)
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
		loaded, err = enqueue(session, true, lineCallOf(ctx), func() (*LoadedDocument, error) {
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

func (observer watchObserver) Advance(ctx context.Context, call durable.WatchCall[durable.JsonObject]) {
	observer.watch.Advance(ctx, call)
}

func (observer watchObserver) End(end durable.WatchEnd) { observer.watch.EndWith(end) }

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
			watch.ObserveAbort(ctx)
		}
		return watch, nil
	}, (*chord.Watch[durable.JsonObject]).Abort)
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
		}, func(observer durable.Observer[durable.JsonObject]) {
			observer.End(durable.WatchEnd{Kind: durable.WatchEnded})
		})
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
	attached, err := enqueue(session, true, lineCallOf(ctx), func() (A, error) {
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
	return enqueue(session, true, lineCallOf(ctx), func() (durable.JsonObject, error) {
		if err := session.assertHealthy(); err != nil {
			return nil, err
		}
		storedEntry, err := session.gated.VisibleEntry(ctx, conversationId, at)
		if err != nil {
			return nil, err
		}
		if storedEntry == nil {
			return nil, fmt.Errorf("Entry %d is not visible from conversation %d", at, conversationId)
		}
		address := resolved.Address
		address.Scope = durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: storedEntry.Entry.ConversationId}
		point := durable.AtSeq(storedEntry.CommitSeq)
		record, err := session.gated.FindDocument(ctx, address, point)
		if err != nil || record == nil {
			return nil, err
		}
		stored, err := session.gated.Document(ctx, record.Id, point)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, fmt.Errorf("Historical document %d (%s) cannot be read", record.Id, record.Kind)
		}
		return materializeValue(definition, stored.Record.AsCreate(), stored.Version, stored.Value)
	})
}

// Close seals admission, settles admitted commits, then closes Storage. Close listeners run synchronously when close begins,
// before the BeforeClose hook. Close drains the writer: the commits in flight and their publication, the held changes stored
// through it. Cancelling ctx stops waiting without interrupting the close; an already cancelled ctx returns its cause at once, as
// upstream's awaitWithContext rejects for an aborted signal.
func (session *SessionImpl) Close(ctx context.Context) error {
	closing := session.beginClose(ctx)
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if err := Wait(ctx, closing); err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.closeErr
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
		//portlint:allow asyncorder joined: Close waits on closing, and listenersRan orders beforeClose after the synchronous listeners as session.ts runs beforeClose on a later microtask
		go func() {
			defer close(closing)
			<-listenersRan
			if session.hooks.BeforeClose != nil {
				session.hooks.BeforeClose()
			}
			_, err := maintain(session, func(int) (struct{}, error) {
				// Every admitted commit is published (or will never be) before anything else: the calls in flight, and what
				// the line admitted, drain through the writer.
				session.awaitDrained()
				// Last, after the writes at close (BeforeClose), while the trackers are still here. A failed Session writes
				// nothing; a failing store fails it, and the backend still closes.
				var unstored error
				if session.failure() == nil && session.holdCount() > 0 {
					unstored = session.storeHolds(cleanup, session.allHolds)
				}
				// The writer's goroutine ended: nothing is queued or in flight.
				session.writer.Wait()
				// The outcomes of the jobs admitted before the seal are delivered, in line order, and their callers have resumed,
				// before Storage closes: what a caller reads off the line right after its job (a context read derives its view from
				// Storage) starts before the backend closes, as upstream's continuation precedes the close job's later awaits.
				session.awaitFenceEmpty()
				session.mu.Lock()
				observers := session.commitOrder
				session.commitOrder = nil
				session.internalOrder = nil
				clear(session.documents)
				session.cache.clearIdle()
				clear(session.cache.attached)
				clear(session.holds)
				session.holdOrder = nil
				clear(session.taskDocuments)
				session.mu.Unlock()
				// After the last publication: the commits admitted before the seal have settled, held changes are stored.
				_, end := CloseEnds(session.failure())
				for _, listener := range observers {
					listener.observer.End(end)
				}
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
				session.end = durable.SessionEnd{Kind: durable.SessionEndClosed}
			} else {
				session.end = durable.SessionEnd{Kind: durable.SessionEndFailed, Error: session.failureErr}
			}
			session.mu.Unlock()
			if reported {
				session.report(unscoped, err, durable.ReportInfo{Kind: durable.ReportStorageFailed})
			}
			// The outcomes of the jobs admitted before the seal are delivered, in line order, before the Session ends.
			session.awaitFenceEmpty()
			session.endLifetime()
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
			onError(panicError(recovered))
		}
	}()
	callback()
}

// ObserveCommits attaches a synchronous observer of every stored commit, with exactly what it stored. Its Advance runs once
// the commit is published, right after the Session's own observers (and inside the Storage's publication scope when it has one),
// on the writer's goroutine, before every wait the commit resolves and every abort it queues (in no order with watch listeners);
// a commit published while no observer was attached has no dispatch. It must not block or call Session operations. A panic is
// reported, detaches it, and ends it listener_error; End gets the Session's close after its last publication.
func (session *SessionImpl) ObserveCommits(observer durable.CommitObserver) durable.CommitObservation {
	observation, err := session.observeCommitsOf(observer)
	if err != nil {
		panic(err)
	}
	return observation
}

func (session *SessionImpl) observeCommitsOf(observer durable.CommitObserver) (durable.CommitObservation, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	entry := &commitListener{}
	entry.observer = guardCommitObserver(observer, func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		for index, candidate := range session.commitOrder {
			if candidate == entry {
				session.commitOrder = slices.Delete(session.commitOrder, index, index+1)
				return
			}
		}
	}, session.reportListener, session.reportListenerIn)
	session.mu.Lock()
	session.commitOrder = append(session.commitOrder, entry)
	session.mu.Unlock()
	return entry.observer, nil
}

// FollowCommits follows every commit as memory adopts it (ObservedCommit), for the Session's own components, whose state
// follows memory. A panic leaves that state behind, so it fails the Session instead of being reported: once the host listeners
// got the commit. They run at the commit's publication, on the writer's goroutine, so they call no Storage method, run no task
// code, and contain every host callback; the waits they resolve and the aborts they queue (DeferUntilPublished) run after the
// host listeners.
func (session *SessionImpl) FollowCommits(listener func(ctx context.Context, commit ObservedCommit)) func() {
	unsubscribe, err := session.followCommits(listener)
	if err != nil {
		panic(err)
	}
	return unsubscribe
}

func (session *SessionImpl) followCommits(listener func(ctx context.Context, commit ObservedCommit)) (func(), error) {
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
// observeCommits (harness-lifecycle.test.ts:331); a Go method cannot be patched.
func (session *SessionImpl) CommitSubscriptions() int {
	session.mu.Lock()
	defer session.mu.Unlock()
	return len(session.commitOrder)
}

// ObserverSubscriptions reports the number of live observers of the Session's own (FollowCommits): a document watch holds
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

// GateTouches reports how many Gate calls returned the gate of key's newest unpublished writer, whoever made them (a job's
// Touch, a retention probe), so a test can wait until the one caller it expects has reached the wait for that publication
// without sleeping.
func (session *SessionImpl) GateTouches(key string) int { return session.writer.Touches(key) }

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

// UnloadDocuments drops every loaded tracker on the mutation line, held changes stored first, through the writer after the
// calls in flight; later access cold-loads from Storage.
func (session *SessionImpl) UnloadDocuments() error {
	_, err := maintain(session, func(int) (struct{}, error) {
		if session.failure() == nil {
			if err := session.storeHolds(unscoped, session.allHolds); err != nil {
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

// AwaitDeliveriesEntered blocks until every watch delivery scheduled before the call has entered: the synchronous part of its
// listener ran (the call returned), or it went on to a wait of the Session (chord.DeliveryEntered). A Harness wait that resumes its
// caller calls it, as upstream's drain microtask runs the listener up to its first await before the code awaiting the wait's
// promise resumes. A call from inside a delivery (a listener that waits for the Harness) enters that delivery and does not
// wait for it. A listener blocked on a plain lock or channel the Session cannot see has not entered until it returns: if the
// caller holds that lock the wait does not return, the one gap Go has where upstream, with no locks, has none
// (pig divergence (D224); the listener calls chord.DeliveryEntered before such a block). It returns the cause of ctx when ctx
// ends the wait.
func (session *SessionImpl) AwaitDeliveriesEntered(ctx context.Context) error {
	return session.scheduler.AwaitEntered(ctx)
}

func (session *SessionImpl) runCommit(ctx context.Context, change func(tx *Transaction) (any, error), scope TransactionScope, withinMs *float64, index int) (ran[any], error) {
	if err := session.assertHealthy(); err != nil {
		return ran[any]{}, err
	}
	if ctx.Err() != nil {
		return ran[any]{}, context.Cause(ctx)
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
	result, err := change(tx)
	settled = true
	if err == nil {
		// A callback that caught a failed read must not commit, nor succeed as if it had.
		err = session.assertHealthy()
	}
	if err != nil {
		tx.settleFailure()
		session.sweepDocuments()
		return ran[any]{}, err
	}
	writes, err := tx.settleSuccess()
	if err != nil {
		// Settlement aborted every change already.
		session.sweepDocuments()
		return ran[any]{}, err
	}
	if len(writes) == 0 {
		tx.discard()
		session.sweepDocuments()
		return ran[any]{value: result}, nil
	}
	if session.hooks.ReadOnly {
		// Refused before Storage: the Session stays healthy, and the caller sees why.
		tx.discard()
		session.sweepDocuments()
		return ran[any]{}, durable.NewReadOnlySession("a commit with writes")
	}
	if withinMs != nil {
		if held, ok := tx.holdable(writes); ok {
			// Adopted in memory when the commits before it are published, as a part of no storage call.
			part := session.writer.Admit(PartSpec{
				Index:   index,
				Keys:    tx.WriteKeys(),
				Ctx:     ctx,
				Publish: func(durable.Seq) *PublishFailure { return session.hold(ctx, tx, held, *withinMs) },
				Discard: tx.discard,
			})
			session.sweepDocuments()
			return ran[any]{value: result, after: part}, nil
		}
	}
	var membership []string
	if session.hooks.MembershipKeys != nil {
		membership, err = session.hooks.MembershipKeys(tx)
		if err != nil {
			tx.discard()
			session.sweepDocuments()
			return ran[any]{}, err
		}
	}
	// Logical admission. Held changes leave their queue only here, once nothing but Storage can fail; a commit never stores a
	// hold adopted after it in line order. Taken before the keys, so the documents it stores held changes of are its writes
	// too: a later job that reads one waits for it instead of storing them again.
	var taken []*heldDocument
	if session.holdCount() > 0 {
		writes, taken = tx.takeHeld(writes, session.heldOf)
	}
	keys := append(tx.WriteKeys(), membership...)
	part := session.writer.Admit(PartSpec{
		Index:  index,
		Writes: writes,
		Keys:   keys,
		Ctx:    ctx,
		Publish: func(seq durable.Seq) *PublishFailure {
			return session.adopt(tx, writes, taken, seq, ctx)
		},
		// Once its keys left: the held documents it stored unpin, and an eviction is scheduled that has to see the keys gone.
		Published: func() {
			session.finishRelease(taken)
			session.scheduleSweepNow()
		},
		Discard: tx.discard,
	})
	session.sweepDocuments()
	return ran[any]{value: result, after: part}, nil
}

// adopt publishes a stored commit (Writer), after the read caches took its writes: it adopts the commit in memory and runs its
// publication. A failure of the adoption leaves memory behind durable state; the writer fails the Session with it.
func (session *SessionImpl) adopt(tx *Transaction, writes []durable.StorageWrite, taken []*heldDocument, seq durable.Seq, ctx context.Context) *PublishFailure {
	documents, err := tx.adopt(seq)
	if err != nil {
		return &PublishFailure{Kind: PublishAdoption, Err: err}
	}
	session.indexTaskDocuments(tx, writes, documents, seq)
	failure := session.publish(ctx, seq, writes, documents)
	session.forget(taken)
	// The documents it changed are used, while its keys are still unpublished: no sweep runs between the keys' removal and this.
	session.refreshChanged(documents.stored)
	if failure != nil {
		return &PublishFailure{Kind: PublishListener, Err: failure}
	}
	return nil
}

// indexTaskDocuments keeps taskDocuments in step with a stored commit at its publication: the tasks it creates start with no
// documents, task documents it creates join their task, ones it retires leave, and tasks it ends leave the index.
func (session *SessionImpl) indexTaskDocuments(tx *Transaction, writes []durable.StorageWrite, documents adoptedDocuments, seq durable.Seq) {
	session.mu.Lock()
	defer session.mu.Unlock()
	for _, id := range tx.CreatedTaskIds() {
		session.taskDocuments[id] = &taskDocuments{records: map[durable.DocumentId]durable.DocumentRecord{}}
	}
	if len(session.taskDocuments) == 0 {
		return
	}
	for _, change := range documents.stored {
		var record durable.DocumentRecord
		retire := false
		switch typed := change.(type) {
		case durable.DocumentChange:
			record = typed.Record
		case durable.DocumentRetireChange:
			record, retire = typed.Record, true
		default:
			continue
		}
		if record.Scope.Kind != durable.ScopeTask {
			continue
		}
		known := session.taskDocuments[record.Scope.TaskId]
		if known == nil {
			continue
		}
		switch {
		case retire:
			if _, present := known.records[record.Id]; present {
				delete(known.records, record.Id)
				known.order = slices.DeleteFunc(known.order, func(id durable.DocumentId) bool { return id == record.Id })
			}
		case record.CreatedAt == seq:
			if _, present := known.records[record.Id]; !present {
				known.order = append(known.order, record.Id)
			}
			known.records[record.Id] = record
		}
	}
	for _, write := range writes {
		if task, ok := write.(durable.TaskWrite); ok && task.Value.State.Status == durable.TaskTerminal {
			delete(session.taskDocuments, task.Value.Id)
		}
	}
}

// taskDocumentsOf is the current documents of a live task when the Session knows all of them (it created the task); known is
// false otherwise.
func (session *SessionImpl) taskDocumentsOf(taskId durable.TaskId) (records []durable.DocumentRecord, known bool) {
	session.mu.Lock()
	defer session.mu.Unlock()
	documents := session.taskDocuments[taskId]
	if documents == nil {
		return nil, false
	}
	records = make([]durable.DocumentRecord, 0, len(documents.order))
	for _, id := range documents.order {
		records = append(records, documents.records[id])
	}
	return records, true
}

// publication is the commit being published: the dispatch to ObserveCommits observers adopt queued, and the work the
// observers deferred until after it (DeferUntilPublished). It runs when the part's publication ended (endPublication).
type publication struct {
	dispatch []func()
	deferred []func()
}

// beginPublication starts the publication of a part: from here until endPublication ran, the work its observers defer
// (DeferUntilPublished) waits.
func (session *SessionImpl) beginPublication() {
	session.deferMu.Lock()
	session.pub = &publication{}
	session.deferMu.Unlock()
}

// endPublication ends the publication of a part, once its own observers ran: it runs the dispatch the publication queued with
// the listeners registered now, then the work the observers deferred: the waits they resolved and the aborts they queued, and
// returns once the registered waits those woke (Park) have resumed. Upstream's dispatch is one microtask after the publication,
// before every wait it resolves.
func (session *SessionImpl) endPublication() {
	for {
		session.deferMu.Lock()
		pub := session.pub
		if pub == nil {
			session.deferMu.Unlock()
			return
		}
		switch {
		case len(pub.dispatch) > 0:
			next := pub.dispatch[0]
			pub.dispatch = pub.dispatch[1:]
			session.deferMu.Unlock()
			next()
		case len(pub.deferred) > 0:
			wakes := pub.deferred
			pub.deferred = nil
			session.deferMu.Unlock()
			for _, wake := range wakes {
				wake()
			}
			session.awaitReadied()
		default:
			session.pub = nil
			session.deferMu.Unlock()
			return
		}
	}
}

// publish publishes a stored commit at its publication: its telemetry events first, then the dispatch to ObserveCommits
// listeners, queued before the waits and aborts the observers defer, then the Session's own observers. It never panics: it
// returns the first error of an own observer, which fails the Session once the commit's storage call settled.
func (session *SessionImpl) publish(ctx context.Context, seq durable.Seq, writes []durable.StorageWrite, documents adoptedDocuments) error {
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
	// One synchronous turn, as Pi's publication is: the followed views (chord follow.ts) the observers advance are told
	// together, when the loop ends, and reach their host as one batch. Only the Session's own observers get the step's
	// context; a host listener (notifyHosts) gets the commit's own.
	stepCtx, endStep := chordctx.WithStep(ctx)
	defer endStep()
	for _, listener := range internal {
		contained(func() { listener.listener(stepCtx, commit) }, func(err error) {
			if failure == nil {
				failure = err
			}
		})
	}
	endStep()
	return failure
}

// event records a commit's telemetry event at its publication. A panicking adapter is reported, as publication must not panic.
func (session *SessionImpl) event(ctx context.Context, span telemetry.TelemetrySpan, name string, attributes telemetry.SpanAttributes) {
	contained(func() { span.AddEvent(name, attributes) }, func(err error) {
		session.report(ctx, err, durable.ReportInfo{Kind: durable.ReportHostError, Callback: durable.HostCallbackTelemetry})
	})
}

// notifyHosts queues, at a part's publication, the dispatch of publication to the ObserveCommits observers: it runs once the
// publication's own observers ran (endPublication), with the listeners registered then, and a panic is reported.
func (session *SessionImpl) notifyHosts(ctx context.Context, publication durable.CommitPublication) {
	dispatch := func() {
		session.mu.Lock()
		listeners := append([]*commitListener(nil), session.commitOrder...)
		session.mu.Unlock()
		for _, listener := range listeners {
			listener.observer.Advance(ctx, publication)
		}
	}
	session.deferMu.Lock()
	if session.pub == nil {
		session.deferMu.Unlock()
		dispatch()
		return
	}
	session.pub.dispatch = append(session.pub.dispatch, dispatch)
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
// call it from the publishing goroutine, as publications are serialized by the writer. Once the work ran, the publication
// returns only when the registered waits it readied (Park, Await) have resumed, so the next part of the call is published after
// what this one resolved started (the hop). A wait that is not registered is not waited for.
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
	commit, err := session.followCommits(func(ctx context.Context, publication ObservedCommit) {
		// Listeners run from a copy of the set: another observer's call may have detached this one.
		if !isAttached() {
			return
		}
		ownerEnds := false
		for _, change := range publication.Changes {
			var (
				document durable.DocumentChange
				record   durable.DocumentRecord
				retired  bool
			)
			switch typed := change.(type) {
			case durable.TaskWrite:
				if address.Scope.Kind == durable.ScopeTask && typed.Value.Id == address.Scope.TaskId && typed.Value.State.Status == durable.TaskTerminal {
					ownerEnds = true
				}
				continue
			case durable.DocumentChange:
				document, record = typed, typed.Record
			case durable.DocumentRetireChange:
				record, retired = typed.Record, true
			default:
				continue
			}
			mu.Lock()
			current := followed && record.Id == incarnation
			mu.Unlock()
			// The incarnation followed is at the address by definition; only another record needs the address compared.
			if !current && !atAddress(record, address) {
				continue
			}
			switch {
			case retired:
				if !current {
					continue
				}
				mu.Lock()
				last := value
				followed, value = false, nil
				release := unpin
				mu.Unlock()
				release()
				observer.Advance(ctx, durable.WatchCall[durable.JsonObject]{Kind: durable.WatchCallValue, Value: last, Retired: true})
			case !current || document.Version != version:
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
				version, value = document.Version, document.Value
				mu.Unlock()
				observer.Advance(ctx, durable.WatchCall[durable.JsonObject]{Kind: durable.WatchCallValue, Value: document.Value, Ops: []durable.Op{{"r", document.Value}}})
			default:
				// A migration-only base changes nothing for an observer of the new version.
				if len(document.Ops) == 0 {
					continue
				}
				mu.Lock()
				value = document.Value
				mu.Unlock()
				observer.Advance(ctx, durable.WatchCall[durable.JsonObject]{Kind: durable.WatchCallValue, Value: document.Value, Ops: document.Ops})
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
		watch, other := CloseEnds(session.failure())
		if _, follows := observer.(watchObserver); follows {
			observer.End(watch)
		} else {
			observer.End(other)
		}
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
	// What an unpublished commit wrote at the address is in neither the cache nor what Storage shows yet.
	if err := session.AwaitTouch(ctx, DocumentKey(addressId)); err != nil {
		return nil, err
	}
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
		// The reload must find what memory holds: held changes are stored before their tracker goes, after a barrier.
		id := cached.Record.Id
		if err := session.storeHolds(ctx, func() []*heldDocument {
			if hold := session.heldOf(id); hold != nil {
				return []*heldDocument{hold}
			}
			return nil
		}); err != nil {
			return nil, err
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
