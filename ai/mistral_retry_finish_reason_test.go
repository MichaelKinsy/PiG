//go:build !pig_strip_mistral_conversations

package ai

import "testing"

// packages/ai/src/api/mistral-conversations.ts:933-934 (Pi 1.1.0, #10487): a Mistral response that ends with finish_reason "error" is retryable, because its message carries "server error"; any other unknown finish reason stays terminal.
func TestMistralFinishReasonErrorIsRetryable(t *testing.T) {
	reason, message := mapMistralStopReason("error")
	if reason != StopReasonError || !IsRetryableAssistantError(AssistantMessage{StopReason: reason, ErrorMessage: message}) {
		t.Fatalf("finish_reason error = %s %q, want a retryable error", reason, message)
	}
	reason, message = mapMistralStopReason("content_filter")
	if reason != StopReasonError || message != "Provider stopped with: content_filter" || IsRetryableAssistantError(AssistantMessage{StopReason: reason, ErrorMessage: message}) {
		t.Fatalf("an unknown finish reason = %s %q, want a terminal error", reason, message)
	}
}
