package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestProviderRetryMessagesUpstream(t *testing.T) {
	for _, tc := range []struct{ name, message, reply string }{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3317-network-connection-lost-retry.test.ts:14
		{`retries transient "Network connection lost." failures`, "Network connection lost.", "recovered after reconnect"},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6019-explicit-provider-retry-message.test.ts:11 (openai row)
		{"retries openai explicit retry guidance", "An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_******** in your message.", "recovered"},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6019-explicit-provider-retry-message.test.ts:11 (bedrock row)
		{"retries bedrock explicit retry guidance", `{"message":"The system encountered an unexpected error during processing. Try your request again."}`, "recovered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}}`}, fauxError(tc.message), fauxReply(tc.reply, ai.StopReasonStop, 0))
			if _, err := h.session.Send(t.Context(), "test"); err != nil {
				t.Fatal(err)
			}
			events := h.settle(t)
			if got := h.provider.callCount(); got != 2 {
				t.Fatalf("provider calls = %d, want 2", got)
			}
			var starts []string
			var ends []bool
			for _, event := range events {
				switch event := event.(type) {
				case agent.AutoRetryStartEvent:
					starts = append(starts, event.ErrorMessage)
				case agent.AutoRetryEndEvent:
					ends = append(ends, event.Success)
				}
			}
			if !slices.Equal(starts, []string{tc.message}) {
				t.Errorf("auto_retry_start errors = %q", starts)
			}
			if !slices.Equal(ends, []bool{true}) {
				t.Errorf("auto_retry_end successes = %v", ends)
			}
			if !projectionContains(h, tc.reply) {
				t.Errorf("assistant response lacks %q", tc.reply)
			}
		})
	}
}
