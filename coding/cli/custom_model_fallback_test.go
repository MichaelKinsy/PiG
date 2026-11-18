package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// customModelFields are the members a custom model id takes from the provider's model it is a copy of: every member but id and name
// (model-resolver.ts:308-312 `{ ...baseModel, id: modelId, name: modelId }`).
func customModelFields(t *testing.T, model *ai.Model) map[string]string {
	t.Helper()
	compat, err := json.Marshal(model.ProviderMeta.Compat)
	if err != nil {
		t.Fatal(err)
	}
	cost := model.CostRates()
	return map[string]string{
		"contextWindow":    fmt.Sprint(model.Capabilities.ContextWindow),
		"maxTokens":        fmt.Sprint(model.Capabilities.MaxOutputTokens),
		"cost":             fmt.Sprintf("input %v output %v cacheRead %v cacheWrite %v tiers %v", cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite, cost.Tiers),
		"input":            strings.Join(model.Input, ","),
		"images":           fmt.Sprint(model.Capabilities.SupportsImages),
		"api":              string(model.ProviderMeta.API),
		"baseUrl":          model.ProviderMeta.BaseURL,
		"compat":           string(compat),
		"reasoning":        fmt.Sprint(model.ProviderMeta.Reasoning),
		"maxThinking":      string(model.Capabilities.MaxThinking),
		"thinkingLevelMap": fmt.Sprint(len(model.ThinkingLevelMap)),
		"type":             string(model.Type),
	}
}

