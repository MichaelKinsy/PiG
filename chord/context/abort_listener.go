package chordctx

import (
	"context"
	"slices"
	"sync/atomic"
)

// Ports packages/chord/src/context/index.ts (lines 138-153, `awaitWithContext`): the abort listener of an AbortSignal. Pi's
// `signal.addEventListener("abort", listener)` runs the listener synchronously inside `abort()`, before it returns, on the one
// thread there is. A Go context has no such listener: context.AfterFunc runs on a goroutine of its own, later than the cancel
// and in no order against what the cancelling code does next. OnAbort is the listener a waiter needs to be ordered with the code
// that cancels.

// abortListener is one OnAbort registration, on every cancel of this package that can abort the context it listens to.
type abortListener struct {
	fn func(cause error)
	// seq orders the listeners of one signal as they were added, as an EventTarget runs its listeners; owner is the context
	// the listener was added to, whose creation orders the listeners of different contexts (see dispatch).
	seq   uint64
	owner *abortContext
	// done is set by the first of: a cancel that ran fn, the stop function.
	done    atomic.Bool
	sources []*abortContext
}

// listenerSeq numbers the registrations of OnAbort.
var listenerSeq atomic.Uint64

// fire runs fn once, unless a cancel ran it or stop kept it from running. A listener that ran is held by no source: a cancel
// clears the set of its own source only, and the others (the parent or signal it listens to as well) would keep a wait that
// ended for the life of their context.
func (l *abortListener) fire(cause error) {
	if l.done.CompareAndSwap(false, true) {
		l.unregister()
		l.fn(cause)
	}
}

// dispatch runs the listeners the abort of this context reaches, on the goroutine that cancels it, after the context and
// everything derived from it are aborted. Pi's order (measured on Node 24: `AbortSignal.any` marks every composite of the
// source aborted, runs the source's own listeners in the order they were added, then each composite's in the order the
// composites were created): by the creation of the context a listener was added to, and by when it was added within one
// context. A context's own cancel comes first in it, as it was created before everything derived from it. It runs only in the
// call that claimed the cancel; taking the listeners ends the abort the calls after it wait for (awaitAbort).
func (c *abortContext) dispatch() {
	c.mu.Lock()
	registered := c.listeners
	c.listeners = nil
	c.aborted = true
	if c.settled != nil {
		close(c.settled)
	}
	c.mu.Unlock()
	if len(registered) == 0 {
		return
	}
	ordered := make([]*abortListener, 0, len(registered))
	for l := range registered {
		ordered = append(ordered, l)
	}
	slices.SortFunc(ordered, func(a, b *abortListener) int {
		switch {
		case a.owner.created < b.owner.created:
			return -1
		case a.owner.created > b.owner.created:
			return 1
		case a.seq < b.seq:
			return -1
		case a.seq > b.seq:
			return 1
		}
		return 0
	})
	cause := context.Cause(c.Context)
	for _, l := range ordered {
		l.fire(cause)
	}
}

// awaitAbort is the cancel function of a call that does not own the cancel (a repeated one, or one that runs at once on
// another goroutine): it returns once the call that owns it has aborted the context and everything derived from it, so that a
// caller that cancels and goes on finds them aborted whichever call aborted them, as Pi's second `abort()` finds them. It
// does not wait for the listeners, which are the owner's to run, and which a call made from inside one of them (on the
// owner's goroutine) could never wait for: no code but the owner's own runs before the listeners are taken.
func (c *abortContext) awaitAbort() {
	c.mu.Lock()
	if c.aborted {
		c.mu.Unlock()
		return
	}
	if c.settled == nil {
		c.settled = make(chan struct{})
	}
	settled := c.settled
	c.mu.Unlock()
	<-settled
}

// abortSources appends to out the contexts of this package whose cancel function (or whose parent's, or signal's) can abort
// ctx, and reports whether those are all its sources: false when a context of another package (a deadline, a cancel
// context.WithCancel made) is one of them. A context with no Done channel has no source.
func abortSources(ctx context.Context, out []*abortContext) ([]*abortContext, bool) {
	for {
		switch c := ctx.(type) {
		case *valueContext:
			ctx = c.Context
		case *signalContext:
			// Its abort is its signal's; its parent has none.
			ctx = c.Context
		case *abortContext:
			if c.detached {
				return out, true
			}
			if c.own {
				// Its own cancel aborts it, and what a listener on it listens to besides.
				out = append(out, c)
			}
			if c.signaled != nil {
				// Cancelled by whichever of its sources aborts first.
				out, fromParent := abortSources(c.parent, out)
				out, fromSignal := abortSources(c.signaled.signal, out)
				return out, fromParent && fromSignal
			}
			ctx = c.parent
		default:
			return out, ctx.Done() == nil
		}
	}
}

