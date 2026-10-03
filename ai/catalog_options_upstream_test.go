package ai

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func assertCatalogJSON(t *testing.T, got any, want string) {
	t.Helper()
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected any
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("got %s, want %s", data, want)
	}
}

func TestBasetenModels(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/baseten-models.test.ts:16
	t.Run("keeps both GLM 5.2 endpoints text-only", func(t *testing.T) {
		for _, id := range []string{"zai-org/GLM-5.2", "zai-org/GLM-5.2-Fast"} {
			assertCatalogJSON(t, mustGeneratedModel(t, "baseten", id).Capabilities, `["text"]`)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/baseten-models.test.ts:21
	t.Run("models Kimi K2.6 reasoning as an explicit off/on toggle", func(t *testing.T) {
		m := mustGeneratedModel(t, "baseten", "moonshotai/Kimi-K2.6")
		assertCatalogJSON(t, m.ThinkingLevelMap, `{"off":"off","minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":null}`)
		if m.Compat == nil || m.Compat.SupportsReasoningEffort == nil || *m.Compat.SupportsReasoningEffort || m.Compat.ThinkingFormat != "baseten" {
			t.Fatalf("compat = %+v", m.Compat)
		}
		assertCatalogJSON(t, m.Compat.ChatTemplateArgs, `{"enable_thinking":{"$var":"thinking.enabled"}}`)
		assertCatalogJSON(t, GetSupportedThinkingLevels(m.ToModel()), `["off","high"]`)
		p := captureCatalogCompletionsPayload(t, "baseten", m.ID, ThinkingHigh)
		assertCatalogJSON(t, p["chat_template_args"], `{"enable_thinking":true}`)
		if _, ok := p["reasoning_effort"]; ok {
			t.Fatalf("unexpected reasoning_effort: %v", p)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/baseten-models.test.ts:61
	t.Run("sends Baseten chat_template_args with reasoning effort", func(t *testing.T) {
		p := captureCatalogCompletionsPayload(t, "baseten", "zai-org/GLM-5.2", ThinkingHigh)
		assertCatalogJSON(t, p["chat_template_args"], `{"enable_thinking":true}`)
		if p["reasoning_effort"] != "high" {
			t.Fatalf("payload = %v", p)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/baseten-models.test.ts:82
	t.Run("disables Baseten opt-in reasoning when thinking is off", func(t *testing.T) {
		p := captureCatalogCompletionsPayload(t, "baseten", "zai-org/GLM-5.2", "")
		assertCatalogJSON(t, p["chat_template_args"], `{"enable_thinking":false}`)
		if p["reasoning_effort"] != "none" {
			t.Fatalf("payload = %v", p)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/baseten-models.test.ts:102
	t.Run("resolves BASETEN_API_KEY from the environment", func(t *testing.T) {
		t.Setenv("BASETEN_API_KEY", "test-baseten-key")
		assertCatalogJSON(t, FindEnvKeys("baseten", nil), `["BASETEN_API_KEY"]`)
		if got := GetEnvAPIKey("baseten", nil); got != "test-baseten-key" {
			t.Fatal(got)
		}
	})
}

func TestTogetherModels(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/together-models.test.ts:16
	t.Run("registers the default Kimi K3 model via OpenAI-compatible Chat Completions API", func(t *testing.T) {
		m := mustGeneratedModel(t, "together", "moonshotai/Kimi-K3")
		if m.API != APIOpenAICompletions || m.Provider != "together" || m.BaseURL != "https://api.together.ai/v1" || !m.Reasoning || m.ContextWindow != 1048576 || m.MaxOutputTokens != 131072 {
			t.Fatalf("model = %+v", m)
		}
		assertCatalogJSON(t, m.Capabilities, `["text","image"]`)
		assertCatalogJSON(t, m.ThinkingLevelMap, `{"minimal":null,"low":null,"medium":null}`)
		assertCatalogJSON(t, (&Model{Capabilities: m.ToCapabilities()}).CostRates(), `{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":0}`)
		assertCatalogJSON(t, m.Compat, `{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":false,"maxTokensField":"max_tokens","thinkingFormat":"together","supportsStrictMode":false,"supportsLongCacheRetention":false}`)
	})
	// .upstream/v0.99.1/packages/ai/test/together-models.test.ts:45
	t.Run("models Together reasoning controls from the Together API surface", func(t *testing.T) {
		gpt := mustGeneratedModel(t, "together", "openai/gpt-oss-120b")
		assertCatalogJSON(t, gpt.ThinkingLevelMap, `{"off":null,"minimal":null,"low":"low","medium":"medium","high":"high","max":null,"xhigh":null}`)
		if gpt.Compat == nil || gpt.Compat.SupportsReasoningEffort == nil || !*gpt.Compat.SupportsReasoningEffort || gpt.Compat.ThinkingFormat != "openai" {
			t.Fatalf("compat = %+v", gpt.Compat)
		}
		deepseek := mustGeneratedModel(t, "together", "deepseek-ai/DeepSeek-V4-Pro")
		assertCatalogJSON(t, deepseek.ThinkingLevelMap, `{"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null}`)
		if deepseek.Compat == nil || deepseek.Compat.SupportsReasoningEffort == nil || !*deepseek.Compat.SupportsReasoningEffort || deepseek.Compat.ThinkingFormat != "together" {
			t.Fatalf("compat = %+v", deepseek.Compat)
		}
		minimax := mustGeneratedModel(t, "together", "MiniMaxAI/MiniMax-M2.7")
		assertCatalogJSON(t, minimax.ThinkingLevelMap, `{"off":null,"minimal":null,"low":null,"medium":null}`)
		if minimax.Compat == nil || minimax.Compat.ThinkingFormat != "" || minimax.Compat.SupportsReasoningEffort == nil || *minimax.Compat.SupportsReasoningEffort {
			t.Fatalf("compat = %+v", minimax.Compat)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/together-models.test.ts:80
	t.Run("resolves TOGETHER_API_KEY from the environment", func(t *testing.T) {
		t.Setenv("TOGETHER_API_KEY", "test-together-key")
		assertCatalogJSON(t, FindEnvKeys("together", nil), `["TOGETHER_API_KEY"]`)
		if got := GetEnvAPIKey("together", nil); got != "test-together-key" {
			t.Fatal(got)
		}
	})
}

func TestXiaomiModels(t *testing.T) {
	for _, provider := range []string{"xiaomi", "xiaomi-token-plan-cn", "xiaomi-token-plan-ams", "xiaomi-token-plan-sgp"} {
		// .upstream/v0.87.1/packages/ai/test/xiaomi-models.test.ts:9
		t.Run("omits deprecated models from "+provider, func(t *testing.T) {
			ids := generatedModelIDs(provider)
			for _, id := range []string{"mimo-v2-flash", "mimo-v2-omni", "mimo-v2-pro"} {
				if slices.Contains(ids, id) {
					t.Fatalf("deprecated model %s", id)
				}
			}
		})
		// .upstream/v0.87.1/packages/ai/test/xiaomi-models.test.ts:14
		t.Run("keeps replacement models on "+provider, func(t *testing.T) {
			ids := generatedModelIDs(provider)
			for _, id := range []string{"mimo-v2.5", "mimo-v2.5-pro"} {
				if !slices.Contains(ids, id) {
					t.Fatalf("missing replacement %s", id)
				}
			}
		})
	}
}

func TestModelCatalogTypes(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/model-catalog-types.test.ts:5
	// Go does not have string literal types. Assert the generated field values instead.
	t.Run("derives model API ID and provider literals from grouped model data", func(t *testing.T) {
		for _, id := range []string{"grok-4.5", "grok-4.6", "grok-4.7", "grok-4.3"} {
			m := mustGeneratedModel(t, "xai", id)
			if m.API != APIOpenAIResponses || m.ID != id || m.Provider != "xai" {
				t.Fatalf("model = %+v", m)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/model-catalog-types.test.ts:16
	t.Run("routes GitHub Copilot Grok 4.5 through the Responses API", func(t *testing.T) {
		if m := mustGeneratedModel(t, "github-copilot", "grok-4.5"); m.API != APIOpenAIResponses {
			t.Fatalf("API = %s", m.API)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/model-catalog-types.test.ts:22
	t.Run("routes all GitHub Copilot GPT models through the Responses API", func(t *testing.T) {
		found := false
		for _, m := range ListModels("github-copilot") {
			if strings.HasPrefix(m.ID, "gpt-") {
				found = true
				if m.API != APIOpenAIResponses {
					t.Fatalf("%s API = %s", m.ID, m.API)
				}
			}
		}
		if !found {
			t.Fatal("no GPT models")
		}
		if m := mustGeneratedModel(t, "github-copilot", "gpt-6-astra"); m.API != APIOpenAIResponses {
			t.Fatal(m.API)
		}
		for _, id := range []string{"gpt-6-sol", "gpt-6-luna"} {
			m := mustGeneratedModel(t, "github-copilot", id)
			if m.API != APIOpenAIResponses || m.ContextWindow != 1000000 || m.MaxOutputTokens != 128000 {
				t.Fatalf("model = %+v", m)
			}
			assertThinkingLevelMap(t, m, map[ThinkingLevel]string{ThinkingOff: "none", ThinkingMax: "max"})
		}
	})
}

func TestOpenRouterCacheControlModels(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openrouter-cache-control-models.test.ts:12
	for _, id := range []string{"~anthropic/claude-fable-latest", "~anthropic/claude-haiku-latest", "~anthropic/claude-opus-latest", "~anthropic/claude-sonnet-latest"} {
		t.Run("keeps completions cache control for "+id, func(t *testing.T) {
			m := mustGeneratedModel(t, "openrouter", id)
			if m.API != APIOpenAICompletions || m.Compat == nil || m.Compat.CacheControlFormat != "anthropic" {
				t.Fatalf("model = %+v", m)
			}
		})
	}
}
