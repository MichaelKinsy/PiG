package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/catalogfixture"
)

// pickerBenchmarkModels is the size of the picker benchmarks' scenario: the 67 models that the benchmarks listed and scoped when their rows were written (Gemini 23 and OpenAI 44 built-in models, ai/models_generated.go of 2026-11-12).
const pickerBenchmarkModels = 67

// pickerBenchmarkProviders are the providers of the fixture that the picker benchmarks configure with a key: nvidia 18, ant-ling 30, radius 15, anthropic 2 and fireworks 2 entries, pickerBenchmarkModels in all, on three APIs, with and without image input and reasoning.
var pickerBenchmarkProviders = []string{"nvidia", "ant-ling", "radius", "anthropic", "fireworks"}

// fixtureModelRegistry is the production registry (NewModelRegistry) over the models.json of catalogfixture.ModelsJSON: all catalogfixture.LiveEntries fixture models as custom models of 19 providers, those of pickerBenchmarkProviders with an API key, so they are the available models. The built-in catalog adds no available model, so the picker benchmarks do not move when it is refreshed. A built-in provider that the environment configures lists a model outside the fixture, and the registry fails the benchmark or test.
func fixtureModelRegistry(tb testing.TB) *ModelRegistry {
	tb.Helper()
	tb.Setenv("PIG_OFFLINE", "1")
	dir := tb.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), catalogfixture.ModelsJSON("benchmark-key", pickerBenchmarkProviders...), 0o600); err != nil {
		tb.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	if loadError := registry.LoadError(); loadError != "" {
		tb.Fatalf("the fixture's models.json does not load: %s", loadError)
	}
	for _, entry := range registry.GetAvailable() {
		if !strings.HasPrefix(entry.ProviderID, catalogfixture.ProviderPrefix) {
			tb.Fatalf("the registry lists a model of the built-in provider %s; unset its API key variables", entry.ProviderID)
		}
	}
	return registry
}

// The picker benchmarks' input is the fixture and nothing the built-in catalog changes: the registry lists exactly the fixture entries of the keyed providers, from the fixture of catalogfixture.LiveEntries entries, with the shapes the picker's rows come from (image input or not, reasoning or not, several providers).
func TestPickerBenchmarkRegistryIsTheFixtureAlone(t *testing.T) {
	registry := fixtureModelRegistry(t)
	entries := registry.GetAvailable()
	want := map[string]bool{}
	rows := catalogfixture.GeneratedModels()
	if len(rows) != catalogfixture.LiveEntries {
		t.Fatalf("fixture has %d entries, want %d", len(rows), catalogfixture.LiveEntries)
	}
	for _, row := range rows {
		if slices.Contains(pickerBenchmarkProviders, row.Provider) {
			want[catalogfixture.ProviderPrefix+row.Provider+"/"+row.ID] = true
		}
	}
	if len(entries) != len(want) || len(want) != pickerBenchmarkModels {
		t.Fatalf("registry lists %d available models, want the %d fixture entries of %v", len(entries), len(want), pickerBenchmarkProviders)
	}
	var image, text, reasoning, plain int
	providers := map[string]bool{}
	for _, entry := range entries {
		key := entry.ProviderID + "/" + entry.ModelID
		if !want[key] {
			t.Fatalf("registry lists %s, which is not a fixture entry", key)
		}
		delete(want, key)
		providers[entry.ProviderID] = true
		if slices.Contains(entry.Input, "image") {
			image++
		} else {
			text++
		}
		if entry.Reasoning {
			reasoning++
		} else {
			plain++
		}
	}
	if len(want) != 0 {
		t.Fatalf("%d fixture entries are missing from the registry", len(want))
	}
	if len(providers) != len(pickerBenchmarkProviders) || image == 0 || text == 0 || reasoning == 0 || plain == 0 {
		t.Fatalf("fixture registry shapes: %d providers, image %d, text only %d, reasoning %d, plain %d", len(providers), image, text, reasoning, plain)
	}
}

// The divisors of the built-in picker benchmarks are the counts the benchmarks handle, taken from the registry and the scope, not constants.
func TestPickerBenchmarkDivisorsAreTheHandledCounts(t *testing.T) {
	registry := builtinPickerRegistry(t)
	s := newPickerScenario(t, registry, true)
	if listed := len(registry.GetAvailable()); s.available != listed || s.available == 0 {
		t.Fatalf("divisor %d available models, the registry lists %d", s.available, listed)
	}
	if items := len(s.m.availableModelItems()); s.scoped != items || s.scoped != s.available {
		t.Fatalf("divisor %d scoped models, the picker lists %d and %d are available", s.scoped, items, s.available)
	}
}
