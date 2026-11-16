//go:build !race

package codingagent

import (
	"fmt"
	"testing"
)

// Resolving "<provider>/<model>" against the catalog (every --model does it) matches the text against the provider of each catalog model; what
// that costs must not grow with the number of models listed: Pi compares strings (model-resolver.ts splitProviderPrefix over models), and an
// ASCII id needs no copy.
func TestSplitProviderPrefixAllocationsDoNotGrowWithTheCatalog(t *testing.T) {
	catalog := func(n int) []RuntimeModel {
		models := make([]RuntimeModel, 0, n)
		for i := range n {
			models = append(models, RuntimeModel{Provider: fmt.Sprintf("provider-%d", i/50), ID: fmt.Sprintf("model-%d", i)})
		}
		return models
	}
	small, large := catalog(100), catalog(1000)
	allocs := func(models []RuntimeModel) float64 {
		return testing.AllocsPerRun(20, func() {
			if provider, rest, ok := splitProviderPrefix("provider-1/model-60", models); !ok || provider != "provider-1" || rest != "model-60" {
				t.Fatalf("splitProviderPrefix = %q, %q, %v", provider, rest, ok)
			}
		})
	}
	if few, many := allocs(small), allocs(large); many > few {
		t.Fatalf("allocations grew with the catalog: %v for 100 models, %v for 1000", few, many)
	}
}
