package ai

import "testing"

func TestOpenAICompletionsSparseWireIndexesKeepArrivalOrder(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-sparse","choices":[{"delta":{"tool_calls":[{"index":2,"id":"call-a","type":"function","function":{"name":"second","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-sparse","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-b","type":"function","function":{"name":"first","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
	}
	tools := []ToolSchema{
		{Name: "first", Description: "First", Parameters: JsonObject{"type": "object"}},
		{Name: "second", Description: "Second", Parameters: JsonObject{"type": "object"}},
	}
	_, response, _ := captureToolChoiceRequest(
		t,
		toolChoiceModel(t, "openai", "gpt-4o-mini", true),
		Context{Messages: []Message{UserMessage{Content: UserText("Use both tools.")}}, Tools: tools},
		StreamOptions{},
		chunks,
	)

	if response.StopReason != StopReasonToolUse {
		t.Fatalf("stop reason = %q, want %q", response.StopReason, StopReasonToolUse)
	}
	assertCompletionsJSON(t, response.Content, `[{"type":"toolCall","id":"call-a","name":"second","arguments":{}},{"type":"toolCall","id":"call-b","name":"first","arguments":{}}]`)
}
