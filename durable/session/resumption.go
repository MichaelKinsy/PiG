package session

// The handshake that orders the continuations of what a publication resolved before the next publication.
//
// Pi's publication resolves promises, and the continuations of the waits they resolve (commit(), Submission.wait(),
// waitForTask()) start in the microtasks that run before the macrotask hop to the next publication of the same storage call
// (docs/spec.md "Two commits of one storage call are published a macrotask apart", docs/group-commit.md section 5.3). A Go
// wake is a close or a send that readies a goroutine, which the runtime runs when it likes; the Session cannot tell when it
// ran unless the wait says so. So the waits of the Session signal it, explicitly, without reading the state of goroutines:
//
//   - A wait registers before it blocks (Park, or Await for a wait on one channel) and acknowledges when it resumed
//     (Parked.Resume), as the first thing the goroutine does after its wait returned.
//   - After the wakes of a publication ran, the writer waits for the acknowledgement of every registered wait whose channel
//     they closed (awaitReadied); the delivery goroutine waits for the acknowledgement of every commit caller it released.
//   - The count of goroutines in the process takes no part, nor do their stacks: the cost of a publication is the number of
//     waits it readied, and the order holds at any number of goroutines.
//
// What the acknowledgement says is that the continuation started: the goroutine returned from its wait. Go cannot tell when
// code that is not the Session's reaches its next wait, as that is an ordinary channel receive or lock like any other, so
// the Session never waits for it: a wake of a goroutine that went on to wait for something else, for as long as that is,
// would otherwise stall every publication behind it. A caller whose continuation must be ordered whole, up to its next wait of
// the Session, brings a Continuation (WithContinuation) in the context of the call and says when it is there (Entered); the
// acknowledgement of its waits then comes from it, and also from the caller's next wait of the Session (a commit, a gate, a
// Park) with that context, as a continuation that goes on to wait has entered.
//
// Goroutines a wake readies that are not in a wait of the Session, such as one selecting on the cancelled context of a task,
// are not waited for: cancel returned when the context is cancelled, and what the goroutine does about it is its own.

import (
	"context"
	"sync"
	"sync/atomic"
)

type continuationKey struct{}

// Continuation is the code a caller runs after a wait of the Session returned, which the Session waits for, up to the point
// the caller says it is done (Entered) or waits in the Session again with the context of the Continuation. It is the
// analogue of a promise's continuation running up to its next await, for the caller that cannot do without that order; no
// caller needs one to be woken in line order.
type Continuation struct {
	mu sync.Mutex
	// pending are the resumptions held until the continuation has entered.
	pending []*resumption
}

// WithContinuation returns a context for Session calls whose continuation the Session waits for, and the Continuation: the
// caller must call Entered once the code that follows each call has run, or wait in the Session again with this context.
// Until it does, the publication of the next part of the storage call that woke it waits.
func WithContinuation(ctx context.Context) (context.Context, *Continuation) {
	continuation := &Continuation{}
	return context.WithValue(ctx, continuationKey{}, continuation), continuation
}

// continuationOf is the Continuation in ctx, or nil.
func continuationOf(ctx context.Context) *Continuation {
	if ctx == nil {
		return nil
	}
	continuation, _ := ctx.Value(continuationKey{}).(*Continuation)
	return continuation
}

// Entered says the code that followed the wait calls returned from ran up to here: the resumptions it held are acknowledged.
// It is safe to call on a nil Continuation and at any time.
func (continuation *Continuation) Entered() {
	if continuation == nil {
		return
	}
	continuation.mu.Lock()
	pending := continuation.pending
	continuation.pending = nil
	continuation.mu.Unlock()
	for _, held := range pending {
		held.acknowledge()
	}
}

// resumption is the acknowledgement of one goroutine a wake readied: resumed is closed once it started running.
type resumption struct {
	resumed chan struct{}
	once    sync.Once
	// continuation, when set, holds the acknowledgement until it has entered.
	continuation *Continuation
	// onAcknowledged, when set, runs once, as the acknowledgement is given.
	onAcknowledged func()
}

