package host

import (
	"context"
	"fmt"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/host/ctx.ts. The `ctx` a method handler gets (§8): its call's context, who called, the
// calling host's incarnation, and call-scoped references; and the abort signal of a call's context, linked to long-lived
// sources until it settles.
//
// Forced Go differences. `Ctx<P>`'s principal type parameter is type-level (owner class a); Principal is the JSON
// object of `data.Principal`, nil for Pi's `undefined`. An AbortSignal is a context.Context (`signal.reason` is
// context.Cause); a listener on a signal is a context.AfterFunc whose stop function removes it. `ctx.get` is an awaited
// Promise, so Get blocks and returns (reference, error): it is called from a goroutine off the host's loop (a handler's)
// and enters the loop (host.loop().Run) for each synchronous section Pi has between its awaits.

// sectionLoop is what the call helpers ask of the host's loop beyond eventLoop: entering a synchronous section from the
// goroutine of a blocking call (an `await` resumption), and owned background tasks (an unawaited Promise). *Loop is one.
type sectionLoop interface {
	eventLoop
	// Run runs work as one synchronous section of the host (Loop.Run).
	Run(work func())
	// Go runs fn as an owned background task (Loop.Go).
	Go(fn func())
	// Lend runs fn as the synchronous part of an async function inside the running section (Loop.Lend).
	Lend(signal *parking.Signal, fn func())
}

// Ctx is a handler's context.
type Ctx interface {
	context.Context
	// Principal is the caller as its link stamped it; on a local object's handler the host's principal (nil, Pi's
	// `undefined`, on a client).
	Principal() data.Principal
	// Incarnation is the calling host's incarnation over a link, this host's otherwise (R20).
	Incarnation() string
	// Get takes a reference as the caller (Principal) sees it, released when the handler settles; afterwards Get returns
	// `released`. It blocks until the reference is acquired.
	Get(Type *contracts.Contract, params any, options GetOptions) (*Reference, error)
}

// caller is who makes a call: the principal it acts as and its host's incarnation.
type caller struct {
	principal   data.Principal
	incarnation string
}

// callContext is the ctx of one handler run (or root scope): its references are released by settle when it settled.
type callContext struct {
	context.Context
	principal   data.Principal
	incarnation string
	host        *chordHost
	// settledOnce is set once the handler settled (Pi's `#references` being undefined).
	settledOnce bool
	// references are the references taken with Get; nil once the handler settled.
	references []*binding
	// settling is cancelled when the handler settles: a Get still waiting gives up and releases its reference. By the
	// first.
	settling       context.Context
	settlingCancel func(cause error)
	// signal is the "returned or parked" of the call this is the ctx of (nil for a scope that runs no handler). The ctx
	// answers Value(parking.Key) with it.
	signal *parking.Signal
}

var _ Ctx = (*callContext)(nil)

func newCallContext(ctx context.Context, from caller, host *chordHost) *callContext {
	return &callContext{Context: ctx, principal: from.principal, incarnation: from.incarnation, host: host}
}

// Value answers the call's parking signal under parking.Key and delegates every other key.
func (c *callContext) Value(key any) any {
	if key == parking.Key && c.signal != nil {
		return c.signal
	}
	return c.Context.Value(key)
}

// Principal implements Ctx.
func (c *callContext) Principal() data.Principal { return c.principal }

// Incarnation implements Ctx.
func (c *callContext) Incarnation() string { return c.incarnation }

// String is Pi's toString.
func (c *callContext) String() string { return fmt.Sprint(c.Context) + ".Ctx" }

// Get implements Ctx. The two sections around the acquire are Pi's synchronous parts of `get`.
func (c *callContext) Get(typ *contracts.Contract, params any, options GetOptions) (*Reference, error) {
	loop := c.host.loop()
	var scoped linkedAbort
	var from caller
	var err error
	loop.Run(func() {
		// A settled scope refuses `released` whatever aborted it; an aborted call creates nothing.
		if c.settledOnce {
			err = settledRefusal(typ)
			return
		}
		if c.Err() != nil {
			err = context.Cause(c.Context)
			return
		}
		// Made for the caller: its principal and its origin's incarnation travel with the reference's holds.
		from = caller{principal: c.principal, incarnation: c.incarnation}
		if c.settling == nil {
			c.settling, c.settlingCancel = chordctx.WithCancel(chordctx.Background())
		}
		scoped = withLinkedAbortSignal(c.settling, c.Context)
	})
	if err != nil {
		return nil, err
	}
	binding, acquireErr := c.host.acquire(typ, params, from, scoped.context, c, options)
	var reference *Reference
	loop.Run(func() {
		scoped.release()
		if acquireErr != nil {
			if c.settledOnce {
				err = settledRefusal(typ)
			} else {
				err = acquireErr
			}
			return
		}
		// A get that finished in the turn its handler settled: nobody releases its reference later.
		if c.settledOnce {
			binding.release()
			err = settledRefusal(typ)
			return
		}
		c.references = append(c.references, binding)
		reference = binding.reference
	})
	if err != nil {
		return nil, err
	}
	return reference, nil
}

// settle: the handler settled: its references are released, waiting gets give up, and Get returns `released` from
// now on. It runs inside a section.
func (c *callContext) settle() {
	references := c.references
	c.references = nil
	c.settledOnce = true
	if c.settlingCancel != nil {
		c.settlingCancel(nil)
	}
	for _, binding := range references {
		binding.release()
	}
}

// settledRefusal is Pi's `settled(Type)`: what a ctx whose handler settled refuses a `get` with.
func settledRefusal(typ *contracts.Contract) error {
	return data.NewChordError(data.ChordErrorInit{
		Code:    data.CodeReleased,
		Reason:  data.ReasonSettled,
		Message: typ.Name + ": ctx.get after its call settled",
	}, nil)
}

// linkedAbort is withLinkedAbortSignal's result: the child context, the release that detaches it, and abort, which
// aborts the child at once (the signal the child follows is aborted by its owner, and `AbortSignal.any` aborts the child
// in the same turn; a context.AfterFunc listener runs on a goroutine of its own, so the owner aborts the child itself as
// well).
type linkedAbort struct {
	context context.Context
	// release is the call's, run when it settles; it removes both listeners.
	release func()
	abort   func(cause error)
}

// withLinkedAbortSignal is a child of ctx aborted by signal and by the caller's signal (the caller's reason first) until
// release(), which the call runs when it settles. Without a caller's signal (ctx has no Done channel) the child is
// aborted by signal itself and release does nothing. Pi avoids AbortSignal.any because it keeps cleanup entries per call
// for as long as its sources live; the Go form of the two listeners is two context.AfterFunc registrations, each removed
// by its stop function, never context.WithCancel's parent registration (which stays until the parent ends).
func withLinkedAbortSignal(signal context.Context, ctx context.Context) linkedAbort {
	if ctx.Done() == nil {
		child, abort := chordctx.WithCancel(ctx)
		if signal.Err() != nil {
			abort(context.Cause(signal))
		} else {
			stop := context.AfterFunc(signal, func() { abort(context.Cause(signal)) })
			context.AfterFunc(child, func() { stop() })
		}
		return linkedAbort{context: child, release: func() {}, abort: abort}
	}
	linked, abort := chordctx.WithCancel(chordctx.WithoutAbortSignal(ctx))
	// The sources in Pi's order: the caller's first, so its reason wins a tie.
	sources := [2]context.Context{ctx, signal}
	var mu sync.Mutex
	var stops [2]func() bool
	release := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, stop := range stops {
			if stop != nil {
				stop()
			}
		}
	}
	// Sources aborted before the link: the first of them in the order given (AbortSignal.any's constructor takes the first
	// aborted input of its array, whenever each aborted).
	for _, source := range sources {
		if source.Err() != nil {
			abort(context.Cause(source))
			return linkedAbort{context: linked, release: release, abort: abort}
		}
	}
	mu.Lock()
	for i, source := range sources {
		stops[i] = context.AfterFunc(source, func() {
			release()
			abort(linkedCause(sources))
		})
	}
	// A source aborted while the listeners were added runs its listener on a goroutine of its own, which waits here for
	// the stops.
	mu.Unlock()
	return linkedAbort{context: linked, release: release, abort: abort}
}

// linkedCause is the reason of the source that aborted first (`AbortSignal.any`): the listeners of the sources run on
// goroutines of their own, in any order, so the earliest abort is told by chordctx.AbortOrder; sources it cannot order (a
// context of another package) rank in the order given, the caller's first.
func linkedCause(sources [2]context.Context) error {
	var first context.Context
	var firstOrder uint64
	for _, source := range sources {
		if source.Err() == nil {
			continue
		}
		order := chordctx.AbortOrder(source)
		if first == nil || order != 0 && (firstOrder == 0 || order < firstOrder) {
			first, firstOrder = source, order
		}
	}
	return context.Cause(first)
}
