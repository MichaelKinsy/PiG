package invocation

import (
	"context"
	"sync"
)

type ackKey struct{}

type acknowledgment struct {
	once sync.Once
	fn   func()
}

// WithAcknowledgment binds a one-shot handler-invocation acknowledgment to ctx.
func WithAcknowledgment(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, ackKey{}, &acknowledgment{fn: fn})
}

// Acknowledge reports that a handler has entered the runtime boundary. Calls
// after the first are no-ops.
func Acknowledge(ctx context.Context) {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	if ack == nil {
		return
	}
	ack.once.Do(func() {
		if ack.fn != nil {
			ack.fn()
		}
	})
}
