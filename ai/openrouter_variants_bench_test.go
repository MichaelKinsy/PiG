package ai

import "testing"

// BenchmarkLookupOpenRouterVariant is the lookup of a routing variant the catalog does not store: the first call derives it from its
// base entry, the rest return the cached entry.
func BenchmarkLookupOpenRouterVariant(b *testing.B) {
	base := ListModels("openrouter")[0]
	spec := "openrouter/" + base.ID + ":nitro"
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := LookupModelExact(spec); !ok {
			b.Fatal("no variant")
		}
	}
}

// BenchmarkOpenRouterVariant is the derivation of one variant from its base entry.
func BenchmarkOpenRouterVariant(b *testing.B) {
	base := ListModels("openrouter")[0]
	b.ReportAllocs()
	for b.Loop() {
		_ = base.OpenRouterVariant("nitro")
	}
}

// BenchmarkListOpenRouterModels is the list of the OpenRouter catalog with its derived variants.
func BenchmarkListOpenRouterModels(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = ListModels("openrouter")
	}
}
