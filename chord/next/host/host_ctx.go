package host

import (
	"context"
	"fmt"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
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
// and enters the loop (callHost.loop().Run) for each synchronous section Pi has between its awaits. The reference
// `Get` returns is the seam's `acquired.Reference()` as `any` (it is never inspected here).

// sectionLoop is what the call helpers ask of the host's loop beyond eventLoop: entering a synchronous section from the
// goroutine of a blocking call (an `await` resumption), and owned background tasks (an unawaited Promise). *Loop is one.
type sectionLoop interface {
	eventLoop
	// Run runs work as one synchronous section of the host (Loop.Run).
	Run(work func())
	// Go runs fn as an owned background task (Loop.Go).
	Go(fn func())
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
	Get(Type *contracts.Contract, params any, options GetOptions) (any, error)
}

// caller is who makes a call: the principal it acts as and its host's incarnation.
type caller struct {
	principal   data.Principal
	incarnation string
}

// acquired is a reference a host acquired for a call (Pi's `Binding`): released once, and the reference it stands for.
type acquired interface {
	Release()
	// Reference is the reference the binding was built for, as `any`: CallContext never inspects it.
	Reference() any
}

// callHost is what a call context asks of its host (DESIGN-go-layout.md section 5).
type callHost interface {
	// Incarnation is this host's incarnation.
	Incarnation() string
	// Acquire is Pi's `host.acquire`: it blocks until the reference is bound or fails, and is called off the loop (it
	// enters the loop itself). A rejection is the returned error.
	Acquire(Type *contracts.Contract, params any, caller caller, ctx context.Context, options GetOptions) (acquired, error)
	loop() sectionLoop
}

// callContext is the ctx of one handler run (or root scope): its references are released by settle when it settled.
type callContext struct {
	context.Context
	principal   data.Principal
	incarnation string
	host        callHost
	// settledOnce is set once the handler settled (Pi's `#references` being undefined).
	settledOnce bool
	// references are the references taken with Get; nil once the handler settled.
	references []acquired
	// settling is cancelled when the handler settles: a Get still waiting gives up and releases its reference. By the
	// first.
	settling       context.Context
	settlingCancel func(cause error)
}

var _ Ctx = (*callContext)(nil)

func newCallContext(ctx context.Context, from caller, host callHost) *callContext {
	return &callContext{Context: ctx, principal: from.principal, incarnation: from.incarnation, host: host}
}

// Principal implements Ctx.
func (c *callContext) Principal() data.Principal { return c.principal }

// Incarnation implements Ctx.
func (c *callContext) Incarnation() string { return c.incarnation }

// String is Pi's toString.
func (c *callContext) String() string { return fmt.Sprint(c.Context) + ".Ctx" }

// Get implements Ctx. The two sections around the acquire are Pi's synchronous parts of `get`.
func (c *callContext) Get(Type *contracts.Contract, params any, options GetOptions) (any, error) {
	loop := c.host.loop()
	var scoped linkedAbort
	var from caller
	var err error
	loop.Run(func() {
		// A settled scope refuses `released` whatever aborted it; an aborted call creates nothing.
		if c.settledOnce {
			err = settledRefusal(Type)
			return
		}
		if c.Context.Err() != nil {
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
	binding, acquireErr := c.host.Acquire(Type, params, from, scoped.context, options)
	var reference any
	loop.Run(func() {
		scoped.release()
		if acquireErr != nil {
			if c.settledOnce {
				err = settledRefusal(Type)
			} else {
				err = acquireErr
			}
			return
		}
		// A get that finished in the turn its handler settled: nobody releases its reference later.
		if c.settledOnce {
			binding.Release()
			err = settledRefusal(Type)
			return
		}
		c.references = append(c.references, binding)
		reference = binding.Reference()
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
		binding.Release()
	}
}

// settledRefusal is Pi's `settled(Type)`: what a ctx whose handler settled refuses a `get` with.
func settledRefusal(Type *contracts.Contract) error {
	return data.NewChordError(data.ChordErrorInit{
		Code:    data.CodeReleased,
		Reason:  data.ReasonSettled,
		Message: Type.Name + ": ctx.get after its call settled",
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
			abort(context.Cause(source))
		})
	}
	// A source aborted while the listeners were added runs its listener on a goroutine of its own, which waits here for
	// the stops.
	mu.Unlock()
	return linkedAbort{context: linked, release: release, abort: abort}
}
