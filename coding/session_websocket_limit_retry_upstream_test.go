package coding

import (
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

const codexConnectionLimitError = "Codex error: Responses websocket connection limit reached (60 minutes). Create a new websocket connection to continue."

// Regression for Pi #12124 (bb1a2b3542b5, #12229): the Codex WebSocket lifetime limit can hit mid-stream after partial thinking, where the provider cannot
// reconnect. The turn is retried without the failed partial response and without running a completed tool again.
// upstream: packages/coding-agent/test/suite/regressions/12124-websocket-connection-limit-retry.test.ts "retries the turn without the failed partial response or re-running completed tools"
func TestIssue12124WebSocketConnectionLifetimeRetry(t *testing.T) {
	echo := &retryEchoTool{}
	var retryMessages []ai.Message
	toolCall := func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(),
			Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "completed_call", Name: "echo", Arguments: ai.JsonObject{"text": "completed once"}}}}
	}
	failed := func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonError, ErrorMessage: codexConnectionLimitError, Timestamp: time.Now().UnixMilli(),
			Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "unfinished thinking"}}}
	}
	recovered := func(messages []ai.Message) *ai.AssistantMessage {
		retryMessages = slices.Clone(messages)
		return fauxReply("recovered", ai.StopReasonStop, 0)(messages)
	}
	h := newRetryEventsHarness(t, `{"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":0}}`, extension.Extension{}, []agent.AgentTool{echo}, toolCall, failed, recovered)
	log := recordSessionEvents(h.session)
	retryPrompt(t, h, "test")

	if got := h.provider.callCount(); got != 3 {
		t.Fatalf("provider calls = %d, want 3", got)
	}
	if got := echo.ran(); !slices.Equal(got, []string{"completed once"}) {
		t.Fatalf("tool runs = %v, want one", got)
	}
	for _, message := range retryMessages {
		if assistant, ok := message.(*ai.AssistantMessage); ok && assistant.StopReason == ai.StopReasonError {
			t.Fatalf("the failed message was sent on the retry: %#v", assistant)
		}
		if assistant, ok := message.(ai.AssistantMessage); ok && assistant.StopReason == ai.StopReasonError {
			t.Fatalf("the failed message was sent on the retry: %#v", assistant)
		}
	}
	if len(retryMessages) == 0 {
		t.Fatal("the retry call saw no messages")
	}
	if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || starts[0].Attempt != 1 || starts[0].ErrorMessage != codexConnectionLimitError {
		t.Fatalf("auto_retry_start = %#v", starts)
	}
	if ends := eventsOf[agent.AutoRetryEndEvent](log); len(ends) != 1 || !ends[0].Success || ends[0].Attempt != 1 {
		t.Fatalf("auto_retry_end = %#v", ends)
	}
	if text := h.session.LastAssistantText(); text == nil || *text != "recovered" {
		t.Fatalf("last assistant text = %v", text)
	}
}
