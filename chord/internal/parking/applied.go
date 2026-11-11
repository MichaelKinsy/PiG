package parking

import (
	"context"
	"sync"
)

// Applied is the hand-off of a route's `resolve` (Chord v2 host, route.ts `#resolve`). Pi's route awaits the app's `resolve`
// and applies its result in the continuation of that `await`, which runs from the microtask queue before any reaction
// registered on the same promise after the route's: a workers component's next queued operation (a Stop and the Retry
// after it) starts from the reaction that runs once the start's bookkeeping is done, so it finds the result applied.
//
// A Go `resolve` is a blocking call that returns on a goroutine of its own, and the route applies its result in a section
// of the host's loop afterwards. Nothing orders that section before whatever the settling of the app's own work lets go
// on, so an app whose next operation reads what the route applies (a Retry meeting a `resolving` address) says so: it
// registers what it releases with Then, and the route runs it, in the section that applied the result, after it did.
//
// The Applied of a route's resolve travels on the ctx of the app's `resolve` under a key of its own (WithApplied,
// AppliedOf), as the call's Signal does: an app that has nothing to order ignores it.
type Applied struct {
	mu      sync.Mutex
	applied bool
	then    []func()
}

// NewApplied returns an Applied whose result was not applied yet.
func NewApplied() *Applied { return &Applied{} }

type appliedKey struct{}

// WithApplied returns a context that answers AppliedOf with applied.
func WithApplied(ctx context.Context, applied *Applied) context.Context {
	return context.WithValue(ctx, appliedKey{}, applied)
}

// AppliedOf is the Applied of the route's resolve ctx belongs to; nil for a ctx no route made for a resolve.
func AppliedOf(ctx context.Context) *Applied {
	applied, _ := ctx.Value(appliedKey{}).(*Applied)
	return applied
}

// Then runs fn once the route applied the result, in the section that applied it and after it did, in the order the calls
// were made; at once when it did already. fn must not block: it runs inside a section of the host's loop.
func (a *Applied) Then(fn func()) {
	a.mu.Lock()
	if a.applied {
		a.mu.Unlock()
		fn()
		return
	}
	a.then = append(a.then, fn)
	a.mu.Unlock()
}

// Apply is the route's: the result was applied. It runs what Then registered, once; a second Apply does nothing.
func (a *Applied) Apply() {
	a.mu.Lock()
	if a.applied {
		a.mu.Unlock()
		return
	}
	a.applied = true
	then := a.then
	a.then = nil
	a.mu.Unlock()
	for _, fn := range then {
		fn()
	}
}
