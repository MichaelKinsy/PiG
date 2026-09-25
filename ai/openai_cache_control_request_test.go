package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// openai-completions.ts applyAnthropicCacheControl marks the first instruction,
// last tool definition, and last text-bearing conversation message. Golden bodies
// distinguish text-part annotations from message annotations and preserve images.
func TestCompletionsRequestAnthropicCacheControl(t *testing.T) {
	image := ImageContent{MimeType: "image/png", Data: "aGk="}
	call := ToolCall{ID: "call", Name: "read", Arguments: JsonObject{"path": "x"}}
	for _, tc := range []struct {
		name     string
		messages []Message
		want     string
	}{
		{"user string", []Message{UserMessage{Content: UserText("hello")}}, `[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}]`},
		{"last text before image", []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "first"}, TextContent{Text: "last"}, image}}}, `[{"role":"user","content":[{"type":"text","text":"first"},{"type":"text","text":"last","cache_control":{"type":"ephemeral"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}}]}]`},
		{"assistant string", []Message{UserMessage{Content: UserText("hello")}, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "answer"}}}}, `[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"text","text":"answer","cache_control":{"type":"ephemeral"}}]}]`},
		{"tool result", []Message{UserMessage{Content: UserText("read")}, AssistantMessage{Content: []AssistantContentBlock{call}}, ToolResultMessage{ToolCallID: "call", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "result"}}}}, `[{"role":"user","content":"read"},{"role":"assistant","content":"","tool_calls":[{"id":"call","type":"function","function":{"name":"read","arguments":"{\"path\":\"x\"}"}}]},{"role":"tool","tool_call_id":"call","content":[{"type":"text","text":"result","cache_control":{"type":"ephemeral"}}]}]`},
		{"skip tool-only assistant", []Message{UserMessage{Content: UserText("read")}, AssistantMessage{Content: []AssistantContentBlock{call}}}, `[{"role":"user","content":[{"type":"text","text":"read","cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":"","tool_calls":[{"id":"call","type":"function","function":{"name":"read","arguments":"{\"path\":\"x\"}"}}]}]`},
		{"skip image-only user", []Message{AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "answer"}}}, UserMessage{Content: UserContentBlocks{image}}}, `[{"role":"assistant","content":[{"type":"text","text":"answer","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}}]}]`},
		{"skip empty string", []Message{UserMessage{Content: UserText("earlier")}, UserMessage{Content: UserText("")}}, `[{"role":"user","content":[{"type":"text","text":"earlier","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":""}]`},
		{"no conversation text", []Message{UserMessage{Content: UserContentBlocks{image}}}, `[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := append([]Message{SystemMessage{Content: SystemText("system"), ToolsAdded: []ToolSchema{
				{Name: "first", Description: "first tool", Parameters: JsonObject{"type": "object"}},
				{Name: "read", Description: "read tool", Parameters: JsonObject{"type": "object"}},
			}}}, tc.messages...)
			body := captureShapeRequest(t, func(url string) Provider {
				return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "test", Model: "test", ProviderID: "openrouter", Compat: &OpenAICompat{CacheControlFormat: "anthropic", SupportsStore: new(false), SupportsStrictMode: new(false)}})
			}, messages, StreamOptions{Env: ProviderEnv{"PI_CACHE_RETENTION": "short"}}, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			got, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, got, `{"model":"test","stream":true,"stream_options":{"include_usage":true},
				"messages":[{"role":"system","content":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}]},`+strings.TrimPrefix(tc.want, "[")+`,
				"tools":[{"type":"function","function":{"name":"first","description":"first tool","parameters":{"type":"object"}}},{"type":"function","function":{"name":"read","description":"read tool","parameters":{"type":"object"}},"cache_control":{"type":"ephemeral"}}]}`)
		})
	}
}

func TestCompletionsRequestAnthropicCacheLongRetentionAutoDetection(t *testing.T) {
	for _, providerID := range []string{"together", "cloudflare-workers-ai", "cloudflare-ai-gateway"} {
		t.Run(providerID, func(t *testing.T) {
			body := captureShapeRequest(t, func(url string) Provider {
				return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "test", Model: "test", ProviderID: providerID, Compat: &OpenAICompat{CacheControlFormat: "anthropic"}})
			}, []Message{UserMessage{Content: UserText("hello")}}, StreamOptions{CacheRetention: CacheRetentionLong}, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			assertShapeJSON(t, body["messages"], `[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}]`)
		})
	}
}

func TestCompletionsRequestAnthropicCacheRetention(t *testing.T) {
	for _, tc := range []struct {
		name      string
		format    string
		retention CacheRetention
		env       string
		long      bool
		want      string
	}{
		{"disabled", "anthropic", CacheRetentionNone, "long", true, `"hello"`},
		{"other format", "", CacheRetentionShort, "short", true, `"hello"`},
		{"short overrides env", "anthropic", CacheRetentionShort, "long", true, `[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]`},
		{"long", "anthropic", CacheRetentionLong, "short", true, `[{"type":"text","text":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}]`},
		{"long unsupported", "anthropic", CacheRetentionLong, "long", false, `[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]`},
		{"env long", "anthropic", "", "long", true, `[{"type":"text","text":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}]`},
		{"env none is default short", "anthropic", "", "none", true, `[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := captureShapeRequest(t, func(url string) Provider {
				return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "test", Model: "test", ProviderID: "openrouter", Compat: &OpenAICompat{CacheControlFormat: tc.format, SupportsLongCacheRetention: new(tc.long)}})
			}, []Message{UserMessage{Content: UserText("hello")}}, StreamOptions{CacheRetention: tc.retention, Env: ProviderEnv{"PI_CACHE_RETENTION": tc.env}}, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			assertShapeJSON(t, body["messages"], `[{"role":"user","content":`+tc.want+`}]`)
		})
	}
}
