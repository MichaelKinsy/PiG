package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// captureStreamWire drives a provider's real Stream path against an httptest
// server that records the serialized request body, then returns that body. The
// body is captured when the HTTP request reaches the handler, so it is available
// regardless of how the (intentionally minimal) SSE response is parsed.
func captureStreamWire(t *testing.T, sse string, makeProvider func(baseURL string) ai.Provider, msgs []ai.Message) []byte {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()

	stream, err := makeProvider(srv.URL).Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: msgs}), ai.StreamOptions{})
	if err == nil {
		_ = stream.Result()
	}
	if len(body) == 0 {
		t.Fatal("provider did not send a request body")
	}
	return body
}

func poisonedAgentHistory() []AgentMessage {
	// The exact abort race D48 documents: the final assistant turn errored while
	// holding the only tool_use ("call_x") for a tool result that was already
	// persisted. After normalization the errored turn is dropped and its result
	// is orphaned; D48 strips the orphan so the provider request stays valid.
	return []AgentMessage{
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "run it"}}}},
		{Assistant: &AssistantMessage{
			Role:         "assistant",
			Content:      []ai.AssistantContentBlock{ai.ToolCall{ID: "call_x", Name: "bash", Arguments: ai.JsonObject{"command": "expr 20 + 22"}}},
			StopReason:   "error",
			ErrorMessage: "connection reset",
		}},
		{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "call_x", ToolName: "bash", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "42\n"}}}},
	}
}

func cleanToolCycleHistory() []AgentMessage {
	// A well-formed tool cycle: the assistant turn survives (not errored), so its
	// tool_use ("call_y") and matching tool_result must both reach the wire: the
	// D48 strip must be inert here.
	return []AgentMessage{
		{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "run it"}}}},
		{Assistant: &AssistantMessage{
			Role:       "assistant",
			Content:    []ai.AssistantContentBlock{ai.ToolCall{ID: "call_y", Name: "bash", Arguments: ai.JsonObject{"command": "expr 20 + 22"}}},
			StopReason: "toolUse",
		}},
		{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "call_y", ToolName: "bash", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "42\n"}}}},
	}
}

const (
	openAICompletionsSSE = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	openAIResponsesSSE   = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"
	anthropicSSE         = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
)

func providerBuilders() []struct {
	name string
	sse  string
	make func(baseURL string) ai.Provider
} {
	return []struct {
		name string
		sse  string
		make func(baseURL string) ai.Provider
	}{
		{"openai-completions", openAICompletionsSSE, func(u string) ai.Provider {
			return ai.NewOpenAIProvider(ai.OpenAIConfig{Model: "gpt-4o-mini", BaseURL: u, APIKey: "k", ProviderID: "openai"})
		}},
		{"openai-responses", openAIResponsesSSE, func(u string) ai.Provider {
			return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{Model: "gpt-5-codex", BaseURL: u, APIKey: "k", ProviderID: "openai"})
		}},
		{"anthropic-messages", anthropicSSE, func(u string) ai.Provider {
			return ai.NewAnthropicProvider(ai.AnthropicConfig{Model: "claude-sonnet-4-20250514", BaseURL: u, APIKey: "k", ProviderID: "anthropic"})
		}},
	}
}

// TestPoisonedHistoryProducesValidProviderRequest closes the provider-conversion
// half of the D48 risk-core surface (foundation criterion 4). Prior tests stopped
// at NormalizeMessages; this drives poisoned history through the production
// convertToLLM seam and then each provider's real Stream path, asserting the
// serialized request never references the orphaned tool id. Without the D48 strip
// the orphaned tool_result would reach the wire and the API would reject the whole
// request ("No tool call found for ... call_id", "tool_use_id not found").
func TestPoisonedHistoryProducesValidProviderRequest(t *testing.T) {
	msgs := convertToLLM(poisonedAgentHistory(), nil)
	for _, p := range providerBuilders() {
		t.Run(p.name, func(t *testing.T) {
			wire := captureStreamWire(t, p.sse, p.make, msgs)
			if bytes.Contains(wire, []byte("call_x")) {
				t.Fatalf("orphaned tool id call_x reached the %s wire; D48 strip did not fire: %s", p.name, wire)
			}
			if bytes.Contains(wire, []byte("tool_result")) || bytes.Contains(wire, []byte("function_call_output")) || bytes.Contains(wire, []byte("tool_call_id")) {
				t.Fatalf("%s wire still carries a tool-result structure for a dropped call: %s", p.name, wire)
			}
		})
	}
}

// TestCleanToolCycleSurvivesProviderRequest proves the D48 strip is inert on a
// well-formed session: a surviving tool cycle keeps both the tool call and its
// matching result across all three provider conversions.
func TestCleanToolCycleSurvivesProviderRequest(t *testing.T) {
	msgs := convertToLLM(cleanToolCycleHistory(), nil)
	for _, p := range providerBuilders() {
		t.Run(p.name, func(t *testing.T) {
			wire := captureStreamWire(t, p.sse, p.make, msgs)
			if n := bytes.Count(wire, []byte("call_y")); n < 2 {
				t.Fatalf("%s wire references call_y %d times, want >=2 (call side + result side); tool cycle was over-stripped: %s", p.name, n, wire)
			}
		})
	}
}
