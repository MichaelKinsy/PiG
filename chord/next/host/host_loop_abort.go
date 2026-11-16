package host

import (
	"context"
	"sync/atomic"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Go-only helper (no Pi file): the abort listener of a waiter. Pi's `signal.addEventListener("abort", ...)` runs the listener
// synchronously inside `abort()` (context/index.ts:138-153), on the one thread there is. A Go context's context.AfterFunc starts
// its callback on a goroutine of its own when the context is cancelled: later than the cancel, in no order against what the
// cancelling code does next, and a goroutine the loop's join does not count, so a test's `delivered()` could return between the
// cancel and the work the abort causes. onAbortNow and onAbort are the listeners a waiter needs: they run inside the cancel
// function (chordctx.OnAbort), and onAbort hands its work to the loop there.

// onAbortNow runs fn once, inside the cancel function that aborts ctx, on the goroutine that cancels (chordctx.OnAbort): Pi's
// listener of an abort. fn MUST NOT block, enter a section of a loop or take a lock the canceller may hold: it cancels another
// context, records, or queues (onAbort). A ctx that is aborted already runs fn at once. stop removes the listener and reports
// whether it kept fn from running (safe after fn ran, and from fn itself); a caller that no longer waits MUST call it, a
// registration is held by every source of ctx until then.
func onAbortNow(ctx context.Context, fn func()) (stop func() bool) {
	var ran atomic.Bool
	once := func() {
		if ran.CompareAndSwap(false, true) {
			fn()
		}
	}
	stopListening, exact := chordctx.OnAbort(ctx, func(error) { once() })
	var stopAfter func() bool
	if !exact {
		// A source chordctx.OnAbort cannot listen to (a deadline, a context.WithCancel of the standard library) aborts ctx too: its
		// listener is the runtime's, on a goroutine of its own.
		stopAfter = context.AfterFunc(ctx, once)
	}
	return func() bool {
		stopped := stopListening()
		if stopAfter != nil {
			stopped = stopAfter() && stopped
		}
		return stopped
	}
}

// onAbort queues fn on loop once ctx is aborted, at the cancel: a microtask after the section that cancelled, as Loop.Queue puts
// it, and from outside a section a counted hop of its own (Loop.joined), so an abort is ordered with the settlements the same
// section queues and a test's `delivered()` waits for fn. fn runs as a section of the loop. stop is onAbortNow's.
func onAbort(loop eventLoop, ctx context.Context, fn func()) (stop func() bool) {
	return onAbortNow(ctx, func() { loop.Queue(fn) })
}
