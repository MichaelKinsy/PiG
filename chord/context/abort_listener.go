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
	// seq orders the listeners of one cancel as they were added, as an EventTarget runs its listeners.
	seq uint64
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

// dispatch runs the listeners of this context's own cancel, in the order they were added. It runs in the cancel function
// of WithCancel, after the context is cancelled, on the goroutine that cancels.
func (c *abortContext) dispatch() {
	c.mu.Lock()
	registered := c.listeners
	c.listeners = nil
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
			if c.signal != nil {
				// Cancelled by whichever of its two sources aborts first.
				out, fromParent := abortSources(c.parent, out)
				out, fromSignal := abortSources(c.signal, out)
				return out, fromParent && fromSignal
			}
			out = append(out, c)
			ctx = c.parent
		default:
			return out, ctx.Done() == nil
		}
	}
}

// OnAbort runs fn(cause) once, synchronously inside the cancel function that aborts ctx, on the goroutine that calls it,
// before that function returns: Pi's abort listener (`signal.addEventListener("abort", fn, { once: true })`), which
// `abort()` runs before it returns. The cause is the one the abort recorded (context.Cause). fn MUST NOT block and MUST
// NOT take a lock the canceller may hold or enter a section of a loop: it only records and enqueues, and it runs on
// whatever goroutine cancels, with or without a loop's section.
//
// Like Pi's `awaitWithContext`, which checks `signal.aborted` before it adds its listener (context/index.ts:140), a ctx
// that is aborted already runs fn at once, before OnAbort returns. A ctx derived from several sources (WithAbortSignal, or
// WithCancel under a parent that is cancelled) runs fn for the first of them to abort, with that one's cause.
//
// stop removes the registration and reports whether it kept fn from running; it is safe to call after fn ran, and from fn
// itself. A caller whose wait ends without fn having run (its result came first, its owner went away) MUST call it: a
// registration is held by every source it listens to, and a source may live much longer than the wait, until then. Once fn
// ran no source holds the registration and stop has nothing to do. exact is false when ctx can also be aborted by a context
// of another package (a deadline, a stdlib cancel), which OnAbort cannot listen to: the caller needs context.AfterFunc for
// that source, which runs on a goroutine of its own.
func OnAbort(ctx context.Context, fn func(cause error)) (stop func() bool, exact bool) {
	l := &abortListener{fn: fn, seq: listenerSeq.Add(1)}
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
