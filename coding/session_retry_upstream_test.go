package coding

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// retryFailures scripts failCount overloaded_error responses, then one "Success" response.
func retryFailures(failCount int) []scriptedResponse {
	responses := make([]scriptedResponse, 0, failCount+1)
	for range failCount {
		responses = append(responses, fauxError("overloaded_error"))
	}
	return append(responses, fauxReply("Success", ai.StopReasonStop, 0))
}

func retrySettings(maxRetries, maxAgentDelayMs int) string {
	return fmt.Sprintf(`{"retry":{"enabled":true,"maxRetries":%d,"baseDelayMs":1,"maxAgentDelayMs":%d}}`, maxRetries, maxAgentDelayMs)
}

// upstream: packages/coding-agent/test/agent-session-retry.test.ts
func TestUpstreamAgentSessionRetry(t *testing.T) {
	// :138
	t.Run("retries after a transient error and succeeds", func(t *testing.T) {
		h := newRetryEventsHarness(t, retrySettings(3, 60000), extension.Extension{}, nil, retryFailures(1)...)
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "Test")
		if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
			t.Errorf("retry events=%v", got)
		}
		if h.provider.callCount() != 2 || sessionIsRetrying(h.session) {
			t.Errorf("calls=%d retrying=%v", h.provider.callCount(), sessionIsRetrying(h.session))
		}
	})
	// :153
	t.Run("exhausts max retries and emits failure", func(t *testing.T) {
		h := newRetryEventsHarness(t, retrySettings(2, 60000), extension.Extension{}, nil, retryFailures(99)...)
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "Test")
		got := retrySummary(log)
		for _, want := range []string{"start:1", "start:2", "end:false"} {
			if !slices.Contains(got, want) {
				t.Errorf("retry events %v lack %s", got, want)
			}
		}
		if h.provider.callCount() != 3 || sessionIsRetrying(h.session) {
			t.Errorf("calls=%d retrying=%v", h.provider.callCount(), sessionIsRetrying(h.session))
		}
	})
	// :170 Regression for #8826.
	t.Run("caps agent retry delay", func(t *testing.T) {
		h := newRetryEventsHarness(t, retrySettings(5, 5), extension.Extension{}, nil, retryFailures(4)...)
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "Test")
		var delays []int
		for _, start := range eventsOf[agent.AutoRetryStartEvent](log) {
			delays = append(delays, start.DelayMs)
		}
		if !slices.Equal(delays, []int{1, 2, 4, 5}) {
			t.Errorf("delays=%v", delays)
		}
	})
	// :183 The 40ms delay is the upstream stimulus that yields the assistant message_end handler, not a wait for completion.
	t.Run("prompt waits for retry completion even when assistant message_end handling is delayed", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"message_end": {func(args ...any) (any, error) {
			if message, ok := args[0].(extension.MessageEndEvent).Message.(agent.AgentMessage); ok && message.Assistant != nil {
				time.Sleep(40 * time.Millisecond)
			}
			return nil, nil
		}}}}
		h := newRetryEventsHarness(t, retrySettings(3, 60000), ext, nil, retryFailures(1)...)
		retryPrompt(t, h, "Test")
		if h.provider.callCount() != 2 || sessionIsRetrying(h.session) {
			t.Errorf("calls=%d retrying=%v", h.provider.callCount(), sessionIsRetrying(h.session))
		}
	})
	// :192
	t.Run("retries provider network_error failures", func(t *testing.T) {
		h := newRetryEventsHarness(t, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}}`, extension.Extension{}, nil,
			fauxError("Provider finish_reason: network_error"), fauxReply("Recovered after retry", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "Test")
		if h.provider.callCount() != 2 {
			t.Errorf("calls=%d", h.provider.callCount())
		}
		if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
			t.Errorf("retry events=%v", got)
		}
	})
	// :250 Regression: session.prompt() must wait for the whole tool loop after an auto-retry whose response includes tool_use.
	t.Run("prompt waits for full agent loop when retry produces tool calls", func(t *testing.T) {
		echo := &retryEchoTool{}
		h := newRetryEventsHarness(t, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}}`, extension.Extension{}, []agent.AgentTool{echo},
			fauxError("overloaded_error"),
			func([]ai.Message) *ai.AssistantMessage {
				return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(), Content: []ai.AssistantContentBlock{
					ai.TextContent{Text: "Looking that up now."}, ai.ToolCall{ID: "call_1", Name: "echo", Arguments: ai.JsonObject{"text": "hello"}},
				}}
			},
			fauxReply("Final answer.", ai.StopReasonStop, 0), fauxReply("Follow-up answer.", ai.StopReasonStop, 0))
		retryPrompt(t, h, "Test")
		if h.provider.callCount() != 3 {
			t.Errorf("all three LLM calls must complete: calls=%d", h.provider.callCount())
		}
		if !slices.Equal(echo.ran(), []string{"hello"}) {
			t.Errorf("tool runs=%v", echo.ran())
		}
		if h.session.IsStreaming() {
			t.Error("agent is streaming after prompt returned")
		}
		retryPrompt(t, h, "Follow-up")
		if h.provider.callCount() != 4 {
			t.Errorf("follow-up calls=%d", h.provider.callCount())
		}
	})
}
