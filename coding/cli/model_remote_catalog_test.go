package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The startup model resolves through the same catalog ModelRuntime.getModel reads: a model only the remote catalog carries keeps its window and prices
// and is not rebuilt from the provider's default model. The shared resolver is exercised for providers of different APIs and endpoints.
func TestResolveModel_RemoteOnlyCatalogModelKeepsItsLimits(t *testing.T) {
	cases := []struct {
		name, provider, id, record string
		api                        ai.API
		window                     int
		input, output              float64
	}{
		{
			name: "anthropic-messages through an OAuth provider", provider: "github-copilot", id: "zz-fixture-claude-fable-6", api: "anthropic-messages", window: 1_000_000, input: 10, output: 50,
			record: `{"models":[{"id":"zz-fixture-claude-fable-6","name":"Claude Fable 6","api":"anthropic-messages","provider":"github-copilot","baseUrl":"https://api.individual.githubcopilot.com","reasoning":true,"input":["text","image"],"cost":{"input":10,"output":50,"cacheRead":1,"cacheWrite":12.5},"contextWindow":1000000,"maxTokens":128000}]}`,
		},
		{
			name: "openai-responses through an API-key provider", provider: "openai", id: "zz-fixture-gpt-fable-9", api: "openai-responses", window: 777_000, input: 4, output: 16,
			record: `{"models":[{"id":"zz-fixture-gpt-fable-9","name":"GPT Fable 9","api":"openai-responses","provider":"openai","baseUrl":"https://api.openai.com/v1","reasoning":true,"input":["text"],"cost":{"input":4,"output":16,"cacheRead":0.4,"cacheWrite":0},"contextWindow":777000,"maxTokens":64000}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			dir := t.TempDir()
			agentDirForModelOverride = dir
			t.Cleanup(func() { agentDirForModelOverride = "" })
			t.Setenv("PIG_HOME", t.TempDir())
			t.Setenv("PI_OFFLINE", "")
			generatedAt := ai.GetBuiltinModelDataGeneratedAt()
			if generatedAt == nil {
				t.Fatal("the generated catalogs record no generation time")
			}
			if _, bundled := ai.LookupModelExact(tc.provider + "/" + tc.id); bundled {
				t.Fatalf("%s/%s is in the bundled catalog: the model must be one only the remote catalog carries", tc.provider, tc.id)
			}
			var entry ai.ModelsStoreEntry
			if err := json.Unmarshal([]byte(tc.record), &entry); err != nil {
				t.Fatal(err)
			}
			entry.CheckedAt, entry.LastModified = new(float64(1)), new(*generatedAt+1)
			if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(t.Context(), tc.provider, entry); err != nil {
				t.Fatal(err)
			}
			model, _, warning, err := resolveModel(tc.provider+"/"+tc.id, "", codingagent.Settings{}, testServices(t, dir))
			if err != nil {
				t.Fatalf("resolveModel: %v", err)
			}
			if warning != "" {
				t.Errorf("warning = %q, want none for a catalogued model", warning)
			}
			if model.ID != tc.id || model.ProviderMeta.API != tc.api || model.Capabilities.ContextWindow != tc.window || model.CostRates().Input != tc.input || model.CostRates().Output != tc.output {
				t.Errorf("model %s api=%s window=%d rates=%v/%v, want %s api=%s window=%d rates=%v/%v", model.ID, model.ProviderMeta.API, model.Capabilities.ContextWindow, model.CostRates().Input, model.CostRates().Output,
					tc.id, tc.api, tc.window, tc.input, tc.output)
			}
		})
	}
}
