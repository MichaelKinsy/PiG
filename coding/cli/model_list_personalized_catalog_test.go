package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Issue #260. Pi's listModels (cli/list-models.ts) prints exactly modelRuntime.getAvailable(). An OpenRouter key's saved personalized
// catalog (model-runtime.ts:create, withPersonalizedCatalog) replaces the bundled OpenRouter list, so a bundled model the saved list
// does not carry is not listed. pig used to add every bundled model of an authenticated provider back after the registry's own list,
// so `pig update --models` followed by `pig --list-models` still showed models Pi had dropped.
func TestPrintModelList_PersonalizedOpenRouterCatalogReplacesBundledModels(t *testing.T) {
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "x")
	dir := t.TempDir()

	bundled := ai.ListModels("openrouter")
	if len(bundled) < 2 {
		t.Fatalf("bundled openrouter catalog has %d models, need at least 2", len(bundled))
	}
	saved := &ai.Model{
		ID: "vendor/personal-only", DisplayName: "Personal only", Input: []string{"text"},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "openrouter", API: "openai-completions", BaseURL: "https://openrouter.ai/api/v1"},
		Capabilities: ai.ModelCapabilities{ContextWindow: 64000, MaxOutputTokens: 8000},
	}
	store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
	if err := store.Write(t.Context(), "openrouter", ai.ModelsStoreEntry{Models: []ai.AnyModel{saved}, CheckedAt: new(float64(1))}); err != nil {
		t.Fatal(err)
	}

	// CLI startup restores the saved catalogs before listing: coding.CreateModelRuntime refreshes offline on create.
	registry := codingagent.NewModelRegistry(dir)
	if result := registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); len(result.Errors) != 0 {
		t.Fatalf("offline restore errors = %v", result.Errors)
	}
	out := captureStdout(func() { printModelList(registry, "") })

	if !strings.Contains(out, "vendor/personal-only") {
		t.Fatalf("--list-models omits the saved personalized model:\n%s", out)
	}
	for _, model := range bundled {
		if strings.Contains(out, " "+model.ID+" ") {
			t.Fatalf("--list-models lists bundled model %q that the saved personalized catalog dropped:\n%s", model.ID, out)
		}
	}
}
