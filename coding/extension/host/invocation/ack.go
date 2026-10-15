package invocation

import (
	"context"
	"sync"
)

type ackKey struct{}

type acknowledgment struct {
	once           sync.Once
	fn             func()
	suspensionOnly bool
}

// WithAcknowledgment binds a one-shot handler-invocation acknowledgment to ctx. The handler's first suspension or its completion releases it.
func WithAcknowledgment(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, ackKey{}, &acknowledgment{fn: fn})
}

// WithSuspensionAcknowledgment binds a one-shot acknowledgment that only a suspension releases. A transport's completion report does not release it: the binder acknowledges a completed handler itself once the handler's result is published, so an awaiting continuation of a handler that never suspended runs before the caller admits later input.
func WithSuspensionAcknowledgment(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, ackKey{}, &acknowledgment{fn: fn, suspensionOnly: true})
}

// Acknowledge reports that a handler has entered the runtime boundary. Calls
// after the first are no-ops.
func Acknowledge(ctx context.Context) {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	if ack == nil {
		return
	}
	ack.release()
}

// Complete reports that a transport observed the handler finish. It releases an acknowledgment bound by WithAcknowledgment and leaves one bound by WithSuspensionAcknowledgment to its binder.
func Complete(ctx context.Context) {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	if ack == nil || ack.suspensionOnly {
		return
	}
	ack.release()
}

func (ack *acknowledgment) release() {
	ack.once.Do(func() {
		if ack.fn != nil {
			ack.fn()
		}
	})
}

type runPhaseKey struct{}

// RunPhase follows whether a request's handler runs its synchronous part or is suspended, as the request's transport sees it.
// RPC mode holds the start of a run that a command sends until the command next suspends or answers (rpc-mode.ts, cli
// rpcAdmission.extensionRunStage); the transport reports each change to the phase bound to the command's context.
type RunPhase interface {
	// Resumed reports that the handler runs again: it made a call that Pi runs inside its synchronous part.
	Resumed()
	// Suspended reports that the handler awaits.
	Suspended()
}

// WithRunPhase binds phase to ctx; the transport serving a request with ctx reports to it.
func WithRunPhase(ctx context.Context, phase RunPhase) context.Context {
	return context.WithValue(ctx, runPhaseKey{}, phase)
}

// Resumed reports to ctx's RunPhase, if any, that its handler runs again.
func Resumed(ctx context.Context) {
	if phase, _ := ctx.Value(runPhaseKey{}).(RunPhase); phase != nil {
		phase.Resumed()
	}
}

// Suspended reports to ctx's RunPhase, if any, that its handler awaits.
func Suspended(ctx context.Context) {
	if phase, _ := ctx.Value(runPhaseKey{}).(RunPhase); phase != nil {
		phase.Suspended()
	}
}
