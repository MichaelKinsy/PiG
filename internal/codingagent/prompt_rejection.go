package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts flushCompactionQueue (the promptPromise.catch that calls restoreQueue).

import (
	"context"
	"errors"
	"sync/atomic"
)

// promptRejectionKey carries a caller's rejection on the context a prompt is submitted with.
type promptRejectionKey struct{}

// promptRejection is a caller's handler for a rejected prompt, and whether one was rejected. The run goroutine marks the rejection before it settles the
// run, which is before the handler runs on the owner loop: a message the caller delivers in between sees an idle agent, and must not start a run of its
// own that is rejected for the same cause.
type promptRejection struct {
	reject   func(error)
	rejected atomic.Bool
}

// withPromptRejection returns ctx with a handler for a prompt that is rejected before its run starts: no model or credentials, an input handler or a
// before_agent_start handler that fails. Pi's prompt() throws then, and flushCompactionQueue catches it; without a handler the error is shown.
func withPromptRejection(ctx context.Context, reject func(error)) context.Context {
	return context.WithValue(ctx, promptRejectionKey{}, &promptRejection{reject: reject})
}

// markPromptRejected records, from the goroutine that found the rejection, that the prompt ctx was submitted with is rejected. The handler runs later.
func markPromptRejected(ctx context.Context) {
	if rejection, ok := ctx.Value(promptRejectionKey{}).(*promptRejection); ok {
		rejection.rejected.Store(true)
	}
}

// promptRejected reports whether a prompt submitted with ctx was rejected, so a message submitted with the same ctx belongs to the queue its handler
// restores and is not sent.
func promptRejected(ctx context.Context) bool {
	rejection, ok := ctx.Value(promptRejectionKey{}).(*promptRejection)
	return ok && rejection.rejected.Load()
}

// rejectPrompt reports a rejected prompt to the handler its context carries, or shows the error. It runs on the owner loop.
func (m *InteractiveMode) rejectPrompt(ctx context.Context, err error) {
	message := turnErrorMessage(err)
	if rejection, ok := ctx.Value(promptRejectionKey{}).(*promptRejection); ok {
		rejection.rejected.Store(true)
		rejection.reject(errors.New(message))
		return
	}
	m.showError(message)
}
