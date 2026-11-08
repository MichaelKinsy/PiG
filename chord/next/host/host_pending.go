package host

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Go-only additions (no Pi file; D430): the two halves of Reference.Call (Start, Wait), and AwaitingCtx.
//
// The two halves of Reference.Call. Pi's `ref.method(args)` starts the call at once
// and returns a Promise; a caller may start several before awaiting any (a chunked upload's pipelined appends), and the
// calls are accepted in the order they were started. A blocking Go Call cannot say that, so Start is the synchronous half
// (the call is admitted and its handler's synchronous part has run when it returns) and Wait the awaiting half.

// PendingCall is a started call whose answer has not been awaited: Pi's Promise of a method call.
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
func AwaitingCtx(ctx Ctx, work func() error) *Handled {
	return Awaiting(ctx.(*callContext).host.loop(), work)
}