// ownerOf is the context of this package whose abort signal ctx has: the one a listener added to ctx is added to in Pi (a value
// layer or a signal alias has its wrapped context's signal). A context of another package, which no cancel function of this
// package aborts, has none; nothing registers a listener on it.
func ownerOf(ctx context.Context) *abortContext {
	for {
		switch c := ctx.(type) {
		case *valueContext:
			ctx = c.Context
		case *signalContext:
			ctx = c.Context
		case *abortContext:
			if c.detached {
				return noOwner
			}
			return c
		default:
			return noOwner
		}
	}
}

// noOwner is the owner of a listener that no cancel function of this package reaches: it sorts first.
var noOwner = &abortContext{}

// OnAbort runs fn(cause) once, synchronously inside the cancel function that aborts ctx, on the goroutine that calls it,
// before that function returns: Pi's abort listener (`signal.addEventListener("abort", fn, { once: true })`), which
// `abort()` runs before it returns. The cause is the one the abort recorded (context.Cause). fn MUST NOT block and MUST
// NOT take a lock the canceller may hold or enter a section of a loop: it only records and enqueues, and it runs on
// whatever goroutine cancels, with or without a loop's section.
//
// When the cancel runs, every context of this package derived from the source that aborted is already aborted (Err, Done and
// Cause are set), then the listeners run in Pi's order: the aborted context's own, in the order they were added, then those of
// the contexts derived from it, in the order those were created, each context's in the order they were added. So a listener
// added to a parent runs before a listener added to its child even when the child's was added first. Like Pi's
// `awaitWithContext`, which checks `signal.aborted` before it adds its listener (context/index.ts:140), a ctx that is aborted
// already runs fn at once, before OnAbort returns, and so does a ctx whose abort has begun on another goroutine; a listener
// added by a listener, to a context aborted already, runs at once as well. A ctx derived from several sources
// (WithAbortSignal, or WithCancel under a parent that is cancelled) runs fn for the first of them to abort, with that one's
// cause.
//
// The guarantee is the canceller's: when the call of its cancel function that aborts ctx returns, the listeners have run. A
// call after it, repeated or at once on another goroutine, runs none and records no cause, as Pi's `abort()` of an aborted
// signal does nothing; it returns once the first has aborted the context and what is derived from it, and the first call's
// listeners may still be running then. A goroutine that sees Err or Done set while that cancel is still running is concurrent
// with it, as a second thread would be: the listener may not have run yet, so order a step against the return of the cancel
// that aborts, not against what Err shows.
//
// stop removes the registration and reports whether it kept fn from running; it is safe to call after fn ran, and from fn
// itself. A caller whose wait ends without fn having run (its result came first, its owner went away) MUST call it: a
// registration is held by every source it listens to, and a source may live much longer than the wait, until then. Once fn
// ran no source holds the registration and stop has nothing to do.
//
// exact is false when ctx can also be aborted by a context of another package (a deadline, a stdlib cancel): OnAbort cannot
// listen to it, Go has no hook inside a stdlib cancel, so a window remains for that source by construction, between its
// cancel and any callback. The rule for such a ctx is one, wherever it applies: register OnAbort (it still runs fn inside the
// cancel of every source of this package), and when exact is false register context.AfterFunc(ctx, ...) too, with fn
// idempotent (run once by whichever comes first), and decide with an ctx.Err() sample, not with state the listener
// maintains, where the order of the abort and another step matters.
func OnAbort(ctx context.Context, fn func(cause error)) (stop func() bool, exact bool) {
	l := &abortListener{fn: fn, seq: listenerSeq.Add(1), owner: ownerOf(ctx)}
	l.sources, exact = abortSources(ctx, nil)
	stop = func() bool {
		stopped := l.done.CompareAndSwap(false, true)
		l.unregister()
		return stopped
	}
	var aborted *abortContext
	for _, c := range l.sources {
		c.mu.Lock()
		// The check and the registration are one step under mu, as the cancel function takes the registrations under mu after
		// it cancelled: a cancel in between finds this listener, one before it is seen here.
		if c.Err() != nil {
			c.mu.Unlock()
			if aborted == nil || earlier(c, aborted) {
				aborted = c
			}
			continue
		}
		if c.listeners == nil {
			c.listeners = map[*abortListener]struct{}{}
		}
		c.listeners[l] = struct{}{}
		c.mu.Unlock()
	}
	if aborted != nil {
		l.fire(context.Cause(aborted.Context))
	}
	// A cancel of another source, or stop, may have settled the listener while this one registered on the later sources:
	// nothing would take its registrations out then.
	if l.done.Load() {
		l.unregister()
	}
	return stop, exact
}

// unregister removes the listener from every source that holds it; a set left empty is dropped, as a map keeps the buckets
// it grew to, and a source that outlives its waits would keep the size of its busiest moment.
func (l *abortListener) unregister() {
	for _, c := range l.sources {
		c.mu.Lock()
		delete(c.listeners, l)
		if len(c.listeners) == 0 {
			c.listeners = nil
		}
		c.mu.Unlock()
	}
}

// earlier reports whether the abort of a came before the abort of b (AbortOrder; a source with no place yet comes last).
func earlier(a, b *abortContext) bool {
	first, second := AbortOrder(a), AbortOrder(b)
	return first != 0 && (second == 0 || first < second)
}
