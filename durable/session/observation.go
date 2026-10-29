package session

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
)

// NewSessionWatch returns a Chord Watch whose deliveries run on the Session's delivery scheduler and whose listener
// failures go to the Session's report, as Pi's Durable builds each Watch with (error) => session.report(error)
// (session.ts #attachDocument, view.ts, task-graph.ts). detach runs once when the watch ends.
func NewSessionWatch[T any](session *SessionImpl, value T, detach func()) *chord.Watch[T] {
	return chord.NewWatch(value, detach, session.reportListener).On(session.scheduler)
}

// syncObserver is what a guard guards: an Observer[T] (A is WatchCall[T]) or a CommitObserver (A is CommitPublication).
type syncObserver[A any] interface {
	Advance(ctx context.Context, call A)
	End(end durable.WatchEnd)
}

// guard is a host's observer behind the rule for observers that run inside Storage's adopt (session.ts guardObserver): one
// that panics is reported through report, detached, and ended listener_error, so the panic never reaches adopt; nothing
// reaches it after its detach or end, and from then on the guard references neither it nor its source.
type guard[A any] struct {
	mu       sync.Mutex
	attached *guardedObserver[A]
}

type guardedObserver[A any] struct {
	observer syncObserver[A]
	detach   func()
	report   func(error)
	// reportIn, when set, reports an Advance's panic under the context of that call (session.ts passes it to report).
	reportIn func(ctx context.Context) func(error)
}

// GuardedObserver is a host's Observer[T] behind the guard. detach is the source's; the guard's Detach is the observation's.
type GuardedObserver[T any] struct {
	guard[durable.WatchCall[T]]
}

// GuardedCommitObserver is a host's CommitObserver behind the guard.
type GuardedCommitObserver struct {
	guard[durable.CommitPublication]
}

// GuardObserver guards observer.
func GuardObserver[T any](observer durable.Observer[T], detach func(), report func(error)) *GuardedObserver[T] {
	return &GuardedObserver[T]{guard[durable.WatchCall[T]]{attached: &guardedObserver[durable.WatchCall[T]]{observer: observer, detach: detach, report: report}}}
}

// guardCommitObserver guards observer.
func guardCommitObserver(observer durable.CommitObserver, detach func(), report func(error), reportIn func(context.Context) func(error)) *GuardedCommitObserver {
	return &GuardedCommitObserver{guard[durable.CommitPublication]{attached: &guardedObserver[durable.CommitPublication]{observer: observer, detach: detach, report: report, reportIn: reportIn}}}
}

// release detaches from the source once, and hands back what the guard was attached with.
func (g *guard[A]) release() *guardedObserver[A] {
	g.mu.Lock()
	released := g.attached
	g.attached = nil
	g.mu.Unlock()
	if released != nil {
		released.detach()
	}
	return released
}

// Advance passes a call on; a panic of the observer is reported, and detaches and ends it listener_error.
func (g *guard[A]) Advance(ctx context.Context, call A) {
	g.mu.Lock()
	current := g.attached
	g.mu.Unlock()
	if current == nil {
		return
	}
	report := current.report
	if current.reportIn != nil {
		report = current.reportIn(ctx)
	}
	contained(func() { current.observer.Advance(ctx, call) }, func(err error) {
		report(err)
		if g.release() != nil {
			end := durable.WatchEnd{Kind: durable.WatchListenerError, Error: err}
			contained(func() { current.observer.End(end) }, current.report)
		}
	})
}

// End ends the observer once, with end.
func (g *guard[A]) End(end durable.WatchEnd) {
	if ending := g.release(); ending != nil {
		contained(func() { ending.observer.End(end) }, ending.report)
	}
}

// Detach detaches the observer from its source; it is idempotent.
func (g *guard[A]) Detach() { g.release() }

// observation is the Observation of a guarded observer attached at value; it keeps nothing but value and the spent guard
// alive once detached.
type observation[T any] struct {
	value   T
	guarded interface{ Detach() }
}

func (o observation[T]) Value() T { return o.value }
func (o observation[T]) Detach()  { o.guarded.Detach() }

// NewObservation returns the Observation of a guarded observer attached at value (session.ts observation).
func NewObservation[T any](value T, guarded interface{ Detach() }) durable.Observation[T] {
	return observation[T]{value: value, guarded: guarded}
}
