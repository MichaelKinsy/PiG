//go:build !race

package coding

import "testing"

// ResolveCliModel lists every catalog model as a resolver model (runtimeModels) and builds only the one it keeps. Listing a model costs what
// reading its source costs and no object of its own: the list is one array, and the build of a kept model is described by a pointer into the
// sources and one function shared by the list, not by a closure per model. The registry's own listing of the sources is the denominator.
func TestRuntimeModelsAddNoObjectPerListedModel(t *testing.T) {
	runtime := resolverRuntimeFixture(t, "anthropic", "openai")
	sources := runtime.services.Registry().GetAllModelDataSources()
	if len(sources) < 500 {
		t.Fatalf("the catalog lists %d models, want the bundled catalog", len(sources))
	}
	if len(runtime.runtimeModels()) != len(sources) {
		t.Fatalf("runtimeModels lists %d models, the registry %d", len(runtime.runtimeModels()), len(sources))
	}
	listing := testing.AllocsPerRun(5, func() { _ = runtime.services.Registry().GetAllModelDataSources() })
	listed := testing.AllocsPerRun(5, func() { _ = runtime.runtimeModels() })
	// A model with intrinsic headers or a built model of its own (a registered provider's) costs allocations the bundled catalog's entries do not.
	if extra := listed - listing; extra > float64(len(sources))/10 {
		t.Fatalf("runtimeModels allocates %.0f objects over the %.0f of the registry's listing of %d models", extra, listing, len(sources))
	}
}
