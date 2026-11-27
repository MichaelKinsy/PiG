package invocation

import (
	"context"
	"sync"
	"sync/atomic"
)

type ackKey struct{}

type acknowledgment struct {
	once           sync.Once
	fn             func()
	suspensionOnly bool
	acked          atomic.Bool
	suspendedAcked atomic.Bool
}

// WithAcknowledgment binds a one-shot handler-invocation acknowledgment to ctx. The handler's first suspension or its completion releases it.
func WithAcknowledgment(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, ackKey{}, &acknowledgment{fn: fn})
}

// WithSuspensionAcknowledgment binds a one-shot acknowledgment that only a suspension releases. A transport's completion report does not release it: the binder acknowledges a completed handler itself once the handler's result is published, so an awaiting continuation of a handler that never suspended runs before the caller admits later input.
func WithSuspensionAcknowledgment(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, ackKey{}, &acknowledgment{fn: fn, suspensionOnly: true})
}

// Bound reports whether ctx carries an acknowledgment: a request without one has nothing to hold back.
func Bound(ctx context.Context) bool {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	return ack != nil
}

// Acknowledged reports whether ctx's bound acknowledgment has already been released.
func Acknowledged(ctx context.Context) bool {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	return ack != nil && ack.acked.Load()
}

// AcknowledgedBySuspension reports whether ctx's bound acknowledgment was released by a handler suspension (AcknowledgeSuspension) rather than plain handler completion.
func AcknowledgedBySuspension(ctx context.Context) bool {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	return ack != nil && ack.suspendedAcked.Load()
}

// AcknowledgeSuspension reports that a handler suspended on an external wait (such as I/O or a timer), releasing the invocation acknowledgment and recording that the emission crossed an async suspension boundary.
func AcknowledgeSuspension(ctx context.Context) {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	if ack == nil {
		return
	}
	ack.release(true)
}

// Acknowledge reports that a handler has entered the runtime boundary. Calls
// after the first are no-ops.
func Acknowledge(ctx context.Context) {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	if ack == nil {
		return
	}
	ack.release(false)
}

// Complete reports that a transport observed the handler finish. It releases an acknowledgment bound by WithAcknowledgment and leaves one bound by WithSuspensionAcknowledgment to its binder.
func Complete(ctx context.Context) {
	ack, _ := ctx.Value(ackKey{}).(*acknowledgment)
	if ack == nil || ack.suspensionOnly {
		return
	}
	ack.release(false)
}

func (ack *acknowledgment) release(bySuspension bool) {
	ack.once.Do(func() {
		if bySuspension {
			ack.suspendedAcked.Store(true)
		}
		ack.acked.Store(true)
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

// Resumption is a RunPhase that follows its handler past a call the host answers.
type Resumption interface {
	// AfterResumption takes fn and runs it once the handler, resumed by the answer of a call it awaited, next awaits or its request's result is published.
	AfterResumption(fn func())
}

// AfterResumption hands fn to ctx's RunPhase when it follows resumptions, and reports whether it did.
func AfterResumption(ctx context.Context, fn func()) bool {
	phase, _ := ctx.Value(runPhaseKey{}).(Resumption)
	if phase == nil {
		return false
	}
	phase.AfterResumption(fn)
	return true
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
