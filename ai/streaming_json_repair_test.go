package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Copied from gate-close-02's TestAnthropicUpstreamSSEParsing repair case.
// Pi packages/ai/test/anthropic-sse-parsing.test.ts:329 retains both repaired arguments.
func TestAnthropicStreamRepairsMalformedToolJSON(t *testing.T) {
	event := func(kind, data string) string { return "event: " + kind + "\ndata: " + data + "\n\n" }
	events := event("message_start", `{"message":{"id":"msg_test","usage":{"input_tokens":12,"output_tokens":0}}}`) +
		event("content_block_start", `{"index":0,"content_block":{"type":"tool_use","id":"toolu_test","name":"edit","input":{}}}`) +
		event("content_block_delta", "{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"A\\H\\\",\\\"text\\\":\\\"col1\tcol2\\\"}\"}}") +
		event("content_block_stop", `{"index":0}`) +
		event("message_delta", `{"delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":12,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`) + event("message_stop", `{}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, events)
	}))
	defer server.Close()
	provider := NewAnthropicProvider(AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key", BaseURL: server.URL})
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	input := Context{Messages: []Message{UserMessage{Content: UserText("Use the edit tool.")}}, Tools: []ToolSchema{{Name: "edit", Description: "Edit a file.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}, "required": []string{"path", "text"}}}}}
	stream, err := provider.Stream(t.Context(), NormalizeContext(input), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonToolUse || result.ErrorMessage != "" || len(result.Content) != 1 {
		t.Fatalf("result=%+v", result)
	}
	call, ok := result.Content[0].(ToolCall)
	if !ok || !reflect.DeepEqual(call.Arguments, JsonObject{"path": "A\\H", "text": "col1\tcol2"}) {
		t.Fatalf("call=%#v", result.Content[0])
	}
}
