package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// nativeToolChangeServer records each request body and answers every request
// with one streamed text reply.
func nativeToolChangeServer(t *testing.T, reply string) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)
	return server, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(bodies)
	}
}

func nativeToolChangeItems(value any) []map[string]any {
	list, _ := value.([]any)
	items := make([]map[string]any, len(list))
	for i, item := range list {
		items[i], _ = item.(map[string]any)
	}
	return items
}

func nativeToolChangeNames(value any, path ...string) []string {
	var names []string
	for _, item := range nativeToolChangeItems(value) {
		for _, key := range path {
			item, _ = item[key].(map[string]any)
		}
		name, _ := item["name"].(string)
		names = append(names, name)
	}
	return names
}

// TestAgentToolLoadoutChangesUseNativeProviderToolChanges drives the agent loop
// through each provider path with native mid-conversation tool changes: the
// agent declares the loadout change as a system message delta, and the second
// request carries it in the provider's native form instead of a folded prompt.
func TestAgentToolLoadoutChangesUseNativeProviderToolChanges(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "short")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	for _, tc := range []struct {
		name     string
		reply    string
		newTools []string
		provider func(baseURL string) (ai.Provider, ai.API)
		check    func(t *testing.T, body map[string]any)
	}{
		{
			name:     "anthropic tool_removal and tool_addition",
			reply:    "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			newTools: []string{"second"},
			provider: func(baseURL string) (ai.Provider, ai.API) {
				return ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "sk-ant-api03-test", Model: "custom-claude", BaseURL: baseURL, Compat: &ai.AnthropicMessagesCompat{
					SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolChanges: new(true),
				}}), ai.APIAnthropicMessages
			},
			check: func(t *testing.T, body map[string]any) {
				if got := nativeToolChangeNames(body["tools"]); !slices.Equal(got, []string{"first", "__pi_deferred_placeholder__", "second"}) {
					t.Errorf("tools = %v", got)
				}
				messages := nativeToolChangeItems(body["messages"])
				update := messages[len(messages)-1]
				blocks := nativeToolChangeItems(update["content"])
				if update["role"] != "system" || len(blocks) != 2 || blocks[0]["type"] != "tool_removal" || blocks[1]["type"] != "tool_addition" {
					t.Errorf("update = %#v", update)
				}
			},
		},
		{
			name:     "openai responses additional_tools",
			reply:    "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
			newTools: []string{"first", "second"},
			provider: func(baseURL string) (ai.Provider, ai.API) {
				return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", Model: "custom-model", BaseURL: baseURL, IsReasoning: true, Compat: &ai.OpenAIResponsesCompat{
					SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true),
				}}), ai.APIOpenAIResponses
			},
			check: func(t *testing.T, body map[string]any) {
				if got := nativeToolChangeNames(body["tools"]); !slices.Equal(got, []string{"first"}) {
					t.Errorf("tools = %v", got)
				}
				var additions []string
				for _, item := range nativeToolChangeItems(body["input"]) {
					if item["type"] == "additional_tools" {
						additions = append(additions, nativeToolChangeNames(item["tools"])...)
					}
				}
				if !slices.Equal(additions, []string{"second"}) {
					t.Errorf("additional_tools = %v", additions)
				}
			},
		},
		{
			name:     "kimi tool-bearing system message",
			reply:    "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
			newTools: []string{"first", "second"},
			provider: func(baseURL string) (ai.Provider, ai.API) {
				return ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", Model: "custom-kimi", ProviderID: "moonshotai", BaseURL: baseURL, Compat: &ai.OpenAICompat{
					SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolAdditions: new(true),
				}}), ai.APIOpenAICompletions
			},
			check: func(t *testing.T, body map[string]any) {
				if got := nativeToolChangeNames(body["tools"], "function"); !slices.Equal(got, []string{"first"}) {
					t.Errorf("tools = %v", got)
				}
				var loaded []string
				for _, message := range nativeToolChangeItems(body["messages"]) {
					if tools, ok := message["tools"]; ok {
						if _, hasContent := message["content"]; hasContent || message["role"] != "system" {
							t.Errorf("tool-bearing message = %#v", message)
						}
						loaded = append(loaded, nativeToolChangeNames(tools, "function")...)
					}
				}
				if !slices.Equal(loaded, []string{"second"}) {
					t.Errorf("loaded tools = %v", loaded)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, bodies := nativeToolChangeServer(t, tc.reply)
			provider, api := tc.provider(server.URL)
			model := &ai.Model{ID: "native-tools", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: api}, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
			a := NewAgent(AgentOptions{Model: model, SystemPrompt: "base", Tools: []AgentTool{echoScriptTool("first")}})
			mustSend(t, a, "one")
			var tools []AgentTool
			for _, name := range tc.newTools {
				tools = append(tools, echoScriptTool(name))
			}
			a.SetTools(tools)
			mustSend(t, a, "two")
			got := bodies()
			if len(got) != 2 {
				t.Fatalf("requests = %d", len(got))
			}
			tc.check(t, got[1])
		})
	}
}