func newResumption(continuation *Continuation) resumption {
	return resumption{resumed: make(chan struct{}), continuation: continuation}
}

// resume is called by the woken goroutine as soon as its wait returned.
func (r *resumption) resume() {
	if r.continuation == nil {
		r.acknowledge()
		return
	}
	r.continuation.mu.Lock()
	r.continuation.pending = append(r.continuation.pending, r)
	r.continuation.mu.Unlock()
}

// acknowledge closes resumed; it is idempotent.
func (r *resumption) acknowledge() {
	r.once.Do(func() {
		if r.onAcknowledged != nil {
			r.onAcknowledged()
		}
		close(r.resumed)
	})
}

// Parked is a wait of the Session registered before it blocks: a publication that readies it waits for it to resume.
type Parked struct {
	resumption
	session *SessionImpl
	done    <-chan struct{}
}

// Park registers the wait the calling goroutine is about to block in until done is closed, and returns the Parked it calls
// Resume on as soon as the wait returned, however it returned. It returns nil, which Resume accepts, when done is closed
// already: nothing is going to be readied. A ctx that carries a Continuation (WithContinuation) is the caller's: the
// continuation has entered once it parks.
func (session *SessionImpl) Park(ctx context.Context, done <-chan struct{}) *Parked {
	select {
	case <-done:
		return nil
	default:
	}
	enter(continuationOf(ctx))
	parked := &Parked{resumption: newResumption(continuationOf(ctx)), session: session, done: done}
	// A wait stays registered until it is acknowledged, not only until it resumed: the acknowledgement of one that belongs to a
	// Continuation is given when the continuation has entered, and a publication that looks for the waits it readied after the
	// goroutine resumed must still find it.
	parked.onAcknowledged = func() {
		session.parkMu.Lock()
		delete(session.parks, parked)
		session.parkMu.Unlock()
	}
	session.parkMu.Lock()
	if session.parks == nil {
		session.parks = map[*Parked]struct{}{}
	}
	session.parks[parked] = struct{}{}
	session.parkMu.Unlock()
	return parked
}

// Resume acknowledges the wait: its goroutine runs again. It is safe to call on nil, and again.
func (parked *Parked) Resume() {
	if parked == nil {
		return
	}
	parked.resume()
}

// Abandon acknowledges a wait that ended without a publication having woken it (its context was cancelled, or a close rejected it): the
// goroutine's continuation is not one a publication orders (Pi orders what a publication resolves, not a rejected wait), so a Continuation
// does not hold the publications that follow. It is safe on nil, and again.
func (parked *Parked) Abandon() {
	if parked == nil {
		return
	}
	parked.acknowledge()
}

// Await blocks until done is closed, as a wait of the Session: Park, the receive, Resume.
func (session *SessionImpl) Await(ctx context.Context, done <-chan struct{}) {
	parked := session.Park(ctx, done)
	defer parked.Resume()
	<-done
}

// awaitReadied returns once every registered wait whose channel is closed has resumed: after the wakes of a publication ran,
// these are the goroutines they readied.
func (session *SessionImpl) awaitReadied() {
	session.parkMu.Lock()
	var readied []*Parked
	for parked := range session.parks {
		select {
		case <-parked.done:
			readied = append(readied, parked)
		default:
		}
	}
	session.parkMu.Unlock()
	for _, parked := range readied {
		session.awaitResumed(&parked.resumption, &session.awaitingWakes)
	}
}

// awaitResumed blocks until r is acknowledged; waiting counts it while it waits.
func (session *SessionImpl) awaitResumed(r *resumption, waiting *atomic.Int32) {
	select {
	case <-r.resumed:
		return
	default:
	}
	waiting.Add(1)
	<-r.resumed
	waiting.Add(-1)
}
