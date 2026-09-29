package ai

import (
	"encoding/json"
	"fmt"
	"testing"
)

func replayToolChoiceContext(provider, id, thinking, signature string, followup bool) Context {
	blocks := []AssistantContentBlock{}
	if thinking != "" {
		blocks = append(blocks, ThinkingContent{Thinking: thinking, ThinkingSignature: signature})
	}
	blocks = append(blocks, ToolCall{ID: "call_1", Name: "read", Arguments: JsonObject{"path": "README.md"}})
	ctx := Context{Messages: []Message{UserMessage{Content: UserText("Read README.md")}, AssistantMessage{API: APIOpenAICompletions, Provider: provider, Model: id, Content: blocks, StopReason: StopReasonToolUse}, ToolResultMessage{ToolCallID: "call_1", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "contents"}}}}}
	if followup {
		ctx.Messages = append(ctx.Messages, UserMessage{Content: UserText("Continue")})
	}
	return ctx
}

func firstToolChoiceAssistant(t *testing.T, payload map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if string(message["role"]) == `"assistant"` {
			return message
		}
	}
	t.Fatal("no assistant message")
	return nil
}

func TestOpenAICompletionsToolChoiceReplayAndFormatsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:466
	t.Run("preserves z.ai thinking when replaying reasoning_content", func(t *testing.T) {
		model := toolChoiceModel(t, "zai", "glm-5.2", false)
		payload, _, _ := captureToolChoiceRequest(t, model, replayToolChoiceContext("zai", "glm-5.2", "prior reasoning", "reasoning_content", true), StreamOptions{Thinking: ThinkingHigh}, nil)
		requireToolChoiceField(t, firstToolChoiceAssistant(t, payload), "reasoning_content", `"prior reasoning"`)
		requireToolChoiceField(t, payload, "thinking", `{"type":"enabled","clear_thinking":false}`)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1257
	t.Run("replays Xiaomi MiMo assistant tool calls with empty reasoning_content when thinking is missing", func(t *testing.T) {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "xiaomi", "mimo-v2.5-pro", false), replayToolChoiceContext("xiaomi", "mimo-v2.5-pro", "", "", false), StreamOptions{Thinking: ThinkingHigh}, nil)
		assistant := firstToolChoiceAssistant(t, payload)
		requireToolChoiceField(t, assistant, "role", `"assistant"`)
		requireToolChoiceField(t, assistant, "reasoning_content", `""`)
		requireToolChoiceField(t, payload, "thinking", `{"type":"enabled"}`)
		requireToolChoiceField(t, payload, "reasoning_effort", `"high"`)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1369
	t.Run("replays OpenCode Go reasoning thinking blocks as reasoning_content", func(t *testing.T) {
		model := toolChoiceModel(t, "opencode-go", "kimi-k2.6", true)
		cfg := &OpenAICompat{SupportsStore: new(false), SupportsDeveloperRole: new(false), SupportsReasoningEffort: new(true), SupportsUsageInStreaming: new(true), SupportsFinishReason: new(true), MaxTokensField: "max_completion_tokens", ThinkingFormat: "openai", SupportsStrictMode: new(true), SupportsOpenAIGrammarTools: new(false), SendSessionAffinityHeaders: new(false), SessionAffinityFormat: SessionAffinityOpenAI, SupportsLongCacheRetention: new(true)}
		provider := &openAIProvider{cfg: OpenAIConfig{Model: model.ID, ProviderID: "opencode-go", Compat: cfg, ModelMetadata: model}}
		messages, err := provider.convertMessagesWithCompat([]Message{AssistantMessage{API: APIOpenAICompletions, Provider: "opencode-go", Model: "kimi-k2.6", Content: []AssistantContentBlock{ThinkingContent{Thinking: "think", ThinkingSignature: "reasoning"}, ToolCall{ID: "call_1", Name: "read", Arguments: JsonObject{"path": "README.md"}}}, StopReason: StopReasonStop}}, nil, "system", false)
		if err != nil || len(messages) == 0 {
			t.Fatalf("messages=%#v err=%v", messages, err)
		}
		encoded, _ := json.Marshal(messages[0])
		var first map[string]json.RawMessage
		_ = json.Unmarshal(encoded, &first)
		requireToolChoiceField(t, first, "role", `"assistant"`)
		requireToolChoiceField(t, first, "reasoning_content", `"think"`)
		forbidToolChoiceFields(t, first, "reasoning")
	})
	for _, tc := range []struct {
		name, provider, id, thinking string
		level                        ThinkingLevel
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1428
		{"sends thinking disabled for OpenCode Go Kimi K2.6 when thinking is off", "opencode-go", "kimi-k2.6", `{"type":"disabled"}`, ""},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1450
		{"sends thinking enabled for OpenCode Go Kimi K2.6 when thinking is enabled", "opencode-go", "kimi-k2.6", `{"type":"enabled"}`, ThinkingHigh},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1473
		{"omits disabled thinking for Moonshot Kimi K2.7 Code models/moonshotai", "moonshotai", "kimi-k2.7-code", "", ""},
		{"omits disabled thinking for Moonshot Kimi K2.7 Code models/moonshotai-cn", "moonshotai-cn", "kimi-k2.7-code", "", ""},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1499
		{"keeps disabled thinking for Moonshot Kimi K2.6 when thinking is off", "moonshotai-cn", "kimi-k2.6", `{"type":"disabled"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, tc.provider, tc.id, false), toolChoiceHi(), StreamOptions{Thinking: tc.level}, nil)
			if tc.thinking == "" {
				forbidToolChoiceFields(t, payload, "thinking")
			} else {
				requireToolChoiceField(t, payload, "thinking", tc.thinking)
			}
			forbidToolChoiceFields(t, payload, "reasoning_effort")
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1619
	t.Run("omits reasoning effort for OpenCode Grok Build", func(t *testing.T) {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "opencode", "grok-build-0.1", false), toolChoiceHi(), StreamOptions{Thinking: ThinkingHigh}, nil)
		forbidToolChoiceFields(t, payload, "reasoning_effort")
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1763
	t.Run("uses OpenRouter reasoning object instead of reasoning_effort", func(t *testing.T) {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "openrouter", "deepseek/deepseek-r1", false), toolChoiceHi(), StreamOptions{Thinking: ThinkingHigh}, nil)
		requireToolChoiceField(t, payload, "reasoning", `{"effort":"high"}`)
		forbidToolChoiceFields(t, payload, "reasoning_effort")
	})
	for _, qwen := range []bool{false, true} {
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1795,1819
		name := "uses configurable chat template boolean thinking kwargs"
		if qwen {
			name = "uses qwen chat template thinking kwargs"
		}
		t.Run(name, func(t *testing.T) {
			for _, level := range []ThinkingLevel{ThinkingHigh, ""} {
				model := localToolChoiceModel("deepseek-ai/DeepSeek-V3.1", "DeepSeek V3.1 via vLLM")
				model.ProviderMeta.Compat = &ModelCompat{ThinkingFormat: "chat-template", SupportsReasoningEffort: new(false), ChatTemplateKwargs: map[string]any{"thinking": map[string]any{"$var": "thinking.enabled"}}}
				if qwen {
					model.ID = "Qwen/Qwen3-Coder"
					model.DisplayName = "Qwen3 Coder via vLLM"
					model.ProviderMeta.Compat = &ModelCompat{ThinkingFormat: "qwen-chat-template", SupportsReasoningEffort: new(false)}
				}
				payload, _, _ := captureToolChoiceRequest(t, model, toolChoiceHi(), StreamOptions{Thinking: level}, nil)
				want := fmt.Sprintf(`{"thinking":%t}`, level != "")
				if qwen {
					want = fmt.Sprintf(`{"enable_thinking":%t,"preserve_thinking":true}`, level != "")
				} else {
					forbidToolChoiceFields(t, payload, "thinking")
				}
				requireToolChoiceField(t, payload, "chat_template_kwargs", want)
				forbidToolChoiceFields(t, payload, "reasoning_effort")
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1844
	t.Run("uses configurable chat template effort kwargs with static kwargs", func(t *testing.T) {
		model := localToolChoiceModel("unsloth/gpt-oss-120b-GGUF", "GPT OSS via vLLM")
		model.ThinkingLevelMap = ThinkingLevelMap{ThinkingXHigh: new("max")}
		model.ProviderMeta.Compat = &ModelCompat{ThinkingFormat: "chat-template", SupportsReasoningEffort: new(false), ChatTemplateKwargs: map[string]any{"preserve_thinking": true, "reasoning_effort": map[string]any{"$var": "thinking.effort", "omitWhenOff": true}}}
		payload, _, _ := captureToolChoiceRequest(t, model, toolChoiceHi(), StreamOptions{Thinking: ThinkingXHigh}, nil)
		requireToolChoiceField(t, payload, "chat_template_kwargs", `{"preserve_thinking":true,"reasoning_effort":"max"}`)
		forbidToolChoiceFields(t, payload, "reasoning_effort")
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1866
	t.Run("uses Ant Ling compatibility metadata", func(t *testing.T) {
		model := toolChoiceModel(t, "ant-ling", "Ring-2.6-1T", false)
		c := model.ProviderMeta.Compat
		if c == nil || c.SupportsStore == nil || *c.SupportsStore || c.SupportsDeveloperRole == nil || *c.SupportsDeveloperRole || c.SupportsReasoningEffort == nil || *c.SupportsReasoningEffort || c.MaxTokensField != "max_tokens" || c.ThinkingFormat != "ant-ling" || c.SupportsLongCacheRetention == nil || *c.SupportsLongCacheRetention || c.SupportsStrictMode == nil || !*c.SupportsStrictMode || c.RequiresReasoningContentOnAssistantMessages != nil {
			t.Fatalf("compat=%#v", c)
		}
		request := toolChoiceHi()
		request.SystemPrompt = "Follow instructions."
		payload, _, _ := captureToolChoiceRequest(t, model, request, StreamOptions{MaxTokens: 123, Thinking: ThinkingHigh, CacheRetention: CacheRetentionLong, SessionID: "ant-ling-session"}, nil)
		requireToolChoiceField(t, payload, "max_tokens", "123")
		forbidToolChoiceFields(t, payload, "max_completion_tokens", "reasoning_effort", "store", "prompt_cache_key", "prompt_cache_retention")
		requireToolChoiceField(t, payload, "reasoning", `{"effort":"high"}`)
		var messages []map[string]json.RawMessage
		if err := json.Unmarshal(payload["messages"], &messages); err != nil || len(messages) == 0 {
			t.Fatalf("messages=%s err=%v", payload["messages"], err)
		}
		requireToolChoiceField(t, messages[0], "role", `"system"`)
	})
	// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1919
	t.Run("omits Ant Ling reasoning for unmapped direct reasoning efforts and non-reasoning models", func(t *testing.T) {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "ant-ling", "Ring-2.6-1T", false), toolChoiceHi(), StreamOptions{ReasoningEffort: "medium"}, nil)
		forbidToolChoiceFields(t, payload, "reasoning")
		payload, _, _ = captureToolChoiceRequest(t, toolChoiceModel(t, "ant-ling", "Ling-2.6-flash", false), toolChoiceHi(), StreamOptions{Thinking: ThinkingHigh}, nil)
		forbidToolChoiceFields(t, payload, "reasoning")
	})
}

func TestOpenAICompletionsToolChoiceInstructionRolesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, provider, id, role string
		strip                    bool
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1153
		{"uses system messages for non-OpenAI/Anthropic OpenRouter reasoning model instructions", "openrouter", "deepseek/deepseek-v4-pro", "system", false},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1175
		{"keeps developer messages for OpenAI and Anthropic OpenRouter batch instructions/openai", "openrouter", "openai/gpt-5.2-codex", "developer", false},
		{"keeps developer messages for OpenAI and Anthropic OpenRouter batch instructions/anthropic", "openrouter", "anthropic/claude-fable-5.1:batch", "developer", false},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1202
		{"keeps developer messages for OpenAI reasoning model instructions", "openai", "gpt-5.5", "developer", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := toolChoiceHi()
			ctx.SystemPrompt = "Follow instructions."
			payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, tc.provider, tc.id, tc.strip), ctx, StreamOptions{}, nil)
			var messages []map[string]json.RawMessage
			if err := json.Unmarshal(payload["messages"], &messages); err != nil || len(messages) == 0 {
				t.Fatalf("messages=%s err=%v", payload["messages"], err)
			}
			requireToolChoiceField(t, messages[0], "role", fmt.Sprintf("%q", tc.role))
		})
	}
}

func TestOpenAICompletionsToolChoiceMaxTokensUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []*Model
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1521
		{"sends max_tokens for OpenCode completions models", []*Model{toolChoiceModel(t, "opencode-go", "kimi-k2.6", false), toolChoiceModel(t, "opencode", "kimi-k2.6", false)}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1548
		{"sends max_tokens for built-in and custom DeepSeek API models", []*Model{toolChoiceModel(t, "deepseek", "deepseek-flash", false), toolChoiceModel(t, "deepseek", "deepseek-v4-pro", false)}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-tool-choice.test.ts:1592
		{"sends max_tokens for Z.AI completions models", []*Model{toolChoiceModel(t, "zai", "glm-5-turbo", false), toolChoiceModel(t, "zai", "glm-5.2", false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, model := range tc.models {
				if model.ProviderMeta.Compat == nil || model.ProviderMeta.Compat.MaxTokensField != "max_tokens" {
					t.Fatalf("catalog compat=%#v", model.ProviderMeta.Compat)
				}
			}
			if tc.name == "sends max_tokens for built-in and custom DeepSeek API models" {
				for _, entry := range []struct{ id, name, url string }{{"custom-deepseek-model", "Custom DeepSeek Model", "https://api.deepseek.com"}, {"custom-uppercase-deepseek-model", "Custom Uppercase DeepSeek Model", "https://API.DeepSeek.COM"}} {
					model := localToolChoiceModel(entry.id, entry.name)
					model.ProviderMeta.ProviderID = "custom-deepseek"
					model.ProviderMeta.BaseURL = entry.url
					tc.models = append(tc.models, model)
				}
			}
			for _, model := range tc.models {
				payload, _, _ := captureToolChoiceRequest(t, model, toolChoiceHi(), StreamOptions{MaxTokens: 123}, nil)
				requireToolChoiceField(t, payload, "max_tokens", "123")
				forbidToolChoiceFields(t, payload, "max_completion_tokens")
			}
		})
	}
}

func TestResponsesNativeReasoningEffortIsNotClamped(t *testing.T) {
	payload, _, _ := captureResponsesCompat(t, responsesCompatConfig(t, "openai", "gpt-5-mini"), Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}}, StreamOptions{ReasoningEffort: "minimal"}, "data: [DONE]\n\n")
	var reasoning struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal(payload["reasoning"], &reasoning); err != nil || reasoning.Effort != "minimal" {
		t.Fatalf("reasoning=%s err=%v", payload["reasoning"], err)
	}
}

func TestCompletionsNativeReasoningEffortIsNotClamped(t *testing.T) {
	for _, tc := range []struct {
		level ThinkingLevel
		want  string
	}{{ThinkingMedium, ""}, {ThinkingHigh, `{"effort":"high"}`}} {
		payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, "ant-ling", "Ring-2.6-1T", false), toolChoiceHi(), StreamOptions{ReasoningEffort: string(tc.level)}, nil)
		if tc.want == "" {
			forbidToolChoiceFields(t, payload, "reasoning")
		} else {
			requireToolChoiceField(t, payload, "reasoning", tc.want)
		}
	}
}
