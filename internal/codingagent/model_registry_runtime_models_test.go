package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func referenceRuntimeModels(registry *ModelRegistry) []RuntimeModel {
	models := registry.GetAllModelData()
	result := make([]RuntimeModel, 0, len(models))
	for _, model := range models {
		result = append(result, RuntimeModel{Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != "", Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)})
	}
	return result
}

func TestRuntimeModelsProjectsOnlySelectionMetadata(t *testing.T) {
	// Pi's model-resolver reads provider/id/name/reasoning/headers from the catalog; it does not copy request capabilities or compatibility data to select a model.
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	if got, want := registry.RuntimeModels(), referenceRuntimeModels(registry); !reflect.DeepEqual(got, want) {
		t.Fatal("selection metadata changed from the full composed catalog")
	}
	baseline := testing.AllocsPerRun(5, func() { _ = referenceRuntimeModels(registry) })
	projected := testing.AllocsPerRun(5, func() { _ = registry.RuntimeModels() })
	if projected >= baseline/2 {
		t.Fatalf("selection projection allocates %.0f times; full model conversion allocates %.0f", projected, baseline)
	}
}

func TestRuntimeModelsPreservesOverlaysAndLiveNativeCatalogs(t *testing.T) {
	dir := t.TempDir()
	base := ai.GeneratedModels[0]
	config := map[string]any{"providers": map[string]any{base.Provider: map[string]any{
		"modelOverrides": map[string]any{base.ID: map[string]any{"name": "selection override", "reasoning": false, "headers": map[string]string{"X-Selection": "override"}}},
	}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	t.Cleanup(registry.CloseModelTasks)
	assertProjection := func() {
		t.Helper()
		if got, want := registry.RuntimeModels(), referenceRuntimeModels(registry); !reflect.DeepEqual(got, want) {
			t.Fatal("selection metadata changed from the composed catalog")
		}
	}
	assertProjection()
	for _, models := range [][]extension.ProviderModelConfig{nil, {}, {{ID: "replacement", Name: "dynamic selection", Reasoning: true}}} {
		input := extension.ProviderConfig{BaseURL: "https://selection.invalid/v1", API: ai.APIOpenAICompletions, APIKey: "fixture", Models: models}
		if err := registry.RegisterExtensionProvider(base.Provider, input); err != nil {
			t.Fatal(err)
		}
		assertProjection()
		registry.UnregisterProvider(base.Provider)
		assertProjection()
	}
	model := &ai.Model{ID: "native-first", ProviderMeta: ai.ProviderMetadata{ProviderID: "selection-native", Headers: map[string]string{"X-Native": "owned"}}}
	reads := 0
	if err := registry.RegisterNativeModelsProvider(&ai.ModelsProvider{ID: "selection-native", GetModels: func() ([]*ai.Model, error) {
		reads++
		return []*ai.Model{model}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	assertProjection()
	before := reads
	projected := registry.RuntimeModels()
	if reads != before+1 {
		t.Fatalf("native catalog callback reads = %d, want one fresh read", reads-before)
	}
	for _, entry := range projected {
		if entry.Provider == "selection-native" {
			*entry.Headers["X-Native"] = "changed by consumer"
		}
	}
	if model.ProviderMeta.Headers["X-Native"] != "owned" {
		t.Fatal("projection shares mutable native headers")
	}
	model.ID = "native-replacement"
	assertProjection()
}

func BenchmarkRuntimeModelsSelection(b *testing.B) {
	registry := NewModelRegistry(b.TempDir())
	b.Cleanup(registry.CloseModelTasks)
	b.ReportAllocs()
	for b.Loop() {
		if len(registry.RuntimeModels()) == 0 {
			b.Fatal("empty catalog")
		}
	}
}
