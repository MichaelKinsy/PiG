package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// The coding-agent classifiers are pi-ai's isContextOverflow and
// isRetryableAssistantError (packages/ai/src/utils/overflow.ts and retry.ts),
// so every upstream example classifies the same way here.
func TestClassifiersMatchPiAI(t *testing.T) {
	errorMessage := func(provider, text string) *agent.AssistantMessage {
		return &agent.AssistantMessage{Provider: provider, StopReason: "error", ErrorMessage: text}
	}
	overflow := []struct {
		message *agent.AssistantMessage
		want    bool
	}{
		{errorMessage("zai", `400 {"code":"1261","message":"Prompt too long"}`), true},
		{errorMessage("cerebras", "400 status code (no body)"), true},
		{errorMessage("opencode-go", "400 status code (no body)"), false},
		{errorMessage("opencode-go", "413 status code (no body)"), false},
	}
	for _, tc := range overflow {
		if got := IsContextOverflow(tc.message, 131072); got != tc.want {
			t.Errorf("IsContextOverflow(%q from %s) = %v, want %v", tc.message.ErrorMessage, tc.message.Provider, got, tc.want)
		}
	}
	retryable := []string{
		"The pending stream has been canceled (caused by: getaddrinfo ENOTFOUND bedrock-runtime.us-east-1.amazonaws.com)",
		"connect ENOTFOUND api.example.com",
		"EAI_AGAIN api.example.com",
		"The system is currently experiencing high demand and cannot process your request.",
		"520 status code (no body)",
		"Error: exceeded request buffer limit while retrying upstream",
		"An error occurred while processing your request. You can retry your request, or contact us through our help center.",
		`{"message":"The system encountered an unexpected error during processing. Try your request again."}`,
		"rate_limit_error: slow down",
	}
	for _, text := range retryable {
		if !IsRetryableError(errorMessage("openai", text), 131072) {
			t.Errorf("IsRetryableError(%q) = false, want true", text)
		}
	}
}
