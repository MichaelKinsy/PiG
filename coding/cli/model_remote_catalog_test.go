package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The startup model resolves through the same catalog ModelRuntime.getModel reads: a model only the remote catalog carries keeps its window and prices
// and is not rebuilt from the provider's default model.
func TestResolveModel_RemoteOnlyCatalogModelKeepsItsLimits(t *testing.T) {
	dir := t.TempDir()
	agentDirForModelOverride = dir
	t.Cleanup(func() { agentDirForModelOverride = "" })
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PI_OFFLINE", "")
	generatedAt := ai.GetBuiltinModelDataGeneratedAt()
	if generatedAt == nil {
		t.Fatal("the generated catalogs record no generation time")
	}
	var entry ai.ModelsStoreEntry
	record := `{"models":[{"id":"claude-fable-5.2","name":"Claude Fable 5.2","api":"anthropic-messages","provider":"github-copilot","baseUrl":"https://api.individual.githubcopilot.com","reasoning":true,"input":["text","image"],"cost":{"input":10,"output":50,"cacheRead":1,"cacheWrite":12.5},"contextWindow":1000000,"maxTokens":128000}]}`
	if err := json.Unmarshal([]byte(record), &entry); err != nil {
		t.Fatal(err)
	}
	entry.CheckedAt, entry.LastModified = new(float64(1)), new(*generatedAt+1)
	if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(t.Context(), "github-copilot", entry); err != nil {
		t.Fatal(err)
	}
	model, _, warning, err := resolveModel("github-copilot/claude-fable-5.2", "", codingagent.Settings{}, testServices(t, dir))
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want none for a catalogued model", warning)
	}
	if model.ID != "claude-fable-5.2" || model.Capabilities.ContextWindow != 1_000_000 || model.CostRates().Input != 10 {
		t.Errorf("model %s window=%d input rate=%v, want claude-fable-5.2 with window 1000000 and input rate 10", model.ID, model.Capabilities.ContextWindow, model.CostRates().Input)
	}
}
