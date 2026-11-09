package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Go-only additions (no Pi file; D430): the two halves of Reference.Call (Start, Wait), AwaitingCtx and Resume.
//
// The two halves of Reference.Call. Pi's `ref.method(args)` starts the call at once
// and returns a Promise; a caller may start several before awaiting any (a chunked upload's pipelined appends), and the
// calls are accepted in the order they were started. A blocking Go Call cannot say that, so Start is the synchronous half
// (the call is admitted and its handler's synchronous part has run when it returns) and Wait the awaiting half.

// PendingCall is a started call whose answer has not been awaited: Pi's Promise of a method call.
//
// pig divergence (D430): Go callers start a call and await it apart; Pi's Promise needs no second operation.
type PendingCall struct {
	result *parkedCallResult
}

// Start starts the method `method` with args as Call does, without waiting for its answer: when Start returns, the call
// is admitted (or refused: the error), in the order Start calls were made. Wait awaits the answer.
func (r *Reference) Start(method string, args delta.JsonValue, ctx context.Context) (*PendingCall, error) {
	b := r.binding
	if _, found := b.methods[method]; !found {
		return nil, fmt.Errorf("%s: %s is not a method", b.name, method)
	}
	var result *parkedCallResult
	var err error
	b.loop.Run(func() { result, err = b.call(method, args, ctx) })
	if err != nil {
		return nil, err
	}
	return &PendingCall{result: result}, nil
}

// Wait is the call's answer: its value, or the error it was rejected with. It blocks until the call settles or ctx ends.
func (p *PendingCall) Wait(ctx context.Context) (delta.JsonValue, error) { return p.result.wait(ctx) }

// AwaitingCtx is Awaiting for a caller outside the package, which cannot name the host's loop: a handler's blocking work
// (the awaits of Pi's async `onLive` or `on` handler) run on an owned task of the loop that ctx's handler runs on, its error
// (nil: resolved) settling the returned Handled in a section. ctx is the handler's own Ctx; it runs inside a section.
//
// pig divergence (D430): the awaits of a handler's async remainder run from outside the host package.
func AwaitingCtx(ctx Ctx, work func() error) *Handled {
	return Awaiting(ctx.(*callContext).host.loop(), work)
}

// Resume runs work as one synchronous section of the host's loop, on the goroutine of a method handler that suspended: the
// awaits of Pi's handler resume inside the loop, so code that reads the host's state after a wait (a transaction, a
// blocking call) reads it there. ctx is the handler's own ctx. When the handler still runs in its admitting section, work
// runs inline. A ctx that is not a handler's returns an error and runs nothing.
//
// pig divergence (D430): the awaits of a handler's async remainder run from outside the host package.
func Resume(ctx context.Context, work func()) error {
	call, ok := ctx.(*callContext)
	if !ok {
		return errors.New("host.Resume: ctx is not a handler's ctx")
	}
	call.host.loop().Run(work)
	return nil
}
