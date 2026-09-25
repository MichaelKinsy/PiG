package codingagent

// Auto-recovery classifiers for agent messages. They apply pi-ai's
// isContextOverflow, isRecoverableLength, and isRetryableAssistantError
// (ai/overflow.go, ai/assistant_retry.go), as upstream agent-session.ts does.

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// IsContextOverflow reports whether an assistant message represents a context
// overflow. It is pi-ai's isContextOverflow (ai.IsContextOverflow).
func IsContextOverflow(msg *agent.AssistantMessage, contextWindow int) bool {
	return msg != nil && ai.IsContextOverflow(msg.LLMMessage(), contextWindow)
}

// IsRecoverableLength reports whether a provider stopped for length before
// reaching the model's original output limit. The limit must be the model
// value before request-time context clamping. It is pi-ai's
// isRecoverableLength (ai.IsRecoverableLength).
func IsRecoverableLength(msg *agent.AssistantMessage, desiredMaxOutput int) bool {
	return msg != nil && ai.IsRecoverableLength(msg.LLMMessage(), desiredMaxOutput)
}

// IsRetryableError reports whether an assistant message is a transient
// provider or transport error worth retrying. A context overflow is not
// retryable: compaction handles it. Mirrors upstream
// AgentSession._isRetryableError, which applies pi-ai's isContextOverflow and
// isRetryableAssistantError.
func IsRetryableError(msg *agent.AssistantMessage, contextWindow int) bool {
	if msg == nil {
		return false
	}
	message := msg.LLMMessage()
	return !ai.IsContextOverflow(message, contextWindow) && ai.IsRetryableAssistantError(message)
}
