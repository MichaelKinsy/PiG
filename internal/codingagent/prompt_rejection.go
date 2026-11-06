package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts flushCompactionQueue (the promptPromise.catch that calls restoreQueue).

import (
	"context"
	"errors"
)

// promptRejectionKey carries a caller's rejection handler on the context a prompt is submitted with.
type promptRejectionKey struct{}

// withPromptRejection returns ctx with a handler for a prompt that is rejected before its run starts: no model or credentials, an input handler or a
// before_agent_start handler that fails. Pi's prompt() throws then, and flushCompactionQueue catches it; without a handler the error is shown.
func withPromptRejection(ctx context.Context, reject func(error)) context.Context {
	return context.WithValue(ctx, promptRejectionKey{}, reject)
}

// rejectPrompt reports a rejected prompt to the handler its context carries, or shows the error. It runs on the owner loop.
func (m *InteractiveMode) rejectPrompt(ctx context.Context, err error) {
	message := turnErrorMessage(err)
	if reject, ok := ctx.Value(promptRejectionKey{}).(func(error)); ok {
		reject(errors.New(message))
		return
	}
	m.showError(message)
}
