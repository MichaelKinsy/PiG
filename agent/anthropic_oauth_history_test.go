package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestAnthropicOAuthPoisonedHistoryRequest replays poisoned persisted history
// (errored, aborted, empty-text, orphaned tool-call, and cross-provider turns)
// through convertToLLM into an Anthropic OAuth-token request. Normalization
// drops the broken turns first; the Claude Code identity then maps every
// replayed tool_use and tool definition to Claude Code casing.
func TestAnthropicOAuthPoisonedHistoryRequest(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("PI_CACHE_RETENTION", "")
	target := &ai.Model{ID: "claude-haiku-4-5", ProviderMeta: ai.ProviderMetadata{ProviderID: "anthropic", API: ai.APIAnthropicMessages}}
	history := []AgentMessage{
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "start"}}}},
		{Assistant: &AssistantMessage{
			Role: "assistant", API: ai.APIOpenAIResponses, Provider: "openai", ModelID: "gpt-5", StopReason: ai.StopReasonToolUse,
			Content: []ai.AssistantContentBlock{
				ai.TextContent{Text: "   "},
				ai.ToolCall{ID: "call_a|fc_a", Name: "bash", Arguments: ai.JsonObject{"command": "ls"}},
			},
		}},
		{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "call_a|fc_a", ToolName: "bash", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}},
		{Assistant: &AssistantMessage{
			Role: "assistant", API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "claude-haiku-4-5", StopReason: ai.StopReasonError, ErrorMessage: "overloaded",
			Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "toolu_err", Name: "write", Arguments: ai.JsonObject{}}},
		}},
		{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "toolu_err", ToolName: "write", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "late"}}}},
		{Assistant: &AssistantMessage{
			Role: "assistant", API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "claude-haiku-4-5", StopReason: ai.StopReasonAborted,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial answer"}},
		}},
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "continue"}}}},
		{Assistant: &AssistantMessage{
			Role: "assistant", API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "claude-haiku-4-5", StopReason: ai.StopReasonToolUse,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}, ai.ToolCall{ID: "toolu_1", Name: "read", Arguments: ai.JsonObject{"path": "a"}}},
		}},
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "next"}}}},
	}
	tools := []ai.ToolSchema{
		{Name: "bash", Parameters: map[string]any{"type": "object"}},
		{Name: "read", Parameters: map[string]any{"type": "object"}},
		{Name: "write", Parameters: map[string]any{"type": "object"}},
	}
	for _, tc := range []struct {
		name     string
		apiKey   string
		toolUses []string
		tools    []string
		system   int
	}{
		{"oauth token", "sk-ant-oat01-test", []string{"Bash#call_a_fc_a", "Read#toolu_1"}, []string{"Bash", "Read", "Write"}, 2},
		{"api key", "sk-ant-api03-test", []string{"bash#call_a_fc_a", "read#toolu_1"}, []string{"bash", "read", "write"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()
			provider := ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: tc.apiKey, Model: "claude-haiku-4-5", BaseURL: server.URL})
			transcript := ai.NormalizeContext(ai.Context{SystemPrompt: "System prompt.", Messages: convertToLLM(history, target), Tools: tools})
			stream, err := provider.Stream(context.Background(), transcript, ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(context.Background()) {
			}

			var roles, toolUses, toolResults, texts []string
			for _, raw := range body["messages"].([]any) {
				message := raw.(map[string]any)
				roles = append(roles, message["role"].(string))
				blocks, ok := message["content"].([]any)
				if !ok {
					texts = append(texts, message["content"].(string))
					continue
				}
				for _, raw := range blocks {
					block := raw.(map[string]any)
					switch block["type"] {
					case "tool_use":
						toolUses = append(toolUses, block["name"].(string)+"#"+block["id"].(string))
					case "tool_result":
						toolResults = append(toolResults, block["tool_use_id"].(string))
					case "text":
						texts = append(texts, block["text"].(string))
					}
				}
			}
			if want := []string{"user", "assistant", "user", "user", "assistant", "user", "user"}; !reflect.DeepEqual(roles, want) {
				t.Errorf("roles = %q, want %q", roles, want)
			}
			if !reflect.DeepEqual(toolUses, tc.toolUses) {
				t.Errorf("tool_use = %q, want %q", toolUses, tc.toolUses)
			}
			if want := []string{"call_a_fc_a", "toolu_1"}; !reflect.DeepEqual(toolResults, want) {
				t.Errorf("tool_result ids = %q, want %q", toolResults, want)
			}
			for _, text := range texts {
				if strings.TrimSpace(text) == "" || strings.Contains(text, "partial answer") {
					t.Errorf("replayed text %q; empty and aborted text must not reach the wire", text)
				}
			}
			var toolNames []string
			for _, raw := range body["tools"].([]any) {
				toolNames = append(toolNames, raw.(map[string]any)["name"].(string))
			}
			if !reflect.DeepEqual(toolNames, tc.tools) {
				t.Errorf("tools = %q, want %q", toolNames, tc.tools)
			}
			if system, _ := body["system"].([]any); len(system) != tc.system {
				t.Errorf("system blocks = %d, want %d", len(system), tc.system)
			}
		})
	}
}
