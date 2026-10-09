// Package lineadmission reports the moment an ordered queue fixes an operation's position.
//
// A TypeScript caller that extends a Promise chain synchronously fixes its order before it awaits, so the next caller may start
// at once and still queue behind it. A blocking Go call fixes its order inside the callee. A caller that must keep its calls in
// order without waiting for each to finish attaches a callback with With; the first ordered queue that admits the call runs it.
package lineadmission

import (
	"context"
	"sync"
)

type key struct{}

type hook struct {
	once     sync.Once
	admitted func()
}

// With returns ctx carrying admitted. The callback runs at most once, when a queue that reports admission takes the call.
func With(ctx context.Context, admitted func()) context.Context {
	return context.WithValue(ctx, key{}, &hook{admitted: admitted})
}

// From returns the admission callback ctx carries, or nil. A queue runs it after the call holds its position and before it waits
// for the calls ahead of it. A context without a callback costs the queue no allocation.
func From(ctx context.Context) func() {
	if attached, ok := ctx.Value(key{}).(*hook); ok {
		return attached.notify
	}
	return nil
}

func (attached *hook) notify() { attached.once.Do(attached.admitted) }
