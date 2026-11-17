package coding

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// referenceAvailableRuntimeModels is AvailableRuntimeModels as the row 136 member wrote it: an index of every composed model.
func referenceAvailableRuntimeModels(runtime *ModelRuntime, models []icodingagent.RuntimeModel) []icodingagent.RuntimeModel {
	type key struct{ provider, id string }
	index := make(map[key]int, len(models))
	for i, model := range models {
		index[key{model.Provider, model.ID}] = i
	}
	available := runtime.GetAvailableSnapshot()
	out := make([]icodingagent.RuntimeModel, 0, len(available))
	for _, model := range available {
		if i, ok := index[key{model.ProviderMeta.ProviderID, model.ID}]; ok {
			out = append(out, models[i])
		}
	}
	return out
}

type availabilityRef struct{ provider, id string }

func availabilityTestRuntime(t testing.TB) *ModelRuntime {
	t.Helper()
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{AgentDir: t.TempDir(), CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { services.Close() })
	return services.ModelRuntime()
}

// publish replaces the published availability with the refs, in order.
func publish(runtime *ModelRuntime, refs []availabilityRef) {
	available := make([]*ai.Model, len(refs))
	for i, ref := range refs {
		model := &ai.Model{ID: ref.id}
		model.ProviderMeta.ProviderID = ref.provider
		available[i] = model
	}
	runtime.availability.mu.Lock()
	runtime.availability.snapshot.available = available
	runtime.availability.mu.Unlock()
}

// catalogOf lists the refs as composed models; Name is the position, so equal results name the same entries.
func catalogOf(refs []availabilityRef) []icodingagent.RuntimeModel {
	models := make([]icodingagent.RuntimeModel, len(refs))
	for i, ref := range refs {
		models[i] = icodingagent.RuntimeModel{Provider: ref.provider, ID: ref.id, Name: fmt.Sprint(i)}
	}
	return models
}

func checkAvailableRuntimeModels(t testing.TB, runtime *ModelRuntime, catalog, snapshot []availabilityRef) {
	t.Helper()
	publish(runtime, snapshot)
	models := catalogOf(catalog)
	want := referenceAvailableRuntimeModels(runtime, models)
	got := runtime.AvailableRuntimeModels(models)
	if got == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog %v snapshot %v:\n got %+v\nwant %+v", catalog, snapshot, got, want)
	}
}

func TestAvailableRuntimeModelsMatchesTheCatalogWideIndex(t *testing.T) {
	runtime := availabilityTestRuntime(t)
	a1, a2, b1, b2, c1 := availabilityRef{"a", "1"}, availabilityRef{"a", "2"}, availabilityRef{"b", "1"}, availabilityRef{"b", "2"}, availabilityRef{"c", "1"}
	r := func(refs ...availabilityRef) []availabilityRef { return refs }
	for name, c := range map[string]struct{ catalog, snapshot []availabilityRef }{
		"empty both":                   {nil, nil},
		"empty snapshot":               {r(a1, a2, b1), nil},
		"empty catalog":                {nil, r(a1)},
		"all available":                {r(a1, a2, b1, b2), r(a1, a2, b1, b2)},
		"snapshot order wins":          {r(a1, a2, b1, b2), r(b2, a1, b1)},
		"snapshot names an absent one": {r(a1, a2), r(a2, c1, a1)},
		"same id across providers":     {r(a1, b1, a2, b2), r(b1)},
		"duplicate catalog entry":      {r(a1, b1, a1), r(a1, b1)},
		"duplicate snapshot entry":     {r(a1, b1), r(b1, a1, b1)},
		"nothing in common":            {r(a1, a2), r(b1, b2)},
	} {
		t.Run(name, func(t *testing.T) { checkAvailableRuntimeModels(t, runtime, c.catalog, c.snapshot) })
	}
}

func FuzzAvailableRuntimeModelsMatchesTheCatalogWideIndex(f *testing.F) {
	f.Add([]byte{}, []byte{})
	f.Add([]byte{0, 1, 2, 3, 4, 5}, []byte{5, 0, 3})
	f.Add([]byte{0, 0, 1, 1}, []byte{1, 1, 0, 9})
	runtime := availabilityTestRuntime(f)
	refs := func(data []byte) []availabilityRef {
		out := make([]availabilityRef, len(data))
		for i, b := range data {
			out[i] = availabilityRef{provider: fmt.Sprint("p", b%3), id: fmt.Sprint("m", b/3%5)}
		}
		return out
	}
	f.Fuzz(func(t *testing.T, catalog, snapshot []byte) {
		checkAvailableRuntimeModels(t, runtime, refs(catalog), refs(snapshot))
	})
}

func BenchmarkAvailableRuntimeModels(b *testing.B) {
	runtime := availabilityTestRuntime(b)
	for _, size := range []int{900, 5000} {
		for _, count := range []int{5, 500} {
			catalog := make([]availabilityRef, size)
			for i := range catalog {
				catalog[i] = availabilityRef{fmt.Sprint("provider-", i/50), fmt.Sprint("model-", i)}
			}
			snapshot := make([]availabilityRef, count)
			for i := range snapshot {
				snapshot[i] = catalog[i*size/count]
			}
			publish(runtime, snapshot)
			models := catalogOf(catalog)
			for name, run := range map[string]func([]icodingagent.RuntimeModel) []icodingagent.RuntimeModel{
				"catalog-wide-index": func(m []icodingagent.RuntimeModel) []icodingagent.RuntimeModel {
					return referenceAvailableRuntimeModels(runtime, m)
				},
				"available-only-index": runtime.AvailableRuntimeModels,
			} {
				b.Run(fmt.Sprintf("catalog=%d/available=%d/%s", size, count, name), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if len(run(models)) != count {
							b.Fatal("wrong count")
						}
					}
				})
			}
		}
	}
}