// nativeToolChangePoisonedHistory is persisted history with a tool addition
// between a cross-provider tool call and its result, followed by errored,
// aborted, empty-text, and orphaned tool-call turns.
func nativeToolChangePoisonedHistory() []AgentMessage {
	return []AgentMessage{
		{System: &ai.SystemMessage{Content: ai.SystemText("System prompt."), ToolsAdded: []ai.ToolSchema{{Name: "bash", Parameters: map[string]any{"type": "object"}}}}},
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "start"}}}},
		{Assistant: &AssistantMessage{
			Role: "assistant", API: ai.APIOpenAIResponses, Provider: "openai", ModelID: "gpt-5", StopReason: ai.StopReasonToolUse,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "   "}, ai.ToolCall{ID: "call_a|fc_a", Name: "bash", Arguments: ai.JsonObject{"command": "ls"}}},
		}},
		{System: &ai.SystemMessage{Content: ai.SystemText(""), ToolsAdded: []ai.ToolSchema{{Name: "read", Parameters: map[string]any{"type": "object"}}}, Timestamp: 1}},
		{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "call_a|fc_a", ToolName: "bash", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}},
		{Assistant: &AssistantMessage{
			Role: "assistant", API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "custom", StopReason: ai.StopReasonError, ErrorMessage: "overloaded",
			Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "toolu_err", Name: "bash", Arguments: ai.JsonObject{}}},
		}},
		{Assistant: &AssistantMessage{
			Role: "assistant", API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "custom", StopReason: ai.StopReasonAborted,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial answer"}},
		}},
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "continue"}}}},
		{Assistant: &AssistantMessage{
			Role: "assistant", API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "custom", StopReason: ai.StopReasonToolUse,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}, ai.ToolCall{ID: "toolu_1", Name: "read", Arguments: ai.JsonObject{"path": "a"}}},
		}},
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "next"}}}},
	}
}

