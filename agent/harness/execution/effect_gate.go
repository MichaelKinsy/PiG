// Package execution provides effect admission for the durable agent harness.
package execution

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// AbortRequested is expected control flow when cancellation wins effect admission.
// Cancellation waits for the owner's shared cancellation operation and returns its error. The owner supplies a repeatable wait function, not the operation itself. Waiting does not cancel the operation or depend on the invoking context.
type AbortRequested struct {
	Cancellation func() error
}

func (*AbortRequested) Error() string { return "Abort requested" }

// Gate is the procedure-facing admission capability for one drive pass.
// Obtain it and its separate owner controls through CreateGate.
type Gate struct {
	state *gateState
}

// GateControl owns the abort and close transitions for one drive pass.
type GateControl struct {
	state *gateState
}

type gateState struct {
	mu           sync.Mutex
	signal       context.Context
	cancel       context.CancelCauseFunc
	cancellation func() error
	closed       bool
	closeError   error
}

// CreateGate creates separate procedure-facing and owner-facing views of an open gate. Its signal has no parent; the caller composes it with invocation context.
func CreateGate() (*Gate, *GateControl) {
	// A harness cancellable context lets contexts combined with the signal
	// (harness.WithAbortSignal) observe the abort synchronously, like the
	// upstream AbortController signal.
	signal, cancel := harness.WithCancel(context.Background())
	state := &gateState{signal: signal, cancel: cancel}
	return &Gate{state: state}, &GateControl{state: state}
}

// Signal is canceled only by SignalAbort or Close, not by BeginAbort.
func (gate *Gate) Signal() context.Context { return gate.state.signal }

// Admit checks admission synchronously and invokes the effect on the calling goroutine. Refusal does not invoke it. An admitted callback owns its work and error; lifecycle controls do not wait for it and can be called from it. The locked check is the admission point when owners and procedures run concurrently.
func (gate *Gate) Admit(invoke func() error) error {
	state := gate.state
	state.mu.Lock()
	var refusal error
	if state.closed {
		refusal = state.closeError
	} else if state.cancellation != nil {
		refusal = &AbortRequested{Cancellation: state.cancellation}
	}
	state.mu.Unlock()
	if refusal != nil {
		return refusal
	}
	return invoke()
}

// BeginAbort refuses new effects without signaling admitted work. Only the first abort while open takes effect. cancellation waits for the owner's shared operation; it must be non-nil and safe to call repeatedly and concurrently.
func (control *GateControl) BeginAbort(cancellation func() error) {
	state := control.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.cancellation != nil {
		return
	}
	state.cancellation = cancellation
}

// SignalAbort signals admitted work after the owner commits cancellation. It does nothing while open or closed and does not itself wait for the cancellation.
func (control *GateControl) SignalAbort() {
	state := control.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.closed && state.cancellation != nil && state.signal.Err() == nil {
		state.cancel(&AbortRequested{Cancellation: state.cancellation})
	}
}

// Close permanently refuses effects with err and signals admitted work unless an earlier signal already won. The first close error wins; err must be non-nil.
func (control *GateControl) Close(err error) {
	state := control.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	state.closed = true
	state.closeError = err
	state.cancel(err)
}
