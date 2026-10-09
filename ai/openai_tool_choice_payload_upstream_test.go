package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func toolChoiceModel(t *testing.T, provider, id string, strip bool) *Model {
	t.Helper()
	generated, ok := LookupModelExact(provider + "/" + id)
	if !ok {
		t.Fatalf("missing catalog model %s/%s", provider, id)
	}
	model := generated.ToModel()
	model.Capabilities = generated.ToCapabilities()
	model.Capabilities.MaxThinking = thinkingMaxLevel(generated.Reasoning, generated.ThinkingLevelMap)
	model.ProviderMeta.API = APIOpenAICompletions
	if strip {
		model.ProviderMeta.Compat = nil
	}
	return model
}

func localToolChoiceModel(id, name string) *Model {
	return &Model{ID: id, DisplayName: name, ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "local-vllm", BaseURL: "http://localhost:8000/v1", Reasoning: true}, Input: []string{"text"}, Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh, ContextWindow: 128000, MaxOutputTokens: 8192}}
}

func captureToolChoiceRequest(t *testing.T, model *Model, request Context, options StreamOptions, chunks []string) (map[string]json.RawMessage, *AssistantMessage, []AssistantMessageEvent) {
	t.Helper()
	provider := NewOpenAIProvider(OpenAIConfig{APIKey: "test", Model: model.ID, ModelMetadata: model, ProviderID: model.ProviderMeta.ProviderID, BaseURL: model.ProviderMeta.BaseURL, Compat: model.ProviderMeta.Compat}).(*openAIProvider)
	var payload map[string]json.RawMessage
	if chunks == nil {
		chunks = []string{`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`}
	}
	var sse strings.Builder
	for _, chunk := range chunks {
		fmt.Fprintf(&sse, "data: %s\n\n", chunk)
	}
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse.String()))}, nil
	})}
	options.IsReasoning = model.ProviderMeta.Reasoning
	if options.MaxTokens == 0 {
		options.MaxTokens = model.Capabilities.MaxOutputTokens
	}
	stream, err := provider.Stream(t.Context(), NormalizeContext(request), options)
	if err != nil {
		t.Fatal(err)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
	}
	return payload, stream.Result(), events
}

func toolChoicePing() ToolSchema {
	return ToolSchema{Name: "ping", Description: "Ping tool", Parameters: JsonObject{"type": "object", "properties": JsonObject{"ok": JsonObject{"type": "boolean"}}, "required": []string{"ok"}}}
}
func toolChoiceHi() Context {
	return Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}}
}
func requireToolChoiceField(t *testing.T, payload map[string]json.RawMessage, field, want string) {
	t.Helper()
	assertShapeJSON(t, payload[field], want)
}
func forbidToolChoiceFields(t *testing.T, payload map[string]json.RawMessage, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if value, ok := payload[field]; ok {
			t.Errorf("unexpected %s=%s", field, value)
		}
	}
}

func TestOpenAICompletionsToolChoicePayloadUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:112
	t.Run("forwards toolChoice from simple options to payload", func(t *testing.T) {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "openai", "gpt-4o-mini", true), Context{Messages: []Message{UserMessage{Content: UserText("Call ping with ok=true")}}, Tools: []ToolSchema{toolChoicePing()}}, StreamOptions{ToolChoice: "required"}, nil)
		requireToolChoiceField(t, payload, "tool_choice", `"required"`)
		var tools []any
		if err := json.Unmarshal(payload["tools"], &tools); err != nil || len(tools) == 0 {
			t.Fatalf("tools=%s err=%v", payload["tools"], err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:153
	t.Run("includes toolChoice when no tools are provided", func(t *testing.T) {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "openai", "gpt-4o-mini", true), Context{Messages: []Message{UserMessage{Content: UserText("Summarize the conversation")}}}, StreamOptions{ToolChoice: "none"}, nil)
		requireToolChoiceField(t, payload, "tool_choice", `"none"`)
		forbidToolChoiceFields(t, payload, "tools")
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:177
	t.Run("omits strict when compat disables strict mode", func(t *testing.T) {
		model := toolChoiceModel(t, "openai", "gpt-4o-mini", true)
		model.ProviderMeta.Compat = &ModelCompat{SupportsStrictMode: new(false)}
		payload, _, _ := captureToolChoiceRequest(t, model, Context{Messages: []Message{UserMessage{Content: UserText("Call ping with ok=true")}}, Tools: []ToolSchema{toolChoicePing()}}, StreamOptions{}, nil)
		var tools []struct {
			Function map[string]json.RawMessage `json:"function"`
		}
		if err := json.Unmarshal(payload["tools"], &tools); err != nil {
			t.Fatal(err)
		}
		if len(tools) != 1 || tools[0].Function == nil {
			t.Fatalf("tools=%s", payload["tools"])
		}
		forbidToolChoiceFields(t, tools[0].Function, "strict")
	})
	for _, builtin := range []bool{false, true} {
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:222,262
		name := "defaults unknown OpenAI-compatible endpoints to non-strict tools"
		if builtin {
			name = "preserves strict tools for capable built-in Chat Completions models"
		}
		t.Run(name, func(t *testing.T) {
			model := localToolChoiceModel("local-model", "Local Model")
			if builtin {
				model = toolChoiceModel(t, "groq", "openai/gpt-oss-20b", false)
				if model.ProviderMeta.Compat == nil || model.ProviderMeta.Compat.SupportsStrictMode == nil || !*model.ProviderMeta.Compat.SupportsStrictMode {
					t.Fatal("catalog strict mode missing")
				}
			}
			var tool ToolSchema
			if err := json.Unmarshal([]byte(`{"name":"ping","description":"Ping tool","parameters":{"type":"object","properties":{"required":{"type":"string"},"optional":{"type":"string"}},"required":["required"]},"constrainedSampling":{"type":"json_schema","strict":"prefer"}}`), &tool); err != nil {
				t.Fatal(err)
			}
			payload, _, _ := captureToolChoiceRequest(t, model, Context{Messages: []Message{UserMessage{Content: UserText("Call ping")}}, Tools: []ToolSchema{tool}}, StreamOptions{}, nil)
			var tools []struct {
				Function map[string]json.RawMessage `json:"function"`
			}
			if err := json.Unmarshal(payload["tools"], &tools); err != nil || len(tools) != 1 {
				t.Fatalf("tools=%s err=%v", payload["tools"], err)
			}
			var params map[string]json.RawMessage
			if err := json.Unmarshal(tools[0].Function["parameters"], &params); err != nil {
				t.Fatal(err)
			}
			if builtin {
				requireToolChoiceField(t, tools[0].Function, "strict", "true")
				requireToolChoiceField(t, params, "required", `["required","optional"]`)
			} else {
				forbidToolChoiceFields(t, tools[0].Function, "strict")
				requireToolChoiceField(t, params, "required", `["required"]`)
			}
		})
	}
	for _, tc := range []struct{ name, id, want string }{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:298
		{"maps Groq Qwen reasoning levels to default reasoning_effort", "qwen/qwen3.6-27b", "default"},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:326
		{"keeps normal reasoning_effort for groq models without compat mapping", "openai/gpt-oss-20b", "medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "groq", tc.id, false), toolChoiceHi(), StreamOptions{Thinking: ThinkingLevelMedium}, nil)
			requireToolChoiceField(t, payload, "reasoning_effort", fmt.Sprintf("%q", tc.want))
		})
	}
	for _, override := range []bool{false, true} {
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:354,554
		name := "enables tool_stream for supported z.ai models with tools"
		if override {
			name = "respects explicit z.ai tool_stream compat override"
		}
		t.Run(name, func(t *testing.T) {
			model := toolChoiceModel(t, "zai", "glm-5.2", false)
			if override {
				model.ProviderMeta.Compat = cloneCompat(model.ProviderMeta.Compat)
				model.ProviderMeta.Compat.ZaiToolStream = new(true)
			}
			payload, _, _ := captureToolChoiceRequest(t, model, Context{Messages: []Message{UserMessage{Content: UserText("Call ping with ok=true")}}, Tools: []ToolSchema{toolChoicePing()}}, StreamOptions{}, nil)
			requireToolChoiceField(t, payload, "tool_stream", "true")
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:428
	t.Run("maps z.ai GLM-5.2 thinking levels to reasoning_effort", func(t *testing.T) {
		for _, tc := range []struct {
			level  ModelThinkingLevel
			effort string
		}{{ThinkingLow, "high"}, {ThinkingMedium, "high"}, {ThinkingHigh, "high"}, {ThinkingMax, "max"}} {
			payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "zai", "glm-5.2", false), toolChoiceHi(), StreamOptions{Thinking: tc.level.ReasoningOption()}, nil)
			requireToolChoiceField(t, payload, "thinking", `{"type":"enabled","clear_thinking":false}`)
			requireToolChoiceField(t, payload, "reasoning_effort", fmt.Sprintf("%q", tc.effort))
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:526
	t.Run("omits z.ai GLM-5.2 reasoning_effort when thinking is off", func(t *testing.T) {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "zai", "glm-5.2", false), toolChoiceHi(), StreamOptions{}, nil)
		requireToolChoiceField(t, payload, "thinking", `{"type":"disabled"}`)
		forbidToolChoiceFields(t, payload, "reasoning_effort")
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:598
	t.Run("omits tool_stream when no tools are provided", func(t *testing.T) {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "zai", "glm-5.2", false), toolChoiceHi(), StreamOptions{}, nil)
		forbidToolChoiceFields(t, payload, "tool_stream")
	})
}

func TestOpenAICompletionsToolChoiceCatalogUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:391
	t.Run("stores z.ai tool_stream support in model compat metadata", func(t *testing.T) {
		for _, id := range []string{"glm-4.7", "glm-4.7", "glm-5-turbo", "glm-5.2"} {
			model := toolChoiceModel(t, "zai", id, false)
			if model.ProviderMeta.Compat == nil || model.ProviderMeta.Compat.ZaiToolStream == nil || !*model.ProviderMeta.Compat.ZaiToolStream {
				t.Fatalf("%s compat=%#v", id, model.ProviderMeta.Compat)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:398
	t.Run("stores z.ai effort metadata", func(t *testing.T) {
		for _, tc := range []struct {
			provider, id string
			want         ThinkingLevelMap
		}{
			{"zai", "glm-5.2", ThinkingLevelMap{ThinkingOff: new("none"), ThinkingMinimal: nil, ThinkingLow: nil, ThinkingMedium: nil, ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: new("max")}},
			{"zai", "glm-5.2-highspeed", ThinkingLevelMap{ThinkingOff: new("none"), ThinkingMinimal: nil, ThinkingLow: nil, ThinkingMedium: nil, ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: new("max")}},
			{"zai", "glm-5.3", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: nil, ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: new("max")}},
			{"zai-coding-cn", "glm-5.3", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: nil, ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: new("max")}},
		} {
			model := toolChoiceModel(t, tc.provider, tc.id, false)
			if model.ProviderMeta.Compat == nil || model.ProviderMeta.Compat.SupportsReasoningEffort == nil || !*model.ProviderMeta.Compat.SupportsReasoningEffort || !reflect.DeepEqual(model.ThinkingLevelMap, tc.want) {
				t.Fatalf("%s/%s metadata=%#v map=%#v", tc.provider, tc.id, model.ProviderMeta.Compat, model.ThinkingLevelMap)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1225
	t.Run("stores OpenRouter Kimi K2.6 reasoning replay compat in built-in metadata", func(t *testing.T) {
		c := toolChoiceModel(t, "openrouter", "moonshotai/kimi-k2.6", false).ProviderMeta.Compat
		if c == nil || c.SupportsDeveloperRole == nil || *c.SupportsDeveloperRole || c.RequiresReasoningContentOnAssistantMessages == nil || !*c.RequiresReasoningContentOnAssistantMessages {
			t.Fatalf("compat=%#v", c)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1233
	t.Run("stores Xiaomi MiMo reasoning replay compat in built-in metadata", func(t *testing.T) {
		for _, p := range []string{"xiaomi", "xiaomi-token-plan-cn", "xiaomi-token-plan-ams", "xiaomi-token-plan-sgp"} {
			c := toolChoiceModel(t, p, "mimo-v2.5-pro", false).ProviderMeta.Compat
			if c == nil || c.RequiresReasoningContentOnAssistantMessages == nil || !*c.RequiresReasoningContentOnAssistantMessages || c.ThinkingFormat != "deepseek" || c.MaxTokensField != "" || c.SupportsDeveloperRole != nil {
				t.Fatalf("%s compat=%#v", p, c)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1245
	t.Run("stores Qwen Token Plan reasoning replay compat in built-in metadata", func(t *testing.T) {
		for _, p := range []string{"qwen-token-plan", "qwen-token-plan-cn", "qwen-token-plan-individual"} {
			c := toolChoiceModel(t, p, "qwen3.7-max", false).ProviderMeta.Compat
			if c == nil || c.ThinkingFormat != "qwen" || c.RequiresReasoningContentOnAssistantMessages != nil || c.SupportsDeveloperRole == nil || *c.SupportsDeveloperRole || c.SupportsStore == nil || *c.SupportsStore {
				t.Fatalf("%s compat=%#v", p, c)
			}
		}
	})
}
