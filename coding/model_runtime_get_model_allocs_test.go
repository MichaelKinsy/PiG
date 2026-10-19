//go:build !race

package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// GetModel of a plain bundled provider builds the one model it returns: the allocations of a lookup do not grow with the number of models the
// provider's catalog lists. Building every model of the provider and searching the result cost about eight allocations per catalog model
// (3,268 for openrouter's 402 models against 48 for groq's 7).
func TestGetModelAllocationsDoNotGrowWithTheProviderCatalog(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	runtime := services.ModelRuntime()
	perLookup := func(provider string) (float64, int) {
		catalog := ai.ListModels(provider)
		if len(catalog) == 0 {
			t.Fatalf("provider %s has no bundled models", provider)
		}
		last := catalog[len(catalog)-1].ID
		if runtime.GetModel(provider, last) == nil {
			t.Fatalf("model %s/%s is missing", provider, last)
		}
		return testing.AllocsPerRun(20, func() { runtime.GetModel(provider, last) }), len(catalog)
	}
	small, smallModels := perLookup("groq")
	large, largeModels := perLookup("openrouter")
	t.Logf("%.0f allocations with %d models, %.0f with %d", small, smallModels, large, largeModels)
	if largeModels < 20*smallModels/2 {
		t.Skipf("the bundled catalog no longer has a provider much larger than groq's (%d against %d)", largeModels, smallModels)
	}
	if large > small+20 {
		t.Fatalf("GetModel allocates %.0f times with %d catalog models and %.0f with %d: the lookup builds models it does not return", large, largeModels, small, smallModels)
	}
}
