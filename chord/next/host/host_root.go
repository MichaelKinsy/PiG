package host

import (
	"context"
	"errors"
	"fmt"
	"slices"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/host/root.ts. Root scopes (slice 8 §4, D3, design §8): `host.root(fn, context,
// options?)`, a call context for code outside objects (a Durable tool, a CLI), sealed when `fn` settles, when `context`
// aborts and when the host closes: its signal aborted, its `ctx.get` refused `released`, every reference it took
// released.
//
// Forced Go differences. `run` is an awaited Promise, so it blocks and returns (value, error); the value is `any` (the
// typed `T` of Pi is the host's typed layer). `fn` runs on a task of its own (Loop.Go): Pi's `running` promise outlives
// the caller's wait when the caller is rejected first, and the scope is sealed when it settles whenever that is. The
// `Promise.race` of `running` and the close promise, wrapped by `awaitWithContext`, is a select over the result, the
// close channel and the caller's context.

// RootOptions is what `host.root` takes besides `fn` and the context.
type RootOptions struct {
	// Principal is whom the scope acts as: the host's principal by default (nil); a trusted same-host caller's choice
	// (base code).
	Principal data.Principal
}

// openScope is an open root scope: its seal, and the rejection its caller gets when the host closes.
type openScope struct {
	// seal seals the scope with a reason, once.
	seal func(reason error)
	// closed is closed once the host closed with closeReason (Pi's `closed` promise rejecting with it).
	closed      chan struct{}
	closeReason error
}

// rootResult is how `fn` ended.
type rootResult struct {
	value any
	err   error
}

// rootScopes are the open root scopes of a host, each sealed once; close seals the rest and rejects their callers.
type rootScopes struct {
	host callHost
	// open is each open scope in the order it opened.
	open []*openScope
}

func newRootScopes(host callHost) *rootScopes { return &rootScopes{host: host} }

// settledReason is the abort reason of a scope whose `fn` settled.
var settledReason = data.NewChordError(data.ChordErrorInit{
	Code:    data.CodeReleased,
	Reason:  data.ReasonSettled,
	Message: "host.root: its fn settled",
}, nil)

// run runs fn with a call context like a handler's (principal, this host's incarnation, ctx's telemetry, a signal linked
// to ctx's), sealed when fn settles, ctx aborts or the host closes. It returns as fn does; it returns at once when ctx
// aborts or the host closes (an uncooperative fn runs on and fails at its next use of Chord), or when ctx was aborted
// already (fn does not run). It blocks, off the host's loop.
func (r *rootScopes) run(fn func(ctx Ctx) (any, error), principal data.Principal, ctx context.Context) (any, error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	host := r.host
	loop := host.loop()
	var open *openScope
	var results chan rootResult
	loop.Run(func() {
		controller, abort := chordctx.WithCancel(chordctx.Background())
		linked := withLinkedAbortSignal(controller, ctx)
		scope := linked.context
		call := newCallContext(scope, caller{principal: principal, incarnation: host.Incarnation()}, host)
		open = &openScope{closed: make(chan struct{})}
		var stopAborted func() bool
		self := open
		open.seal = func(reason error) {
			index := slices.Index(r.open, self)
			if index < 0 {
				return
			}
			r.open = slices.Delete(r.open, index, index+1)
			stopAborted()
			// References first: a `get` still waiting gives up with `released`, not with the abort's reason.
			call.settle()
			// While still linked, so the scope's own signal (a caller's context has one) aborts too.
			abort(reason)
			linked.abort(reason)
			linked.release()
		}
		r.open = append(r.open, open)
		stopAborted = context.AfterFunc(scope, func() {
			loop.Run(func() { self.seal(context.Cause(scope)) })
		})
		results = make(chan rootResult, 1)
		// Plugin code: a panic becomes the rejection. The scope is sealed before the caller is told, as Pi's `running.then`
		// runs before the race's.
		loop.Go(func() {
			value, err := tryPlugin(func() (any, error) { return fn(call) })
			loop.Run(func() { self.seal(settledReason) })
			results <- rootResult{value: value, err: err}
		})
	})
	// A method's caller gets `released` when its object ends, whatever the handler still does: so does this one.
	select {
	case result := <-results:
		return result.value, result.err
	case <-open.closed:
		return nil, open.closeReason
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// close: the host closes: every open scope is sealed with reason, its caller rejected with it. It runs inside a section.
func (r *rootScopes) close(reason error) {
	for _, open := range slices.Clone(r.open) {
		open.seal(reason)
		open.closeReason = reason
		close(open.closed)
	}
}

// tryPlugin runs plugin code, returning what a `throw` would: its error, or a panic recovered as one.
func tryPlugin(work func() (any, error)) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value, err = nil, recoveredError(recovered)
		}
	}()
	return work()
}

// recoveredError is a recovered panic value as the error a JavaScript `throw` of it would be.
func recoveredError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return errors.New(fmt.Sprint(recovered))
}
