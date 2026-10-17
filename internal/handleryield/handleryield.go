// Package handleryield carries, through an extension event's dispatch context, the call a host makes when a handler
// stops running synchronously.
//
// Pi runs every extension on the process's one event loop. While a handler and the promise continuations it settles
// at once are running, nothing else in the process runs: a child process that exited, a provider response that
// arrived and a timer all wait until the handler awaits a timer or I/O and the loop turns. A host that runs a handler
// elsewhere reports that turn with Report, so its caller can let other work proceed at the same point.
package handleryield

import "context"

type key struct{}

// With returns ctx carrying yield. A host calls it, possibly more than once and from any goroutine, when a handler
// dispatched with the returned context is no longer running synchronously.
func With(ctx context.Context, yield func()) context.Context {
	return context.WithValue(ctx, key{}, yield)
}

// Report calls the yield ctx carries, if any.
func Report(ctx context.Context) {
	if yield, _ := ctx.Value(key{}).(func()); yield != nil {
		yield()
	}
}
