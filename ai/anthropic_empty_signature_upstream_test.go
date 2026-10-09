package ai

import (
	"encoding/json"
	"testing"
)

func emptySignatureModel(compat *ModelCompat) *Model {
	return &Model{ID: "mimo-v2.5-pro", DisplayName: "MiMo-V2.5-Pro", Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh, ContextWindow: 1048576, MaxOutputTokens: 1024}, Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: "xiaomi-token-plan-ams", API: APIAnthropicMessages, Reasoning: true, Compat: compat}}
}

func emptySignatureContext(provider, model, thinking, signature string, answer bool) Context {
	content := []AssistantContentBlock{ThinkingContent{Thinking: thinking, ThinkingSignature: signature}}
	if answer {
		content = append(content, TextContent{Text: "answer"})
	}
	return Context{Messages: []Message{UserMessage{Content: UserText("first")}, AssistantMessage{API: APIAnthropicMessages, Provider: provider, Model: model, Content: content, StopReason: StopReasonStop}, UserMessage{Content: UserText("second")}}}
}

func assistantPayloadContent(t *testing.T, payload map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.Role == "assistant" {
			return message.Content
		}
	}
	t.Fatal("missing assistant payload")
	return nil
}

func TestAnthropicUpstreamEmptyThinkingSignature(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		model                     *Model
		signature, thinking, want string
	}{
		// .upstream/v0.87.1/packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts:82
		{"converts empty-signature thinking to text by default", emptySignatureModel(nil), "", "internal reasoning", `[{"type":"text","text":"internal reasoning"}]`},
		// .upstream/v0.87.1/packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts:88
		{"preserves empty thinking text when the signature is present", emptySignatureModel(nil), "signed-thinking", "", `[{"type":"thinking","thinking":"","signature":"signed-thinking"}]`},
		// .upstream/v0.87.1/packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts:94
		{"preserves empty-signature thinking when allowEmptySignature is enabled", emptySignatureModel(&ModelCompat{AllowEmptySignature: new(true)}), " ", "internal reasoning", `[{"type":"thinking","thinking":"internal reasoning","signature":""}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := upstreamAnthropicParams(t, tc.model, emptySignatureContext("xiaomi-token-plan-ams", "mimo-v2.5-pro", tc.thinking, tc.signature, false), StreamOptions{})
			assertShapeJSON(t, assistantPayloadContent(t, payload), tc.want)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts:101
	t.Run("allows empty thinking signatures for every Vercel AI Gateway model", func(t *testing.T) {
		models := ListModels("vercel-ai-gateway")
		if len(models) == 0 {
			t.Fatal("empty Vercel catalog")
		}
		for _, model := range models {
			if model.Compat == nil || model.Compat.AllowEmptySignature == nil || !*model.Compat.AllowEmptySignature {
				t.Errorf("unsigned thinking not enabled for %s", model.ID)
			}
		}
	})
	// .upstream/v0.99.1/packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts:108 (all four 0.99.1 rows)
	for _, id := range []string{"accounts/fireworks/models/deepseek-v4p1-flash", "accounts/fireworks/models/qwen3p8-max", "accounts/fireworks/models/qwen3p8-2p4t-a95b", "accounts/fireworks/models/nemotron-3-ultra-nvfp4"} {
		t.Run("preserves unsigned thinking for Fireworks "+id, func(t *testing.T) {
			model := upstreamCatalogModel(t, "fireworks", id)
			if !modelAllowsEmptySignature(model) {
				t.Fatal("missing compat flag")
			}
			payload := upstreamAnthropicParams(t, model, emptySignatureContext("fireworks", id, "internal reasoning", "", true), StreamOptions{})
			assertShapeJSON(t, assistantPayloadContent(t, payload), `[{"type":"thinking","thinking":"internal reasoning","signature":""},{"type":"text","text":"answer"}]`)
		})
	}
	// .upstream/v0.99.1/packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts:127
	t.Run("still converts cross-model Fireworks thinking to text", func(t *testing.T) {
		model := upstreamCatalogModel(t, "fireworks", "accounts/fireworks/models/deepseek-v4p1-flash")
		payload := upstreamAnthropicParams(t, model, emptySignatureContext("fireworks", "accounts/fireworks/models/nemotron-3-ultra-nvfp4", "internal reasoning", "", false), StreamOptions{})
		assertShapeJSON(t, assistantPayloadContent(t, payload), `[{"type":"text","text":"internal reasoning"}]`)
	})
	// .upstream/v0.87.1/packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts:140
	t.Run("allows empty signatures for Kimi Coding k3", func(t *testing.T) {
		model := upstreamCatalogModel(t, "kimi-coding", "k3")
		if !modelAllowsEmptySignature(model) {
			t.Fatal("missing compat flag")
		}
		payload := upstreamAnthropicParams(t, model, emptySignatureContext("kimi-coding", "k3", "internal reasoning", " ", false), StreamOptions{})
		assertShapeJSON(t, assistantPayloadContent(t, payload), `[{"type":"thinking","thinking":"internal reasoning","signature":""}]`)
	})
}
