//go:build !race

package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// D199: OpenRouter's routing variants are held entries, so the metadata the model resolver reads is not rebuilt per variant. A RuntimeModels call lists
// every variant and allocates less than once per two models, as it did before the variants existed (771 times for 2398 models; 2998 when each variant
// built its id, name and id list).
func TestRuntimeModelsAllocatesNothingPerRoutingVariant(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	models := registry.RuntimeModels()
	variants := 0
	for _, model := range models {
		if _, _, ok := ai.OpenRouterRoutingVariant(model.Provider, model.ID); ok {
			variants++
		}
	}
	if variants < 100 {
		t.Fatalf("%d routing variants listed; the guard needs enough of them that a per-variant cost shows", variants)
	}
	allocs := testing.AllocsPerRun(5, func() { _ = registry.RuntimeModels() })
	if allocs > float64(len(models))/2 {
		t.Errorf("RuntimeModels allocates %v times for %d routing variants among %d models, want fewer than one per two models", allocs, variants, len(models))
	}
}

// The model selector reads one model by provider and id (modelSelectorRuntime.GetModel) once per scoped model, so the read builds that model and not the
// catalog, which D199 grows by every OpenRouter routing variant. It answers what the composed catalog lists, variants included.
func TestSelectorModelLookupBuildsOnlyTheModelRead(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	runtime := modelSelectorRuntime{registry: registry}
	catalog := registry.GetAllModelData()
	for i := 0; i < len(catalog); i += 37 {
		want := catalog[i]
		got := runtime.GetModel(want.ProviderID(), want.ModelID())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("GetModel(%s, %s) = %+v, want the catalog's %+v", want.ProviderID(), want.ModelID(), got, want)
		}
	}
	variants := 0
	for _, want := range catalog {
		if _, _, ok := ai.OpenRouterRoutingVariant(want.ProviderID(), want.ModelID()); ok {
			if variants++; variants%11 != 0 {
				continue
			}
			if got := runtime.GetModel(want.ProviderID(), want.ModelID()); !reflect.DeepEqual(got, want) {
				t.Fatalf("GetModel of the variant %s = %+v, want %+v", want.ModelID(), got, want)
			}
		}
	}
	if variants == 0 {
		t.Fatal("the catalog lists no routing variant")
	}
	for _, missing := range [][2]string{{"openrouter", "no/such-model:nitro"}, {"no-such-provider", catalog[0].ModelID()}, {catalog[0].ProviderID(), "no-such-model"}} {
		if got := runtime.GetModel(missing[0], missing[1]); got != nil {
			t.Fatalf("GetModel(%s, %s) = %+v, want nil", missing[0], missing[1], got)
		}
	}
	provider, id := catalog[0].ProviderID(), catalog[0].ModelID()
	if allocs := testing.AllocsPerRun(5, func() { _ = runtime.GetModel(provider, id) }); allocs > 200 {
		t.Errorf("GetModel allocates %v times for one model of a %d model catalog", allocs, len(catalog))
	}
}
