package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream treats a bodyless 400 as an overflow only for Cerebras, so an
// OpenRouter "400 Provider returned error" is a transient provider error that
// the Session retries (pi-ai isContextOverflow and isRetryableAssistantError).
func TestSessionRetriesProviderReturnedError400(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":1}}`},
		fauxError("400 Provider returned error"),
		fauxReply("recovered", ai.StopReasonStop, 0),
	)
	if _, err := h.session.Send(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	h.settle(t)
	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2 (one retry)", got)
	}
	if text := h.session.LastAssistantText(); text == nil || *text != "recovered" {
		t.Fatalf("last assistant text = %v, want the retried response", text)
	}
}

// Upstream retries DNS lookup failures and explicit retry guidance.
func TestSessionRetriesUpstreamRetryableErrors(t *testing.T) {
	for _, message := range []string{
		"connect ENOTFOUND api.example.com",
		"The system is currently experiencing high demand and cannot process your request.",
		"An error occurred while processing your request. You can retry your request.",
	} {
		t.Run(message, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":1}}`},
				fauxError(message),
				fauxReply("recovered", ai.StopReasonStop, 0),
			)
			if _, err := h.session.Send(context.Background(), "start"); err != nil {
				t.Fatal(err)
			}
			h.settle(t)
			if got := h.provider.callCount(); got != 2 {
				t.Fatalf("model calls = %d, want 2 (one retry)", got)
			}
		})
	}
}
