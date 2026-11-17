package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// D199 (docs/parity/divergences/D199.md): "A variant is a copy of its base entry in everything but its id, name and JSON record: API, base URL, prices, limits, input types, reasoning and thinking levels are the base model's."
// Pi lists no routing variant, so the record is the oracle. A user whose saved OpenRouter list is personalized (the models, limits and
// prices of the key) selects the variant at startup and gets the saved base record's window and prices, not the bundled record's or the provider's default.
func TestResolveModel_OpenRouterRoutingVariantKeepsItsSavedBaseRecordLimits(t *testing.T) {
	const bundledBase = "openai/gpt-5.2"
	if _, ok := ai.LookupModelExact("openrouter/" + bundledBase); !ok {
		t.Fatalf("the bundled catalog has no openrouter/%s", bundledBase)
	}
	record := `{"models":[` +
		`{"id":"vendor/base","name":"Vendor Base","api":"openai-completions","provider":"openrouter","baseUrl":"https://openrouter.ai/api/v1","reasoning":false,"input":["text"],"cost":{"input":7,"output":14,"cacheRead":0,"cacheWrite":0},"contextWindow":4321,"maxTokens":321},` +
		`{"id":"` + bundledBase + `","name":"Saved GPT","api":"openai-completions","provider":"openrouter","baseUrl":"https://openrouter.ai/api/v1","reasoning":false,"input":["text"],"cost":{"input":3,"output":6,"cacheRead":0,"cacheWrite":0},"contextWindow":1234,"maxTokens":123}]}`
	cases := []struct {
		id            string
		window        int
		input, output float64
	}{
		{"vendor/base", 4321, 7, 14},
		{"vendor/base:nitro", 4321, 7, 14},
		{"vendor/base:floor", 4321, 7, 14},
		{bundledBase, 1234, 3, 6},
		{bundledBase + ":nitro", 1234, 3, 6},
		{bundledBase + ":floor", 1234, 3, 6},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			dir := t.TempDir()
			agentDirForModelOverride = dir
			t.Cleanup(func() { agentDirForModelOverride = "" })
			t.Setenv("PIG_HOME", t.TempDir())
			t.Setenv("PI_OFFLINE", "")
			var entry ai.ModelsStoreEntry
			if err := json.Unmarshal([]byte(record), &entry); err != nil {
				t.Fatal(err)
			}
			entry.CheckedAt = new(float64(1))
			if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(t.Context(), "openrouter", entry); err != nil {
				t.Fatal(err)
			}
			model, _, warning, err := resolveModel("openrouter/"+tc.id, "", codingagent.Settings{}, testServices(t, dir))
			if err != nil {
				t.Fatalf("resolveModel: %v", err)
			}
			if warning != "" {
				t.Errorf("warning = %q, want none for a catalogued model", warning)
			}
			if model.ID != tc.id || model.Capabilities.ContextWindow != tc.window || model.CostRates().Input != tc.input || model.CostRates().Output != tc.output {
				t.Errorf("model %s window=%d rates=%v/%v, want %s window=%d rates=%v/%v", model.ID, model.Capabilities.ContextWindow, model.CostRates().Input, model.CostRates().Output,
					tc.id, tc.window, tc.input, tc.output)
			}
		})
	}
}
