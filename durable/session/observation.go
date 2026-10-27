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

// NewSessionNativeWatch is NewSessionWatch for a native value (events.ts): every value is delivered in order, one call each;
// replace gives the value an overflow delivers, and nil delivers the newest value.
func NewSessionNativeWatch[T any](session *SessionImpl, value T, detach func(), replace func() T) *chord.NativeWatch[T] {
	return chord.NewNativeWatch(value, detach, session.reportListener, replace).On(session.scheduler)
}

// GuardedObserver is a host's Observer behind the rule for observers that run inside Storage's adopt (session.ts
// guardObserver): one that panics is reported through report, detached, and ended with the error, so the panic never reaches
// adopt; nothing reaches it after its detach or end, and from then on the guard references neither it nor its source.
type GuardedObserver[T any] struct {
	mu       sync.Mutex
	attached *guardedObserver[T]
}

type guardedObserver[T any] struct {
	observer durable.Observer[T]
	detach   func()
	report   func(error)
}

// GuardObserver guards observer. detach is the source's; the guard's Detach is the observation's.
func GuardObserver[T any](observer durable.Observer[T], detach func(), report func(error)) *GuardedObserver[T] {
	return &GuardedObserver[T]{attached: &guardedObserver[T]{observer: observer, detach: detach, report: report}}
}

// release detaches from the source once, and hands back what the guard was attached with.
func (guard *GuardedObserver[T]) release() *guardedObserver[T] {
	guard.mu.Lock()
	released := guard.attached
	guard.attached = nil
	guard.mu.Unlock()
	if released != nil {
		released.detach()
	}
	return released
}

// Advance passes a change on; a panic of the observer is reported, and detaches and ends it with the error.
func (guard *GuardedObserver[T]) Advance(ctx context.Context, change durable.WatchChange[T]) {
	guard.mu.Lock()
	current := guard.attached
	guard.mu.Unlock()
	if current == nil {
		return
	}
	contained(func() { current.observer.Advance(ctx, change) }, func(err error) {
		current.report(err)
		if guard.release() != nil {
			contained(func() { current.observer.End(err) }, current.report)
		}
	})
}

// End ends the observer once, with the failure of the Session, or nil when it closed.
func (guard *GuardedObserver[T]) End(failure error) {
	if ending := guard.release(); ending != nil {
		contained(func() { ending.observer.End(failure) }, ending.report)
	}
}

// Detach detaches the observer from its source; it is idempotent.
func (guard *GuardedObserver[T]) Detach() { guard.release() }

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
