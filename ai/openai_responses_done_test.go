package ai

import "testing"

func TestOpenAIResponsesDoneBeforeTerminalEventFails(t *testing.T) {
	result, _ := collectResponsesEvents(t, "data: [DONE]\n\n")
	assertSSEErrorContains(t, result, "OpenAI Responses stream ended before a terminal response event")
	if !IsRetryableAssistantError(*result) {
		t.Fatalf("error should be retryable: %#v", result)
	}
}
