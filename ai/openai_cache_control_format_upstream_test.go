package ai

import (
	"encoding/json"
	"testing"
)

func TestOpenAICompletionsCacheControlFormatUpstream(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		alias, toolResult, disabled bool
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-cache-control-format.test.ts:130
		{"applies Anthropic-style cache markers when model compat enables them", false, false, false},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-cache-control-format.test.ts:156
		{"preserves Anthropic-style cache markers for OpenRouter Anthropic batch aliases", true, false, false},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-cache-control-format.test.ts:162
		{"moves the conversation cache marker to a tool result", true, true, false},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-cache-control-format.test.ts:203
		{"omits Anthropic-style cache markers when cacheRetention is none", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modelID, format := "custom-qwen", "anthropic"
			if tc.alias {
				model, ok := LookupModel("openrouter/anthropic/claude-fable-5.1:batch")
				if !ok || model.Compat == nil {
					t.Fatal("missing catalog alias")
				}
				modelID, format = model.ID, model.Compat.CacheControlFormat
			}
			messages := []Message{SystemMessage{Content: SystemText("System prompt"), ToolsAdded: []ToolSchema{{Name: "read", Description: "Read a file", Parameters: JsonObject{"type": "object", "properties": JsonObject{"path": JsonObject{"type": "string"}}, "required": []string{"path"}}}}}, UserMessage{Content: UserText("Hello")}}
			if tc.toolResult {
				messages[1] = UserMessage{Content: UserText("Read the file")}
				messages = append(messages, AssistantMessage{API: APIOpenAICompletions, Provider: "openrouter", Model: modelID, StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "call_1", Name: "read", Arguments: JsonObject{"path": "README.md"}}}}, ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "file contents"}}})
			}
			options := StreamOptions{Env: ProviderEnv{"PI_CACHE_RETENTION": ""}}
			if tc.disabled {
				options.CacheRetention = CacheRetentionNone
			}
			body := captureShapeRequest(t, func(url string) Provider {
				return NewOpenAIProvider(OpenAIConfig{Model: modelID, ProviderID: "openrouter", APIKey: "test-key", BaseURL: url, Compat: &OpenAICompat{CacheControlFormat: format}})
			}, messages, options, "data: {\"id\":\"chatcmpl-test\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":0}}}\n\n")
			var got []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(body["messages"], &got); err != nil {
				t.Fatal(err)
			}
			var tools []map[string]json.RawMessage
			if err := json.Unmarshal(body["tools"], &tools); err != nil {
				t.Fatal(err)
			}
			if len(tools) != 1 || len(got) < 2 {
				t.Fatalf("messages=%#v tools=%#v", got, tools)
			}
			if tc.disabled {
				assertShapeJSON(t, got[0].Content, `"System prompt"`)
				assertShapeJSON(t, got[len(got)-1].Content, `"Hello"`)
				if _, ok := tools[0]["cache_control"]; ok {
					t.Fatal("disabled tool cache marker present")
				}
				return
			}
			assertShapeJSON(t, got[0].Content, `[{"type":"text","text":"System prompt","cache_control":{"type":"ephemeral"}}]`)
			assertShapeJSON(t, tools[0]["cache_control"], `{"type":"ephemeral"}`)
			if tc.toolResult {
				assertShapeJSON(t, got[1].Content, `"Read the file"`)
				if got[len(got)-1].Role != "tool" {
					t.Fatal("last message is not tool")
				}
				assertShapeJSON(t, got[len(got)-1].Content, `[{"type":"text","text":"file contents","cache_control":{"type":"ephemeral"}}]`)
			} else {
				if got[len(got)-1].Role != "user" {
					t.Fatal("last message is not user")
				}
				assertShapeJSON(t, got[len(got)-1].Content, `[{"type":"text","text":"Hello","cache_control":{"type":"ephemeral"}}]`)
			}
		})
	}
}
