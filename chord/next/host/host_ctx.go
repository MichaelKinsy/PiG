package host

import (
	"context"
	"errors"
	"fmt"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/telemetry"
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
	Lend(signal *parking.Signal, fn func(abandoned bool))
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
	// Trace runs fn in a span named name (the host's `chord.host` and attributes), a child of the handler's, with a child
	// ctx: the same principal, signal and references, its telemetry the span. A failure of fn marks the span failed
	// (`trace_failed`; a cancel keeps it ok) and is returned; with nothing recorded fn gets this ctx itself. A panic of fn
	// is its error.
	Trace(name string, attributes telemetry.SpanAttributes, fn func(ctx Ctx) (any, error)) (any, error)
}

// InSection runs work as one synchronous section of the host's loop, on the goroutine of a method handler that suspended:
// the awaits of Pi's handler resume inside the loop, so code that reads the host's state after a wait (a transaction, a
// blocking call) reads it here. ctx is the handler's ctx. Inline when the handler still runs in its admitting section.
//
// pig divergence (D430): the awaits of a handler's async remainder run from outside the host package.
func InSection(ctx context.Context, work func()) error {
	call, ok := ctx.(*callContext)
	if !ok {
		return errors.New("host.InSection: ctx is not a handler's ctx")
	}
	call.host.loop().Run(work)
	return nil
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
	// attempt is the attempt a method call runs on; nil for a root scope, a routine or a job run (no chain node).
	attempt oldWorkSource
}

var (
	_ Ctx      = (*callContext)(nil)
	_ callNode = (*callContext)(nil)
)

// newCallContext is the ctx of a handler run: a method call's is its own node of the call chain (on: the attempt it runs on,
// as the chain's `CallNode`), so the chain costs no allocation; a root scope, a routine or a job run has none (on is nil).
func newCallContext(ctx context.Context, from caller, host *chordHost, on oldWorkSource) *callContext {
	return &callContext{Context: ctx, principal: from.principal, incarnation: from.incarnation, host: host, attempt: on}
}

// Value answers the call's parking signal under parking.Key, itself under the call chain's key when it is a method call's,
// and delegates every other key.
func (c *callContext) Value(key any) any {
	if key == parking.Key && c.signal != nil {
		return c.signal
	}
	if c.attempt != nil && key == callChainKey.Token() {
		return callNode(c)
	}
	return c.Context.Value(key)
}

// on is the attempt its handler runs on (only read through the call chain, which a method call's ctx answers).
func (c *callContext) on() oldWorkSource { return c.attempt }

// up is the chain of the call that made this one.
func (c *callContext) up() callNode { return chainOf(c.Context) }

// chained is c's context with c's own call chain node: what a getter made through Get belongs to (old work during a
// reload), without the call's other values (its parking signal).
type chained struct {
	context.Context
	node *callContext
}

// Value answers the chain's key with the call's node; every other key is the call's context's.
func (c chained) Value(key any) any {
	if key == callChainKey.Token() && c.node.attempt != nil {
		return callNode(c.node)
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
		// Under this ctx's call chain: the getter belongs to the call (old work during a reload).
		scoped = withLinkedAbortSignal(c.settling, chained{Context: c.Context, node: c})
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

// linkedAbort is withLinkedAbortSignal's result: the child context and the release that detaches it.
type linkedAbort struct {
	context context.Context
	// release is the call's, run when it settles; it removes both listeners.
	release func()
	// sources are the two signals the child follows, the caller's first; zero when the child is the signal itself.
	sources [2]context.Context
}

// aborted is the reason the child aborted for, read from its sources at once: Pi's `AbortSignal.any` aborts its result in the
// same synchronous step as the input, where the child's own Done follows on a goroutine of its own (context.AfterFunc). Nil
// while neither source nor the child is aborted.
func (l linkedAbort) aborted() error {
	if l.context.Err() != nil {
		return context.Cause(l.context)
	}
	for _, source := range l.sources {
		if source != nil && source.Err() != nil {
			return linkedCause(l.sources)
		}
	}
	return nil
}

// withLinkedAbortSignal is a child of ctx aborted by signal and by the caller's signal (the caller's reason first) until
// release(), which the call runs when it settles. Without a caller's signal (ctx has no Done channel) the child is the
// signal itself with ctx's values, as in Pi, and release does nothing: the child listens to nothing, so a signal that
// outlives its calls (an attempt's, a reference's) keeps nothing of them. Pi avoids AbortSignal.any because it keeps
// cleanup entries per call for as long as its sources live; the Go form of the two listeners is two context.AfterFunc
// registrations, each removed by its stop function, never context.WithCancel's parent registration (which stays until the
// parent ends).
func withLinkedAbortSignal(signal context.Context, ctx context.Context) linkedAbort {
	if ctx.Done() == nil {
		return linkedAbort{context: chordctx.WithAbortSignal(ctx, signal), release: func() {}}
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
			return linkedAbort{context: linked, release: release}
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
	return linkedAbort{context: linked, release: release, sources: sources}
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
