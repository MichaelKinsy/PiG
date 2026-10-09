package coding

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func registryWithOverride(t *testing.T, provider, id string, override map[string]any) *AgentSessionServices {
	return registryTestServices(t, "", map[string]any{provider: map[string]any{"modelOverrides": map[string]any{id: override}}})
}
func TestModelRegistryModelOverridesUpstream(t *testing.T) {
	const sonnet = "anthropic/claude-sonnet-4"
	const opus = "anthropic/claude-opus-4.1"
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:689
	t.Run("model override applies to a single built-in model", func(t *testing.T) {
		s := registryWithOverride(t, "openrouter", sonnet, map[string]any{"name": "Custom Sonnet Name"})
		if mustRegistryModel(t, s, "openrouter", sonnet).DisplayName != "Custom Sonnet Name" || mustRegistryModel(t, s, "openrouter", opus).DisplayName == "Custom Sonnet Name" {
			t.Fatal("name override leaked or missing")
		}
	})
	for _, tc := range []struct {
		name   string
		models []ai.AnthropicAllowedFallbackModel
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:711
		{"Anthropic model override replaces allowed fallback metadata", []ai.AnthropicAllowedFallbackModel{{Provider: "anthropic", Model: "claude-opus-5", Cost: ai.ModelCost{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}}, {Provider: "anthropic", Model: "claude-opus-4-8", Cost: ai.ModelCost{Input: 4, Output: 20, CacheRead: 0.4, CacheWrite: 5}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:741
		{"empty allowed fallback model override disables server-side fallback", []ai.AnthropicAllowedFallbackModel{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := registryWithOverride(t, "anthropic", "claude-fable-5", map[string]any{"compat": map[string]any{"allowedFallbackModels": tc.models}})
			assertRegistryNoError(t, s)
			m := mustRegistryModel(t, s, "anthropic", "claude-fable-5")
			if m.ProviderMeta.Compat == nil || !reflect.DeepEqual(m.ProviderMeta.Compat.AllowedFallbackModels, tc.models) {
				t.Fatalf("compat=%+v", m.ProviderMeta.Compat)
			}
		})
	}
	// .upstream/v1.0.2/packages/coding-agent/test/model-registry.test.ts:757 (Pi 1.0.2 #9776 adds samplingParamsByThinkingLevel)
	t.Run("custom models and model overrides carry sampling params", func(t *testing.T) {
		s := registryFromJSON(t, `{"openrouter":{"baseUrl":"https://my-proxy.example.com/v1","api":"openai-completions","models":[{"id":"custom/sampling-model","samplingParams":{"temperature":1,"top_p":0.95,"top_k":0},"samplingParamsByThinkingLevel":{"low":{"temperature":0.6,"top_p":0.95},"high":{"temperature":0.8}}}],"modelOverrides":{"custom/sampling-model":{"samplingParamsByThinkingLevel":{"low":{"temperature":0.5,"top_k":20},"max":{"temperature":1}}},"anthropic/claude-sonnet-4":{"samplingParams":{"top_p":0.9},"samplingParamsByThinkingLevel":{"high":{"temperature":0.8}}}}}}`)
		assertRegistryNoError(t, s)
		custom := mustRegistryModel(t, s, "openrouter", "custom/sampling-model")
		if !reflect.DeepEqual(custom.SamplingParams, map[string]any{"temperature": float64(1), "top_p": 0.95, "top_k": float64(0)}) {
			t.Errorf("custom samplingParams = %v", custom.SamplingParams)
		}
		if want := (ai.SamplingParamsByThinkingLevel{
			ai.ThinkingLow:  {"temperature": 0.5, "top_p": 0.95, "top_k": float64(20)},
			ai.ThinkingHigh: {"temperature": 0.8},
			ai.ThinkingMax:  {"temperature": float64(1)},
		}); !reflect.DeepEqual(custom.SamplingParamsByThinkingLevel, want) {
			t.Errorf("custom samplingParamsByThinkingLevel = %v, want %v", custom.SamplingParamsByThinkingLevel, want)
		}
		sonnetModel := mustRegistryModel(t, s, "openrouter", sonnet)
		if !reflect.DeepEqual(sonnetModel.SamplingParams, map[string]any{"top_p": 0.9}) {
			t.Errorf("sonnet samplingParams = %v", sonnetModel.SamplingParams)
		}
		if want := (ai.SamplingParamsByThinkingLevel{ai.ThinkingHigh: {"temperature": 0.8}}); !reflect.DeepEqual(sonnetModel.SamplingParamsByThinkingLevel, want) {
			t.Errorf("sonnet samplingParamsByThinkingLevel = %v, want %v", sonnetModel.SamplingParamsByThinkingLevel, want)
		}
		// Models without sampling config keep it unset.
		opusModel := mustRegistryModel(t, s, "openrouter", opus)
		if opusModel.SamplingParams != nil || opusModel.SamplingParamsByThinkingLevel != nil {
			t.Errorf("opus sampling = %v / %v, want unset", opusModel.SamplingParams, opusModel.SamplingParamsByThinkingLevel)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:790
	t.Run("custom model and model override carry prompt cache lifetimes", func(t *testing.T) {
		s := registryFromJSON(t, `{"openrouter":{"baseUrl":"https://my-proxy.example.com/v1","api":"openai-completions","models":[{"id":"custom/cached-model","promptCache":{"short":120}}],"modelOverrides":{"anthropic/claude-sonnet-4":{"promptCache":{"short":300}}}},"anthropic":{"modelOverrides":{"claude-sonnet-4-6":{"promptCache":{"long":1800}}}}}`)
		assertRegistryNoError(t, s)
		for _, tc := range []struct {
			provider, id string
			want         ai.ModelPromptCache
		}{{"openrouter", "custom/cached-model", ai.ModelPromptCache{"short": 120}}, {"openrouter", sonnet, ai.ModelPromptCache{"short": 300}}, {"openrouter", opus, nil}, {"anthropic", "claude-sonnet-4-6", ai.ModelPromptCache{"short": 300, "long": 1800}}} {
			if actual := mustRegistryModel(t, s, tc.provider, tc.id).PromptCache; !reflect.DeepEqual(actual, tc.want) {
				t.Errorf("%s/%s cache=%v, want %v", tc.provider, tc.id, actual, tc.want)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:819
	t.Run("model override deep-merges image resize limits", func(t *testing.T) {
		s := registryFromJSON(t, `{"test":{"baseUrl":"https://example.com","apiKey":"test-key","api":"openai-completions","models":[{"id":"vision-model","input":["text","image"],"inputLimits":{"maxRequestBytes":33554432,"images":{"maxPerRequest":100,"resize":{"maxWidth":2000,"maxHeight":2000,"maxBytes":4718592,"jpegQuality":80}}}}],"modelOverrides":{"vision-model":{"inputLimits":{"images":{"resize":{"maxWidth":1568,"maxBytes":524288,"jpegQuality":75}}}}}}}`)
		assertRegistryNoError(t, s)
		data, err := json.Marshal(mustRegistryModel(t, s, "test", "vision-model").InputLimits)
		if err != nil {
			t.Fatal(err)
		}
		var got any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		var want any
		if err := json.Unmarshal([]byte(`{"maxRequestBytes":33554432,"images":{"maxPerRequest":100,"resize":{"maxWidth":1568,"maxHeight":2000,"maxBytes":524288,"jpegQuality":75}}}`), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("limits=%s", data)
		}
	})
	for _, tc := range []struct {
		name    string
		routing map[string]any
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:866
		{"model override with compat.openRouterRouting", map[string]any{"only": []string{"amazon-bedrock"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:907
		{"model override deep merges compat settings", map[string]any{"order": []string{"anthropic", "together"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := registryWithOverride(t, "openrouter", sonnet, map[string]any{"compat": map[string]any{"openRouterRouting": tc.routing}})
			got, err := json.Marshal(mustRegistryModel(t, s, "openrouter", sonnet).ProviderMeta.Compat.OpenRouterRouting)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(tc.routing)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("routing=%s, want %s", got, want)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:887
	t.Run("supportsFinishReason can be configured at provider and model levels", func(t *testing.T) {
		s := registryFromJSON(t, `{"openrouter":{"compat":{"supportsFinishReason":true},"modelOverrides":{"anthropic/claude-sonnet-4":{"compat":{"supportsFinishReason":false}}}}}`)
		a, b := mustRegistryModel(t, s, "openrouter", sonnet).ProviderMeta.Compat, mustRegistryModel(t, s, "openrouter", opus).ProviderMeta.Compat
		if a == nil || a.SupportsFinishReason == nil || *a.SupportsFinishReason || b == nil || b.SupportsFinishReason == nil || !*b.SupportsFinishReason {
			t.Fatalf("compat=%+v,%+v", a, b)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:929
	t.Run("multiple model overrides on same provider", func(t *testing.T) {
		s := registryFromJSON(t, `{"openrouter":{"modelOverrides":{"anthropic/claude-sonnet-4":{"compat":{"openRouterRouting":{"only":["amazon-bedrock"]}}},"anthropic/claude-opus-4.1":{"compat":{"openRouterRouting":{"only":["anthropic"]}}}}}}`)
		for _, tc := range []struct{ id, want string }{{sonnet, `{"only":["amazon-bedrock"]}`}, {opus, `{"only":["anthropic"]}`}} {
			got, err := json.Marshal(mustRegistryModel(t, s, "openrouter", tc.id).ProviderMeta.Compat.OpenRouterRouting)
			if err != nil || string(got) != tc.want {
				t.Fatalf("routing=%s err=%v", got, err)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:955
	t.Run("model override combined with baseUrl override", func(t *testing.T) {
		s := registryFromJSON(t, `{"openrouter":{"baseUrl":"https://my-proxy.example.com/v1","modelOverrides":{"anthropic/claude-sonnet-4":{"name":"Proxied Sonnet"}}}}`)
		a, b := mustRegistryModel(t, s, "openrouter", sonnet), mustRegistryModel(t, s, "openrouter", opus)
		if a.ProviderMeta.BaseURL != "https://my-proxy.example.com/v1" || a.DisplayName != "Proxied Sonnet" || b.ProviderMeta.BaseURL != "https://my-proxy.example.com/v1" || b.DisplayName == "Proxied Sonnet" {
			t.Fatal("override precedence wrong")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:981
	t.Run("model override for non-existent model ID is ignored", func(t *testing.T) {
		s := registryWithOverride(t, "openrouter", "nonexistent/model-id", map[string]any{"name": "This should not appear"})
		assertRegistryNoError(t, s)
		if s.Registry().Find("openrouter", "nonexistent/model-id") != nil {
			t.Fatal("override created a model")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1001
	t.Run("model override can change cost fields partially", func(t *testing.T) {
		s := registryWithOverride(t, "openrouter", sonnet, map[string]any{"cost": map[string]any{"input": 99}})
		cost := mustRegistryModel(t, s, "openrouter", sonnet).CostRates()
		if cost.Input != 99 || cost.Output <= 0 {
			t.Fatalf("cost=%+v", cost)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1022
	t.Run("model override can add headers at request time", func(t *testing.T) {
		s := registryWithOverride(t, "openrouter", sonnet, map[string]any{"headers": map[string]string{"X-Custom-Model-Header": "value"}})
		auth := s.Registry().GetAPIKeyAndHeaders(t.Context(), mustRegistryModel(t, s, "openrouter", sonnet))
		if !auth.OK || auth.Headers["X-Custom-Model-Header"] == nil || *auth.Headers["X-Custom-Model-Header"] != "value" {
			t.Fatalf("auth=%+v", auth)
		}
	})
	for _, tc := range []struct {
		name   string
		remove bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1045
		{"refresh() picks up model override changes", false},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1078
		{"removing model override restores built-in values", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := "First Name"
			if tc.remove {
				first = "Custom Name"
			}
			s := registryWithOverride(t, "openrouter", sonnet, map[string]any{"name": first})
			if mustRegistryModel(t, s, "openrouter", sonnet).DisplayName != first {
				t.Fatal("initial override missing")
			}
			next := map[string]any{}
			if !tc.remove {
				next["openrouter"] = map[string]any{"modelOverrides": map[string]any{sonnet: map[string]any{"name": "Second Name"}}}
			}
			writeRegistryModels(t, s, next)
			result := s.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
			if result.Aborted || len(result.Errors) > 0 {
				t.Fatal(result)
			}
			actual := mustRegistryModel(t, s, "openrouter", sonnet).DisplayName
			if tc.remove && actual == first || !tc.remove && actual != "Second Name" {
				t.Fatalf("name=%q", actual)
			}
		})
	}
}
