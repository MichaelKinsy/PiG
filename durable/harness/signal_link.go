// Synchronous signal links. Chord's withAbortSignal(signal, context) propagates an abort to the derived context in the
// same turn; context.AfterFunc runs its callback on another goroutine, so a job queued on the Session line behind the
// commit that cancels the signal can run first and see the derived context still live.

package harness

import (
	"context"
	"sync"
)

type signalLinkKey struct{}

// signalLinks holds the cancel functions of the contexts derived from one signal, so cancelling the signal cancels them
// before it returns.
type signalLinks struct {
	// signal is the context newLinkedSignal returned. A context derived from it inherits the registry through its value
	// but has its own cancellation, so only signal itself uses the synchronous path.
	signal   context.Context
	mu       sync.Mutex
	children map[*context.CancelCauseFunc]struct{}
}

// newLinkedSignal returns a cancellable context whose derived contexts (withSignal, linkSignal) are cancelled
// synchronously by the returned cancel function.
func newLinkedSignal(parent context.Context) (context.Context, context.CancelCauseFunc) {
	links := &signalLinks{children: map[*context.CancelCauseFunc]struct{}{}}
	ctx, cancel := context.WithCancelCause(context.WithValue(parent, signalLinkKey{}, links))
	links.signal = ctx
	return ctx, func(cause error) {
		// Cancel before taking the snapshot: a link registered after the snapshot then sees the signal cancelled when it
		// re-checks, so no link is left live.
		cancel(cause)
		links.mu.Lock()
		children := make([]context.CancelCauseFunc, 0, len(links.children))
		for child := range links.children {
			children = append(children, *child)
		}
		links.mu.Unlock()
		for _, child := range children {
			child(context.Cause(ctx))
		}
	}
}

// linkedChild derives a context from ctx that signal also cancels. A signal made by newLinkedSignal cancels it
// synchronously; any other signal does so on another goroutine. The returned function releases the link.
func linkedChild(ctx, signal context.Context) (context.Context, func()) {
	child, cancel := context.WithCancelCause(ctx)
	if cause := signal.Err(); cause != nil {
		cancel(context.Cause(signal))
		return child, func() { cancel(context.Canceled) }
	}
	if links, ok := signal.Value(signalLinkKey{}).(*signalLinks); ok && links.signal == signal {
		handle := &cancel
		links.mu.Lock()
		links.children[handle] = struct{}{}
		links.mu.Unlock()
		// Close the window between the check above and the registration.
		if signal.Err() != nil {
			cancel(context.Cause(signal))
		}
		return child, func() {
			links.mu.Lock()
			delete(links.children, handle)
			links.mu.Unlock()
			cancel(context.Canceled)
		}
	}
	stop := context.AfterFunc(signal, func() { cancel(context.Cause(signal)) })
	return child, func() {
		stop()
		cancel(context.Canceled)
	}
}