func TestNativeToolChangesPoisonedPersistedHistoryRequests(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "short")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	for _, tc := range []struct {
		name     string
		api      ai.API
		reply    string
		provider func(baseURL string) ai.Provider
		shape    func(body map[string]any) []string
		want     []string
	}{
		{
			name: "anthropic", api: ai.APIAnthropicMessages,
			reply: "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			provider: func(baseURL string) ai.Provider {
				return ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "sk-ant-api03-test", Model: "custom-claude", BaseURL: baseURL, Compat: &ai.AnthropicMessagesCompat{
					SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolChanges: new(true),
				}})
			},
			shape: func(body map[string]any) []string {
				var shape []string
				for _, message := range nativeToolChangeItems(body["messages"]) {
					entry, _ := message["role"].(string)
					for _, block := range nativeToolChangeItems(message["content"]) {
						entry += " " + block["type"].(string)
						for _, key := range []string{"id", "tool_use_id"} {
							if id, ok := block[key].(string); ok {
								entry += ":" + id
							}
						}
						if tool, ok := block["tool"].(map[string]any); ok {
							entry += ":" + tool["name"].(string)
						}
					}
					shape = append(shape, entry)
				}
				return append(shape, "tools="+strings.Join(nativeToolChangeNames(body["tools"]), ","))
			},
			// The update waits for the next assistant message, so each tool_use
			// stays directly followed by its tool_result.
			want: []string{
				"user text", "assistant tool_use:call_a_fc_a", "user tool_result:call_a_fc_a",
				"user text", "system tool_addition:read", "assistant tool_use:toolu_1", "user tool_result:toolu_1",
				"user text", "tools=bash,__pi_deferred_placeholder__,read",
			},
		},
		{
			name: "openai responses", api: ai.APIOpenAIResponses,
			reply: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
			provider: func(baseURL string) ai.Provider {
				return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{APIKey: "test", Model: "custom", BaseURL: baseURL, IsReasoning: true, Compat: &ai.OpenAIResponsesCompat{
					SupportsMidConvoSystemMessages: new(true), SupportsAdditionalTools: new(true),
				}})
			},
			shape: func(body map[string]any) []string {
				var shape []string
				for _, item := range nativeToolChangeItems(body["input"]) {
					// Assistant text replay is covered by the provider tests.
					if item["type"] == "message" {
						continue
					}
					entry, _ := item["type"].(string)
					if entry == "" {
						entry, _ = item["role"].(string)
					}
					if callID, ok := item["call_id"].(string); ok {
						entry += ":" + callID
					}
					if tools, ok := item["tools"]; ok {
						entry += ":" + strings.Join(nativeToolChangeNames(tools), ",")
					}
					shape = append(shape, entry)
				}
				return append(shape, "tools="+strings.Join(nativeToolChangeNames(body["tools"]), ","))
			},
			// The addition loads in place, between the call and its output.
			want: []string{
				"developer", "user", "function_call:call_a", "additional_tools:read", "function_call_output:call_a",
				"user", "function_call:toolu_1", "function_call_output:toolu_1", "user", "tools=bash",
			},
		},
		{
			name: "kimi", api: ai.APIOpenAICompletions,
			reply: "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
			provider: func(baseURL string) ai.Provider {
				return ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", Model: "custom", ProviderID: "moonshotai", BaseURL: baseURL, Compat: &ai.OpenAICompat{
					SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolAdditions: new(true),
				}})
			},
			shape: func(body map[string]any) []string {
				var shape []string
				for _, message := range nativeToolChangeItems(body["messages"]) {
					entry, _ := message["role"].(string)
					for _, call := range nativeToolChangeItems(message["tool_calls"]) {
						entry += ":" + call["id"].(string)
					}
					if id, ok := message["tool_call_id"].(string); ok {
						entry += ":" + id
					}
					if tools, ok := message["tools"]; ok {
						entry += ":" + strings.Join(nativeToolChangeNames(tools, "function"), ",")
					}
					shape = append(shape, entry)
				}
				return append(shape, "tools="+strings.Join(nativeToolChangeNames(body["tools"], "function"), ","))
			},
			// Completions keeps transcript order: the tool-bearing message lands
			// where the persisted update sits, as upstream sends it.
			want: []string{
				"system", "user", "assistant:call_a_fc_a", "system:read", "tool:call_a_fc_a",
				"user", "assistant:toolu_1", "tool:toolu_1", "user", "tools=bash",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, bodies := nativeToolChangeServer(t, tc.reply)
			target := &ai.Model{ID: "custom", ProviderMeta: ai.ProviderMetadata{API: tc.api}}
			transcript := ai.NormalizeContext(ai.Context{Messages: convertToLLM(nativeToolChangePoisonedHistory(), target)})
			stream, err := tc.provider(server.URL).Stream(context.Background(), transcript, ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(context.Background()) {
			}
			got := tc.shape(bodies()[0])
			if !slices.Equal(got, tc.want) {
				t.Errorf("request = %q, want %q", got, tc.want)
			}
		})
	}
}
