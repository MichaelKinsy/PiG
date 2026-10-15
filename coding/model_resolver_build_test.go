package coding

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// resolveCliModelOverOwnedModels is ResolveCliModel over GetModels' built models, the reference the lazily built resolution must equal.
func resolveCliModelOverOwnedModels(options ResolveCliModelOptions) ResolveCliModelResult {
	resolved := icodingagent.ResolveCliModel(options.CliProvider, options.CliModel, string(options.CliThinking), false, resolverRuntime{options.ModelRuntime})
	result := ResolveCliModelResult{ThinkingLevel: resolved.ThinkingLevel, Warning: resolved.Warning, Error: resolved.Error}
	if resolved.Model != nil {
		result.Model = resolved.Model.NativeModel
	}
	return result
}

// ResolveCliModel builds only the catalog model it selects. Every selection form (provider/model, --provider, bare
// and ambiguous IDs, thinking suffixes, custom-model fallbacks, unknown names, case differences) resolves to a model deeply equal to the one
// resolution over GetModels' built models returns, including the model its catalog backend streams.
func TestResolveCliModelBuildingOnlyTheSelectedModelEqualsResolutionOverBuiltModels(t *testing.T) {
	runtime := resolverRuntimeFixture(t, "anthropic", "openai")
	cases := []ResolveCliModelOptions{
		{CliModel: "openai/gpt-4o"},
		{CliModel: "OpenAI/GPT-4o"},
		{CliProvider: "openai", CliModel: "gpt-4o"},
		{CliProvider: "OPENAI", CliModel: "openai/gpt-4o"},
		{CliModel: "gpt-4o"},
		{CliModel: "sonnet"},
		{CliModel: "sonnet:high"},
		{CliModel: "anthropic/sonnet:low"},
		{CliModel: "openai/gpt-4o:xhigh"},
		{CliModel: "openai/not-a-model"},
		{CliModel: "openai/not-a-model:high"},
		{CliProvider: "openai", CliModel: "custom-id", CliThinking: "medium"},
		{CliProvider: "anthropic", CliModel: "custom-id", CliThinking: "off"},
		{CliProvider: "nope", CliModel: "gpt-4o"},
		{CliModel: "nope/gpt-4o"},
		{CliModel: "definitely-missing"},
		{CliModel: "openrouter/openai/gpt-4o"},
		{CliModel: "gpt"},
		{CliModel: ""},
	}
	assertResolutionsEqual(t, runtime, cases)

	// Registered providers (one replacing a bundled provider's models) and virtual models are listed as built
	// models in the same order; a virtual model follows the listed models.
	services := newTestServices(t)
	registerRegistryInput(t, services, "custom-gateway", registryInput("https://gateway.invalid/v1", "openai-completions", "gw-small", "gw-large"))
	registerRegistryInput(t, services, "openai", registryInput("https://openai-proxy.invalid/v1", "openai-completions", "gpt-4o", "proxy-only"))
	registryRefresh(t, services)
	extended := services.ModelRuntime()
	route := func(context.Context, ModelRouteRequest) (ModelRoute, error) {
		return ModelRoute{}, errors.New("unrouted")
	}
	for _, definition := range []VirtualModelDefinition{{Provider: "anthropic", ID: "virtual-fast", Name: "Virtual Fast", Route: route}, {Provider: "router", ID: "auto", Name: "Auto", Route: route}} {
		if err := extended.RegisterVirtualModel(definition); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{"custom-gateway/gw-small", "openai/proxy-only", "anthropic/virtual-fast", "router/auto"} {
		if got := ResolveCliModel(ResolveCliModelOptions{CliModel: ref, ModelRuntime: extended}).Model; got == nil || got.ProviderMeta.ProviderID+"/"+got.ID != ref {
			t.Fatalf("ResolveCliModel(%q) = %+v, want that model", ref, got)
		}
	}
	assertResolutionsEqual(t, extended, append(cases,
		ResolveCliModelOptions{CliModel: "custom-gateway/gw-small"},
		ResolveCliModelOptions{CliModel: "gw-large:high"},
		ResolveCliModelOptions{CliModel: "openai/proxy-only"},
		ResolveCliModelOptions{CliProvider: "custom-gateway", CliModel: "gw-unknown"},
		ResolveCliModelOptions{CliModel: "anthropic/virtual-fast"},
		ResolveCliModelOptions{CliModel: "virtual"},
		ResolveCliModelOptions{CliModel: "router/auto"},
		ResolveCliModelOptions{CliModel: "auto"},
	))
}

func assertResolutionsEqual(t *testing.T, runtime *ModelRuntime, cases []ResolveCliModelOptions) {
	t.Helper()
	for _, options := range cases {
		options.ModelRuntime = runtime
		want := resolveCliModelOverOwnedModels(options)
		got := ResolveCliModel(options)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ResolveCliModel(%+v):\n got %+v\nwant %+v", options, got, want)
		}
	}
}

// The selected model shares no slice, map or pointer with the bundled catalog: changing it, or the model its backend streams, changes no later
// resolution.
func TestResolveCliModelReturnsAModelTheCatalogDoesNotShare(t *testing.T) {
	runtime := resolverRuntimeFixture(t, "openai")
	first := ResolveCliModel(ResolveCliModelOptions{CliModel: "openai/gpt-4o", ModelRuntime: runtime}).Model
	if first == nil {
		t.Fatal("openai/gpt-4o did not resolve")
	}
	reference := ResolveCliModel(ResolveCliModelOptions{CliModel: "openai/gpt-4o", ModelRuntime: runtime}).Model
	scribble := func(model *ai.Model) {
		model.Input = append(model.Input[:0], "scribbled")
		for level := range model.ThinkingLevelMap {
			model.ThinkingLevelMap[level] = nil
		}
		if model.ProviderMeta.Headers == nil {
			model.ProviderMeta.Headers = map[string]string{}
		}
		model.ProviderMeta.Headers["X-Scribbled"] = "1"
		if model.ProviderMeta.Compat != nil && model.ProviderMeta.Compat.SupportsStore != nil {
			*model.ProviderMeta.Compat.SupportsStore = !*model.ProviderMeta.Compat.SupportsStore
		}
		if model.InputLimits != nil {
			*model.InputLimits = ai.ModelInputLimits{}
		}
	}
	scribble(first)
	if backend, ok := first.Provider.(*catalogModelBackend); ok {
		scribble(backend.model)
	} else {
		t.Fatalf("provider %T is not the catalog backend", first.Provider)
	}
	again := ResolveCliModel(ResolveCliModelOptions{CliModel: "openai/gpt-4o", ModelRuntime: runtime}).Model
	again.Provider, reference.Provider = nil, nil
	if !reflect.DeepEqual(again, reference) {
		t.Fatalf("a changed resolution changed the catalog:\n got %+v\nwant %+v", again, reference)
	}
}