// TestStartupCustomModelIDIsACopyOfTheProvidersModel pins what `--model <provider>/<id the catalog does not know>` starts with, for every
// shape of provider that has models. Pi's resolveCliModel copies the provider's default model when the provider lists it, else its first
// model, under the id (model-resolver.ts:302-313 buildFallbackModel) and main.ts:536-537 gives the session that object: the window, the
// output limit, the prices, the input kinds and the compat flags are the copied model's. Pi 1.5.2, run with a registered provider, a
// models.json provider and a models.json override of a built-in default, reports in get_state a model that differs from the copied one in
// id and name only: the registered window and limit, 128000 and 16384 for a models.json model with only an id, the overridden values.
func TestStartupCustomModelIDIsACopyOfTheProvidersModel(t *testing.T) {
	registeredModels := func(prefix string, window, maxTokens int) []extension.ProviderModelConfig {
		return []extension.ProviderModelConfig{
			{ID: prefix + "-1", Name: "Registered One", Input: []string{"text", "image"}, Cost: extension.ProviderModelCost{Input: 3, Output: 4, CacheRead: 0.3, CacheWrite: 0.4},
				ContextWindow: window, MaxTokens: maxTokens, Headers: map[string]string{"X-Model-H": "m"},
				Compat: map[string]any{"supportsDeveloperRole": false, "maxTokensField": "max_tokens"}},
			{ID: prefix + "-2", Name: "Registered Two", Reasoning: true, Input: []string{"text"}, Cost: extension.ProviderModelCost{Input: 5, Output: 6},
				ContextWindow: 96000, MaxTokens: 9000},
		}
	}
	modelsJSON := func(api string) string {
		return `{"providers":{"configured":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"unused","api":"` + api + `","headers":{"X-Prov-H":"p"},"models":[` +
			`{"id":"configured-1","name":"Configured One","reasoning":true,"input":["text","image"],"contextWindow":48000,"maxTokens":3000,` +
			`"cost":{"input":3,"output":4,"cacheRead":0.3,"cacheWrite":0.4},"compat":{"supportsDeveloperRole":false},"headers":{"X-Model-H":"m"}}]}}}`
	}
	register := func(id string, config extension.ProviderConfig) func(*testing.T, *coding.AgentSessionServices) {
		return func(t *testing.T, services *coding.AgentSessionServices) {
			t.Helper()
			if err := services.Registry().RegisterExtensionProvider(id, config); err != nil {
				t.Fatal(err)
			}
		}
	}
	defaultOf := func(provider string) string { return codingagent.DefaultModelPerProvider()[provider] }
	cases := []struct {
		name string
		// config is the models.json of the agent directory; register runs after the services exist.
		config   string
		register func(*testing.T, *coding.AgentSessionServices)
		provider string
		// sibling is the model Pi copies; window and maxTokens are Pi's values, 0 for a built-in provider (the catalog default's).
		sibling           string
		window, maxTokens int
	}{
		{name: "extension provider with its own api and stream", provider: "ext-stream", sibling: "es-1", window: 64000, maxTokens: 2048,
			register: register("ext-stream", extension.ProviderConfig{BaseURL: "http://127.0.0.1:1/es", APIKey: "unused", API: "ext-stream-api", Headers: map[string]string{"X-Prov-H": "p"},
				StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
					var none extension.AssistantMessageEventStream
					return none
				}, Models: registeredModels("es", 64000, 2048)})},
		{name: "extension provider on openai-completions", provider: "ext-completions", sibling: "ec-1", window: 32000, maxTokens: 1000,
			register: register("ext-completions", extension.ProviderConfig{BaseURL: "http://127.0.0.1:1/v1", APIKey: "unused", API: ai.APIOpenAICompletions, Headers: map[string]string{"X-Prov-H": "p"},
				Models: registeredModels("ec", 32000, 1000)})},
		{name: "extension provider on openai-responses", provider: "ext-responses", sibling: "er-1", window: 40000, maxTokens: 1500,
			register: register("ext-responses", extension.ProviderConfig{BaseURL: "http://127.0.0.1:1/v1", APIKey: "unused", API: ai.APIOpenAIResponses, Headers: map[string]string{"X-Prov-H": "p"},
				Models: registeredModels("er", 40000, 1500)})},
		{name: "extension registers a built-in provider's base URL and no models", provider: "groq", sibling: defaultOf("groq"),
			register: register("groq", extension.ProviderConfig{BaseURL: "http://127.0.0.1:1/groq"})},
		{name: "provider object", provider: "provider-object", sibling: "po-1", window: 72000, maxTokens: 2500,
			register: func(t *testing.T, services *coding.AgentSessionServices) {
				t.Helper()
				model := &ai.Model{ID: "po-1", DisplayName: "Provider Object One", Input: []string{"text"},
					Capabilities: ai.ModelCapabilities{ContextWindow: 72000, MaxOutputTokens: 2500, InputCostPer1M: 7, OutputCostPer1M: 8},
					ProviderMeta: ai.ProviderMetadata{ProviderID: "provider-object", API: ai.APIOpenAICompletions, BaseURL: "http://127.0.0.1:1/v1"}}
				provider := &ai.ModelsProvider{ID: "provider-object", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil }}
				if err := services.ModelRuntime().RegisterNativeProvider(provider); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "models.json provider on openai-completions", config: modelsJSON("openai-completions"), provider: "configured", sibling: "configured-1", window: 48000, maxTokens: 3000},
		{name: "models.json provider on openai-responses", config: modelsJSON("openai-responses"), provider: "configured", sibling: "configured-1", window: 48000, maxTokens: 3000},
		{name: "models.json provider whose model has only an id", provider: "bare", sibling: "bare-1", window: 128000, maxTokens: 16384,
			config: `{"providers":{"bare":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"unused","api":"openai-completions","models":[{"id":"bare-1"}]}}}`},
		{name: "models.json base URL of a built-in provider", provider: "mistral", sibling: defaultOf("mistral"),
			config: `{"providers":{"mistral":{"baseUrl":"http://127.0.0.1:1/mistral"}}}`},
		{name: "models.json override of a built-in provider's default model", provider: "cerebras", sibling: defaultOf("cerebras"), window: 12345, maxTokens: 678,
			config: `{"providers":{"cerebras":{"modelOverrides":{"` + defaultOf("cerebras") + `":{"contextWindow":12345,"maxTokens":678}}}}}`},
		{name: "built-in API key provider", provider: "openai", sibling: defaultOf("openai")},
		{name: "built-in OAuth provider", provider: "github-copilot", sibling: defaultOf("github-copilot")},
		{name: "built-in OAuth subscription provider", provider: "openai-codex", sibling: defaultOf("openai-codex")},
	}
	for _, tc := range cases {
		if tc.provider == "mistral" && pigstrip.Has(pigstrip.ListAPIs, "mistral-conversations") {
			continue // a Piglet that strips the Mistral API has no mistral provider to copy a model from
		}
		t.Run(tc.name, func(t *testing.T) {
			dir := isolateProviderAuthEnv(t)
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			services := testServices(t, dir)
			t.Cleanup(services.Close)
			if tc.register != nil {
				tc.register(t, services)
			}
			if tc.sibling == "" {
				t.Fatalf("provider %s has no default model in DefaultModelPerProvider", tc.provider)
			}
			sibling, _, warning, err := resolveModel(tc.provider+"/"+tc.sibling, "", codingagent.Settings{}, services)
			if err != nil || sibling == nil || warning != "" {
				t.Fatalf("the provider's own model %s/%s: model %v, warning %q, error %v", tc.provider, tc.sibling, sibling, warning, err)
			}
			custom, _, warning, err := resolveModel(tc.provider+"/nosuch", "", codingagent.Settings{}, services)
			if err != nil || custom == nil {
				t.Fatalf("custom id %s/nosuch: model %v, error %v", tc.provider, custom, err)
			}
			if want := `Model "nosuch" not found for provider "` + tc.provider + `". Using custom model id.`; warning != want {
				t.Errorf("warning = %q, want %q", warning, want)
			}
			if custom.ID != "nosuch" || custom.DisplayName != "nosuch" || custom.ProviderMeta.ProviderID != tc.provider {
				t.Errorf("custom model is %s/%s named %q, want %s/nosuch named nosuch", custom.ProviderMeta.ProviderID, custom.ID, custom.DisplayName, tc.provider)
			}
			want, got := customModelFields(t, sibling), customModelFields(t, custom)
			if tc.window != 0 {
				want["contextWindow"], want["maxTokens"] = fmt.Sprint(tc.window), fmt.Sprint(tc.maxTokens)
			}
			for _, field := range []string{"contextWindow", "maxTokens", "cost", "input", "images", "api", "baseUrl", "compat", "reasoning", "maxThinking", "thinkingLevelMap", "type"} {
				if got[field] != want[field] {
					t.Errorf("%s of the custom id = %s, want the copied model's %s", field, got[field], want[field])
				}
			}
			if custom.Provider == nil {
				t.Error("the custom model has no provider to send its requests")
			}
		})
	}
}
