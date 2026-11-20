package codingagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// provider-composer.ts:212-261,336-342 (applyModelsJson -> findModelDefaults -> modelFromJson)
// and model-registry.test.ts:234-257 ("built-in provider custom models inherit api and baseUrl without explicit fields"):
// Resolve must fill omitted api and/or baseUrl on a models.json custom model from the same composed provider defaults
// that GetProviderModelDataByID / ModelRuntime.GetModel produces.
func TestResolveCustomModelUnderBuiltInProviderInheritsDefaults(t *testing.T) {
	generatedAt := ai.GetBuiltinModelDataGeneratedAt()
	if generatedAt == nil {
		t.Fatal("GetBuiltinModelDataGeneratedAt() == nil")
	}
	newer := *generatedAt + 1000

	cases := []struct {
		name       string
		provider   string
		modelID    string
		modelsJSON string
		storeJSON  string
		extension  *extension.ProviderConfig
		wantAPI    string
		wantBase   string
		wantWin    int
	}{
		{
			name:       "built-in api-key provider omitting api and baseUrl",
			provider:   "groq",
			modelID:    "custom-groq-model",
			modelsJSON: `{"providers":{"groq":{"models":[{"id":"custom-groq-model","name":"Custom Groq","contextWindow":64000,"maxTokens":8192}]}}}`,
			wantAPI:    "openai-completions",
			wantBase:   "https://api.groq.com/openai/v1",
			wantWin:    64000,
		},
		{
			name:       "built-in oauth multi-api provider prefers openai-completions default",
			provider:   "github-copilot",
			modelID:    "custom-copilot-model",
			modelsJSON: `{"providers":{"github-copilot":{"models":[{"id":"custom-copilot-model","name":"Custom Copilot","contextWindow":200000,"maxTokens":16384}]}}}`,
			wantAPI:    "openai-completions",
			wantBase:   "https://api.individual.githubcopilot.com",
			wantWin:    200000,
		},
		{
			name:       "built-in multi-api provider omitting both picks openai-completions and its baseUrl",
			provider:   "opencode-go",
			modelID:    "custom-og-default",
			modelsJSON: `{"providers":{"opencode-go":{"models":[{"id":"custom-og-default","name":"Custom OG"}]}}}`,
			wantAPI:    "openai-completions",
			wantBase:   "https://opencode.ai/inference/go/openai/v1",
			wantWin:    128000,
		},
		{
			name:       "built-in multi-api provider with explicit api inherits matching api baseUrl",
			provider:   "opencode-go",
			modelID:    "custom-og-anth",
			modelsJSON: `{"providers":{"opencode-go":{"models":[{"id":"custom-og-anth","api":"anthropic-messages"}]}}}`,
			wantAPI:    "anthropic-messages",
			wantBase:   "https://opencode.ai/inference/go/anthropic",
			wantWin:    128000,
		},
		{
			name:       "built-in single non-openai api provider falls back to first chat model",
			provider:   "anthropic",
			modelID:    "custom-claude",
			modelsJSON: `{"providers":{"anthropic":{"models":[{"id":"custom-claude"}]}}}`,
			wantAPI:    "anthropic-messages",
			wantBase:   "https://api.anthropic.com",
			wantWin:    128000,
		},
		{
			name:       "built-in provider with provider baseUrl and omitted api",
			provider:   "groq",
			modelID:    "custom-groq-proxy",
			modelsJSON: `{"providers":{"groq":{"baseUrl":"https://proxy.example/v1","models":[{"id":"custom-groq-proxy"}]}}}`,
			wantAPI:    "openai-completions",
			wantBase:   "https://proxy.example/v1",
			wantWin:    128000,
		},
		{
			name:       "explicit api and baseUrl in models.json win unchanged",
			provider:   "groq",
			modelID:    "custom-groq-explicit",
			modelsJSON: `{"providers":{"groq":{"models":[{"id":"custom-groq-explicit","api":"openai-responses","baseUrl":"https://explicit.example/v1","contextWindow":32000}]}}}`,
			wantAPI:    "openai-responses",
			wantBase:   "https://explicit.example/v1",
			wantWin:    32000,
		},
		{
			name:       "store entry with same id supplies omitted api and baseUrl while models.json fields win",
			provider:   "groq",
			modelID:    "zz-store-and-models-json",
			storeJSON:  `{"id":"zz-store-and-models-json","name":"Store Name","api":"openai-responses","provider":"groq","baseUrl":"https://store.example/v1","reasoning":true,"input":["text","image"],"cost":{"input":2,"output":8,"cacheRead":0,"cacheWrite":0},"contextWindow":500000,"maxTokens":32000}`,
			modelsJSON: `{"providers":{"groq":{"models":[{"id":"zz-store-and-models-json","name":"ModelsJSON Name","contextWindow":250000,"maxTokens":16000}]}}}`,
			wantAPI:    "openai-responses",
			wantBase:   "https://store.example/v1",
			wantWin:    250000,
		},
		{
			name:       "extension baseUrl overlay plus models.json custom model omitting api and baseUrl",
			provider:   "groq",
			modelID:    "custom-groq-ext-base",
			modelsJSON: `{"providers":{"groq":{"models":[{"id":"custom-groq-ext-base"}]}}}`,
			extension:  &extension.ProviderConfig{BaseURL: "https://ext-overlay.example/v1"},
			wantAPI:    "openai-completions",
			wantBase:   "https://ext-overlay.example/v1",
			wantWin:    128000,
		},
		{
			// provider-composer.ts:347-349,555-560: applyExtension runs after applyModelsJson and replaces
			// baseUrl on every model when !extension.models, matching GetProviderModelDataByID.
			name:       "extension baseUrl overlay overrides explicit models.json custom model baseUrl",
			provider:   "groq",
			modelID:    "custom-groq-ext-override",
			modelsJSON: `{"providers":{"groq":{"models":[{"id":"custom-groq-ext-override","api":"openai-responses","baseUrl":"https://models-json.example/v1","contextWindow":48000}]}}}`,
			extension:  &extension.ProviderConfig{BaseURL: "https://ext-overlay.example/v1"},
			wantAPI:    "openai-responses",
			wantBase:   "https://ext-overlay.example/v1",
			wantWin:    48000,
		},
		{
			// provider-composer.ts:289,350: when extension.models is set, applyExtension replaces the list via
			// extensionModelFromDefinition (definition.baseUrl ?? config.baseUrl ?? defaults?.baseUrl), so a
			// model's own baseUrl is not overwritten by extension.BaseURL (matching GetProviderModelDataByID).
			name:       "extension with its own models keeps model baseUrl unchanged",
			provider:   "groq",
			modelID:    "custom-groq-ext-owned",
			modelsJSON: `{"providers":{"groq":{"models":[{"id":"custom-groq-ext-owned","api":"openai-responses","baseUrl":"https://models-json.example/v1","contextWindow":48000}]}}}`,
			extension: &extension.ProviderConfig{
				BaseURL: "https://ext-provider.example/v1",
				Models: []extension.ProviderModelConfig{
					{
						ID:            "custom-groq-ext-owned",
						API:           "openai-responses",
						BaseURL:       "https://ext-model.example/v1",
						ContextWindow: 96000,
						MaxTokens:     8192,
					},
				},
			},
			wantAPI:  "openai-responses",
			wantBase: "https://ext-model.example/v1",
			wantWin:  96000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(tc.modelsJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.storeJSON != "" {
				var entry ai.ModelsStoreEntry
				if err := json.Unmarshal([]byte(`{"models":[`+tc.storeJSON+`]}`), &entry); err != nil {
					t.Fatal(err)
				}
				entry.CheckedAt = new(1.0)
				entry.LastModified = &newer
				if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(context.Background(), tc.provider, entry); err != nil {
					t.Fatal(err)
				}
			}
			registry := NewModelRegistry(dir)
			if tc.storeJSON != "" {
				registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{})
			}
			if tc.extension != nil {
				if err := registry.RegisterExtensionProvider(tc.provider, *tc.extension); err != nil {
					t.Fatal(err)
				}
			}
			if registry.LoadError() != "" {
				t.Fatalf("unexpected load error: %s", registry.LoadError())
			}
			composed := registry.GetProviderModelDataByID(tc.provider, tc.modelID)
			if composed == nil {
				t.Fatalf("GetProviderModelDataByID(%s, %s) = nil", tc.provider, tc.modelID)
			}
			entry, ok := registry.Resolve(tc.provider, tc.modelID)
			if !ok {
				t.Fatalf("Resolve(%s, %s) ok = false", tc.provider, tc.modelID)
			}
			if entry.API != tc.wantAPI || entry.BaseURL != tc.wantBase {
				t.Errorf("Resolve(%s, %s) api=%q baseUrl=%q, want api=%q baseUrl=%q (GetProviderModelDataByID has api=%q baseUrl=%q)",
					tc.provider, tc.modelID, entry.API, entry.BaseURL, tc.wantAPI, tc.wantBase, composed.ProviderMeta.API, composed.ProviderMeta.BaseURL)
			}
			if string(composed.ProviderMeta.API) != entry.API || composed.ProviderMeta.BaseURL != entry.BaseURL {
				t.Errorf("Resolve(%s, %s) api=%q baseUrl=%q differs from GetProviderModelDataByID api=%q baseUrl=%q",
					tc.provider, tc.modelID, entry.API, entry.BaseURL, composed.ProviderMeta.API, composed.ProviderMeta.BaseURL)
			}
			if entry.ContextWindow != tc.wantWin {
				t.Errorf("Resolve(%s, %s) contextWindow=%d, want %d", tc.provider, tc.modelID, entry.ContextWindow, tc.wantWin)
			}
		})
	}
}
