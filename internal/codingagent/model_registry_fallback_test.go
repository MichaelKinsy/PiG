package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestResolveFallbackModelKeepsTheModelAndResolvesTheProvidersConnection pins the entry of a model id no catalog carries, for every shape of
// provider: the model members are those of the copy the CLI resolver made of one of the provider's models (model-resolver.ts:302-313
// buildFallbackModel `{ ...baseModel, id: modelId, name: modelId }`), and the key and request headers are the provider's. Pi 1.5.2 sends
// such a request with the copied model's output limit and compat flags and with the provider's headers, not the copied model's own.
func TestResolveFallbackModelKeepsTheModelAndResolvesTheProvidersConnection(t *testing.T) {
	registeredModels := []extension.ProviderModelConfig{{ID: "known", Name: "Known", Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100, Headers: map[string]string{"X-Model-H": "m"}}}
	cases := []struct {
		name     string
		config   string
		env      map[string]string
		register *extension.ProviderConfig
		provider string
		api      ai.API
		// baseURL is the copy's base URL; wantBaseURL the entry's when the provider's settings replace it.
		baseURL, wantBaseURL string
		wantKey              string
		wantHeaders          map[string]string
	}{
		{name: "registration with its own models on openai-completions", provider: "registered", api: ai.APIOpenAICompletions, baseURL: "http://127.0.0.1:1/v1", wantKey: "registered-key", wantHeaders: map[string]string{"X-Prov-H": "p"},
			register: &extension.ProviderConfig{BaseURL: "http://127.0.0.1:1/v1", APIKey: "registered-key", API: ai.APIOpenAICompletions, Headers: map[string]string{"X-Prov-H": "p"}, Models: registeredModels}},
		{name: "registration with its own models on openai-responses", provider: "registered", api: ai.APIOpenAIResponses, baseURL: "http://127.0.0.1:1/v1", wantKey: "registered-key", wantHeaders: map[string]string{"X-Prov-H": "p"},
			register: &extension.ProviderConfig{BaseURL: "http://127.0.0.1:1/v1", APIKey: "registered-key", API: ai.APIOpenAIResponses, Headers: map[string]string{"X-Prov-H": "p"}, Models: registeredModels}},
		{name: "registration of a built-in provider's base URL and key", provider: "groq", api: ai.APIOpenAICompletions, baseURL: "http://127.0.0.1:1/groq", wantKey: "groq-key",
			register: &extension.ProviderConfig{BaseURL: "http://127.0.0.1:1/groq", APIKey: "groq-key"}},
		{name: "models.json provider on openai-completions", provider: "configured", api: ai.APIOpenAICompletions, baseURL: "http://127.0.0.1:1/v1", wantKey: "configured-key", wantHeaders: map[string]string{"X-Prov-H": "p"},
			config: `{"providers":{"configured":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"configured-key","api":"openai-completions","headers":{"X-Prov-H":"p"},"models":[{"id":"known","headers":{"X-Model-H":"m"}}]}}}`},
		{name: "models.json provider on openai-responses", provider: "configured", api: ai.APIOpenAIResponses, baseURL: "http://127.0.0.1:1/v1", wantKey: "configured-key", wantHeaders: map[string]string{"X-Prov-H": "p"},
			config: `{"providers":{"configured":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"configured-key","api":"openai-responses","headers":{"X-Prov-H":"p"},"models":[{"id":"known","headers":{"X-Model-H":"m"}}]}}}`},
		{name: "models.json base URL and key of a built-in provider", provider: "mistral", api: ai.APIOpenAICompletions, baseURL: "https://catalog.invalid/v1", wantBaseURL: "http://127.0.0.1:1/mistral", wantKey: "mistral-key",
			config: `{"providers":{"mistral":{"baseUrl":"http://127.0.0.1:1/mistral","apiKey":"mistral-key"}}}`},
		{name: "built-in provider with an environment key", provider: "openai", api: ai.APIOpenAIResponses, baseURL: "https://catalog.invalid/v1", wantKey: "environment-key",
			env: map[string]string{"OPENAI_API_KEY": "environment-key"}},
		{name: "built-in OAuth provider without credentials", provider: "openai-codex", api: ai.APIOpenAIResponses, baseURL: "https://catalog.invalid/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			for _, name := range []string{"OPENAI_API_KEY", "GROQ_API_KEY", "MISTRAL_API_KEY"} {
				t.Setenv(name, tc.env[name])
			}
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			registry := NewModelRegistry(dir)
			if tc.register != nil {
				if err := registry.RegisterExtensionProvider(tc.provider, *tc.register); err != nil {
					t.Fatal(err)
				}
			}
			fallback := &ai.Model{ID: "nosuch", DisplayName: "nosuch", Input: []string{"text", "image"},
				Capabilities: ai.ModelCapabilities{ContextWindow: 48000, MaxOutputTokens: 3000, SupportsImages: true, InputCostPer1M: 3, OutputCostPer1M: 4, CacheReadCostPer1M: 0.3, CacheWriteCostPer1M: 0.4},
				ProviderMeta: ai.ProviderMetadata{ProviderID: tc.provider, API: tc.api, BaseURL: tc.baseURL, Reasoning: true, Compat: &ai.OpenAICompat{SupportsDeveloperRole: new(false), MaxTokensField: "max_tokens"}}}
			entry := registry.ResolveFallbackModel(fallback)

			wantBaseURL := tc.wantBaseURL
			if wantBaseURL == "" {
				wantBaseURL = tc.baseURL
			}
			type members struct {
				Provider, ID, Name, API, BaseURL string
				Window, MaxTokens                int
				Input                            []string
				Rates                            [4]float64
				Reasoning                        bool
				MaxTokensField                   string
				DeveloperRole                    bool
			}
			got := members{Provider: entry.ProviderID, ID: entry.ModelID, Name: entry.DisplayName, API: entry.API, BaseURL: entry.BaseURL, Window: entry.ContextWindow, MaxTokens: entry.MaxTokens,
				Input: entry.Input, Rates: [4]float64{entry.InputCost, entry.OutputCost, entry.CacheReadCost, entry.CacheWriteCost}, Reasoning: entry.Reasoning}
			if entry.Compat != nil {
				got.MaxTokensField = entry.Compat.MaxTokensField
				got.DeveloperRole = entry.Compat.SupportsDeveloperRole == nil || *entry.Compat.SupportsDeveloperRole
			} else {
				got.DeveloperRole = true
			}
			want := members{Provider: tc.provider, ID: "nosuch", Name: "nosuch", API: string(tc.api), BaseURL: wantBaseURL, Window: 48000, MaxTokens: 3000,
				Input: []string{"text", "image"}, Rates: [4]float64{3, 4, 0.3, 0.4}, Reasoning: true, MaxTokensField: "max_tokens"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("model members of the entry:\n got %+v\nwant %+v", got, want)
			}
			if entry.APIKey != tc.wantKey {
				t.Errorf("key = %q, want the provider's %q", entry.APIKey, tc.wantKey)
			}
			if len(entry.Headers) != len(tc.wantHeaders) || (len(tc.wantHeaders) > 0 && !reflect.DeepEqual(entry.Headers, tc.wantHeaders)) {
				t.Errorf("request headers = %v, want the provider's %v and none of another model's", entry.Headers, tc.wantHeaders)
			}
		})
	}
}
