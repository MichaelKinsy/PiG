package coding

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func registryFromJSON(t *testing.T, raw string) *AgentSessionServices {
	t.Helper()
	var providers map[string]any
	if err := json.Unmarshal([]byte(raw), &providers); err != nil {
		t.Fatal(err)
	}
	return registryTestServices(t, "", providers)
}
func mustRegistryModel(t *testing.T, s *AgentSessionServices, provider, id string) *ai.Model {
	t.Helper()
	m := s.Registry().Find(provider, id)
	if m == nil {
		t.Fatalf("missing model %s/%s (error %s)", provider, id, s.Registry().GetError())
	}
	return m
}
func assertRegistryNoError(t *testing.T, s *AgentSessionServices) {
	t.Helper()
	if err := s.Registry().GetError(); err != "" {
		t.Fatal(err)
	}
}

func TestModelRegistryCustomModelsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:234
	t.Run("built-in provider custom models inherit api and baseUrl without explicit fields", func(t *testing.T) {
		s := registryFromJSON(t, `{"openrouter":{"models":[{"id":"fake-provider/fake-model","name":"Fake model","reasoning":true,"input":["text"]}]}}`)
		assertRegistryNoError(t, s)
		m := mustRegistryModel(t, s, "openrouter", "fake-provider/fake-model")
		if m.ProviderMeta.API != ai.APIOpenAICompletions || m.ProviderMeta.BaseURL != "https://openrouter.ai/api/v1" {
			t.Fatalf("model=%+v", m)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:259
	t.Run("non-built-in provider custom models still require baseUrl", func(t *testing.T) {
		s := registryFromJSON(t, `{"my-custom-provider":{"apiKey":"test-key","models":[{"id":"my-model","api":"openai-completions","reasoning":false,"input":["text"]}]}}`)
		if !strings.Contains(s.Registry().GetError(), "baseUrl") {
			t.Fatal(s.Registry().GetError())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:278
	t.Run("reports every provider composition error", func(t *testing.T) {
		s := registryFromJSON(t, `{"broken-one":{"api":"openai-completions","models":[{"id":"one"}]},"broken-two":{"api":"openai-completions","models":[{"id":"two"}]}}`)
		for _, part := range []string{`Provider "broken-one"`, `Provider "broken-two"`} {
			if !strings.Contains(s.Registry().GetError(), part) {
				t.Errorf("error=%q, missing %q", s.Registry().GetError(), part)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:291
	t.Run("custom provider with same name as built-in merges with built-in models", func(t *testing.T) {
		s := registryTestServices(t, "", map[string]any{"anthropic": registryProviderConfig("https://my-proxy.example.com/v1", "anthropic-messages", "claude-custom")})
		models := registryModelsForProvider(s.Registry(), "anthropic")
		if len(models) <= 1 || s.Registry().Find("anthropic", "claude-custom") == nil {
			t.Fatal("custom merge lost models")
		}
		found := false
		for _, m := range models {
			found = found || strings.Contains(m.ModelID, "claude")
		}
		if !found {
			t.Fatal("Claude catalog missing")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:304
	t.Run("custom model with same id replaces built-in model by id", func(t *testing.T) {
		s := registryTestServices(t, "", map[string]any{"openrouter": registryProviderConfig("https://my-proxy.example.com/v1", "openai-completions", "anthropic/claude-sonnet-4")})
		count := 0
		for _, m := range registryModelsForProvider(s.Registry(), "openrouter") {
			if m.ModelID == "anthropic/claude-sonnet-4" {
				count++
				if m.BaseURL != "https://my-proxy.example.com/v1" {
					t.Errorf("URL=%s", m.BaseURL)
				}
			}
		}
		if count != 1 {
			t.Fatalf("matching models=%d", count)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:321
	t.Run("custom provider with same name as built-in does not affect other built-in providers", func(t *testing.T) {
		s := registryTestServices(t, "", map[string]any{"anthropic": registryProviderConfig("https://my-proxy.example.com/v1", "anthropic-messages", "claude-custom")})
		for _, id := range []string{"google", "openai"} {
			if len(registryModelsForProvider(s.Registry(), id)) == 0 {
				t.Errorf("missing provider %s", id)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:332
	t.Run("provider-level baseUrl applies to both built-in and custom models", func(t *testing.T) {
		s := registryTestServices(t, "", map[string]any{"anthropic": registryProviderConfig("https://merged-proxy.example.com/v1", "anthropic-messages", "claude-custom")})
		for _, m := range registryModelsForProvider(s.Registry(), "anthropic") {
			if m.BaseURL != "https://merged-proxy.example.com/v1" {
				t.Errorf("URL=%s", m.BaseURL)
			}
		}
	})
	for _, tc := range []struct {
		name, raw, model string
		usage            bool
		field            string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:345
		{"provider-level compat applies to custom models", `{"demo":{"baseUrl":"https://example.com/v1","apiKey":"DEMO_KEY","api":"openai-completions","compat":{"supportsUsageInStreaming":false,"maxTokensField":"max_tokens"},"models":[{"id":"demo-model","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}]}}`, "demo-model", false, "max_tokens"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:375
		{"model-level compat overrides provider-level compat for custom models", `{"demo":{"baseUrl":"https://example.com/v1","apiKey":"DEMO_KEY","api":"openai-completions","compat":{"supportsUsageInStreaming":false,"maxTokensField":"max_tokens"},"models":[{"id":"demo-model","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,"compat":{"supportsUsageInStreaming":true,"maxTokensField":"max_completion_tokens"}}]}}`, "demo-model", true, "max_completion_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := registryFromJSON(t, tc.raw)
			compat := mustRegistryModel(t, s, "demo", tc.model).ProviderMeta.Compat
			if compat == nil || compat.SupportsUsageInStreaming == nil || *compat.SupportsUsageInStreaming != tc.usage || compat.MaxTokensField != tc.field {
				t.Fatalf("compat=%+v", compat)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:409
	t.Run("provider-level compat applies to built-in models", func(t *testing.T) {
		s := registryFromJSON(t, `{"openrouter":{"compat":{"supportsUsageInStreaming":false,"supportsStrictMode":false}}}`)
		models := registryModelsForProvider(s.Registry(), "openrouter")
		if len(models) == 0 {
			t.Fatal("catalog missing")
		}
		for _, m := range models {
			if m.Compat == nil || m.Compat.SupportsUsageInStreaming == nil || *m.Compat.SupportsUsageInStreaming || m.Compat.SupportsStrictMode == nil || *m.Compat.SupportsStrictMode {
				t.Fatalf("compat=%+v", m.Compat)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:430
	t.Run("model schema accepts thinkingLevelMap and compat schema accepts supportsStrictMode and cacheControlFormat", func(t *testing.T) {
		s := registryFromJSON(t, `{"demo":{"baseUrl":"https://example.com/v1","apiKey":"DEMO_KEY","api":"openai-completions","models":[{"id":"demo-model","reasoning":true,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,"thinkingLevelMap":{"minimal":null,"high":"max"},"compat":{"supportsStrictMode":false,"cacheControlFormat":"anthropic"}}]}}`)
		assertRegistryNoError(t, s)
		m := mustRegistryModel(t, s, "demo", "demo-model")
		if !reflect.DeepEqual(m.ThinkingLevelMap, ai.ThinkingLevelMap{ai.ThinkingMinimal: nil, ai.ThinkingHigh: new("max")}) || m.ProviderMeta.Compat == nil || m.ProviderMeta.Compat.SupportsStrictMode == nil || *m.ProviderMeta.Compat.SupportsStrictMode || m.ProviderMeta.Compat.CacheControlFormat != "anthropic" {
			t.Fatalf("model=%+v", m)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:467
	t.Run("compat schema accepts chat template thinking configuration", func(t *testing.T) {
		s := registryFromJSON(t, `{"demo":{"baseUrl":"https://example.com/v1","apiKey":"DEMO_KEY","api":"openai-completions","models":[{"id":"kwargs-model","reasoning":true,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,"compat":{"thinkingFormat":"chat-template","chatTemplateKwargs":{"preserve_thinking":true,"thinking":{"$var":"thinking.enabled"}}}},{"id":"args-model","reasoning":true,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,"compat":{"thinkingFormat":"baseten","chatTemplateArgs":{"enable_thinking":{"$var":"thinking.enabled"}}}}]}}`)
		assertRegistryNoError(t, s)
		kwargs := mustRegistryModel(t, s, "demo", "kwargs-model").ProviderMeta.Compat
		args := mustRegistryModel(t, s, "demo", "args-model").ProviderMeta.Compat
		if kwargs == nil || kwargs.ThinkingFormat != "chat-template" || !reflect.DeepEqual(kwargs.ChatTemplateKwargs, map[string]any{"preserve_thinking": true, "thinking": map[string]any{"$var": "thinking.enabled"}}) || args == nil || args.ThinkingFormat != "baseten" || !reflect.DeepEqual(args.ChatTemplateArgs, map[string]any{"enable_thinking": map[string]any{"$var": "thinking.enabled"}}) {
			t.Fatalf("kwargs=%+v args=%+v", kwargs, args)
		}
	})
	for _, tc := range []struct{ name, flag string }{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:523
		{"compat schema accepts Anthropic eager tool input streaming flag", "supportsEagerToolInputStreaming"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:552
		{"compat schema accepts long cache retention flag", "supportsLongCacheRetention"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := registryProviderConfig("https://example.com", "anthropic-messages", "demo-model")
			config["apiKey"] = "DEMO_KEY"
			config["compat"] = map[string]any{tc.flag: false}
			config["models"] = []any{map[string]any{"id": "demo-model", "reasoning": true, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100}}
			s := registryTestServices(t, "", map[string]any{"demo": config})
			assertRegistryNoError(t, s)
			m := mustRegistryModel(t, s, "demo", "demo-model")
			raw, err := json.Marshal(m.ProviderMeta.Compat)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if value, exists := fields[tc.flag]; !exists || value != false {
				t.Fatalf("compat=%s", raw)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:581
	t.Run("model-level baseUrl overrides provider-level baseUrl for custom models", func(t *testing.T) {
		s := registryFromJSON(t, `{"opencode-go":{"baseUrl":"https://opencode.ai/zen/go/v1","apiKey":"TEST_KEY","models":[{"id":"minimax-m2.5","api":"anthropic-messages","baseUrl":"https://opencode.ai/zen/go","reasoning":true,"input":["text"],"cost":{"input":0.3,"output":1.2,"cacheRead":0.03,"cacheWrite":0},"contextWindow":204800,"maxTokens":131072},{"id":"glm-5","api":"openai-completions","reasoning":true,"input":["text"],"cost":{"input":1,"output":3.2,"cacheRead":0.2,"cacheWrite":0},"contextWindow":204800,"maxTokens":131072}]}}`)
		if mustRegistryModel(t, s, "opencode-go", "minimax-m2.5").ProviderMeta.BaseURL != "https://opencode.ai/zen/go" || mustRegistryModel(t, s, "opencode-go", "glm-5").ProviderMeta.BaseURL != "https://opencode.ai/zen/go/v1" {
			t.Fatal("model/provider URL precedence lost")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:618
	t.Run("modelOverrides still apply when provider also defines models", func(t *testing.T) {
		s := registryFromJSON(t, `{"openrouter":{"baseUrl":"https://my-proxy.example.com/v1","apiKey":"OPENROUTER_API_KEY","api":"openai-completions","models":[{"id":"custom/openrouter-model","name":"Custom OpenRouter Model","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":16384}],"modelOverrides":{"anthropic/claude-sonnet-4":{"name":"Overridden Built-in Sonnet"}}}}`)
		mustRegistryModel(t, s, "openrouter", "custom/openrouter-model")
		if mustRegistryModel(t, s, "openrouter", "anthropic/claude-sonnet-4").DisplayName != "Overridden Built-in Sonnet" {
			t.Fatal("builtin override missing")
		}
	})
	for _, tc := range []struct {
		name   string
		remove bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:652
		{"refresh() reloads merged custom models from disk", false},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:671
		{"removing custom models from models.json keeps built-in provider models", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := registryTestServices(t, "", map[string]any{"anthropic": registryProviderConfig("https://first-proxy.example.com/v1", "anthropic-messages", "claude-custom")})
			mustRegistryModel(t, s, "anthropic", "claude-custom")
			next := map[string]any{}
			if !tc.remove {
				next["anthropic"] = registryProviderConfig("https://second-proxy.example.com/v1", "anthropic-messages", "claude-custom-2")
			}
			writeRegistryModels(t, s, next)
			result := s.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
			if result.Aborted || len(result.Errors) > 0 {
				t.Fatal(result)
			}
			if s.Registry().Find("anthropic", "claude-custom") != nil {
				t.Fatal("old custom model retained")
			}
			if !tc.remove {
				mustRegistryModel(t, s, "anthropic", "claude-custom-2")
			}
			found := false
			for _, m := range registryModelsForProvider(s.Registry(), "anthropic") {
				found = found || strings.Contains(m.ModelID, "claude")
			}
			if !found {
				t.Fatal("builtin catalog lost")
			}
		})
	}
}
