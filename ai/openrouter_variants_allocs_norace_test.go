//go:build !race

package ai

import "testing"

// D199: the routing variants are derived on demand, so listing the catalog costs the lists it builds and never a cache key, boxed lookup or copy
// per variant. The held variants are warm after the first list; a list of N OpenRouter models then allocates its two result slices, not 2N cache keys.
func TestListingOpenRouterRoutingVariantsAllocatesNothingPerVariant(t *testing.T) {
	bases := 0
	for _, model := range GeneratedModels {
		if isOpenRouterBase(&model) {
			bases++
		}
	}
	if bases < 100 {
		t.Fatalf("%d OpenRouter models bundled; the guard needs enough of them that a per-variant cost shows", bases)
	}
	for _, provider := range []string{"openrouter", ""} {
		_ = ListModels(provider)
		if allocs := testing.AllocsPerRun(5, func() { _ = ListModels(provider) }); allocs > 4 {
			t.Errorf("ListModels(%q) allocates %v times for %d OpenRouter models, want only its result lists", provider, allocs, bases)
		}
	}
}

// Deriving a variant builds its id, name and record, and nothing around them: the entry is an element of the one held list.
func TestDerivingOpenRouterRoutingVariantsAllocatesOnlyTheEntries(t *testing.T) {
	models := ListModels("openrouter")
	variants := 0
	for i := range models {
		if _, _, ok := OpenRouterRoutingVariant(models[i].Provider, models[i].ID); ok {
			variants++
		}
	}
	resetDerivedVariants(t)
	allocs := testing.AllocsPerRun(1, func() {
		derivedVariants.Lock()
		derivedVariants.bundle.Store(nil)
		derivedVariants.byKey, derivedVariants.bySpec = nil, nil
		derivedVariants.Unlock()
		_ = ListModels("openrouter")
	})
	if perVariant := allocs / float64(variants); perVariant > 4 {
		t.Errorf("deriving %d variants allocates %v times, %.1f per variant, want at most 4 (id, name, record)", variants, allocs, perVariant)
	}
}
