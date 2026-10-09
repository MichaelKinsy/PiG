package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/src/core/model-registry.ts getAll, getAvailable, hasConfiguredAuth (the registry facade delegates to ModelRuntime and returns pi-ai Model objects, whose cost is part of the model).
func TestModelRegistryFacadeReturnsAIModelsWithCost(t *testing.T) {
	const providers = `{"demo":{"baseUrl":"https://example.com/v1","apiKey":"literal-key","api":"openai-completions","models":[{"id":"priced","name":"Priced","reasoning":false,"input":["text"],"cost":{"input":1.25,"output":2.5,"cacheRead":0.125,"cacheWrite":0.5},"contextWindow":1000,"maxTokens":100}]},"keyless":{"baseUrl":"https://example.com/v1","apiKey":"$FACADE_SHAPE_MISSING_KEY","api":"openai-completions","models":[{"id":"unconfigured","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}]}}`
	t.Setenv("FACADE_SHAPE_MISSING_KEY", "")
	services := registryFromJSON(t, providers)
	registry := services.Registry()

	var priced *ai.Model
	for _, model := range registry.GetAll() {
		if model.ProviderID() == "demo" && model.ID == "priced" {
			priced = model
		}
	}
	if priced == nil {
		t.Fatal("GetAll does not list the models.json model as an ai.Model")
	}
	if cost := priced.CostRates(); cost.Input != 1.25 || cost.Output != 2.5 || cost.CacheRead != 0.125 || cost.CacheWrite != 0.5 {
		t.Errorf("cost = %+v, want input 1.25 output 2.5 cacheRead 0.125 cacheWrite 0.5", cost)
	}
	if priced.Capabilities.ContextWindow != 1000 || priced.Capabilities.MaxOutputTokens != 100 {
		t.Errorf("capabilities = %+v", priced.Capabilities)
	}

	// getAvailable is the published availability: the literal-key provider is configured, so its model is available.
	available := registry.GetAvailable()
	if !slices.ContainsFunc(available, func(model *ai.Model) bool { return model.ProviderID() == "demo" && model.ID == "priced" }) {
		t.Errorf("GetAvailable = %d models, none is demo/priced", len(available))
	}
	if !registry.HasConfiguredAuth(priced) {
		t.Error("HasConfiguredAuth(demo/priced) = false with a literal apiKey")
	}
	// An unconfigured provider is in GetAll but not in GetAvailable.
	if !slices.ContainsFunc(registry.GetAll(), func(model *ai.Model) bool { return model.ProviderID() == "keyless" }) {
		t.Error("GetAll omits the unconfigured provider's model")
	}
	if slices.ContainsFunc(available, func(model *ai.Model) bool { return model.ProviderID() == "keyless" }) {
		t.Error("GetAvailable lists a model whose provider has no configured auth")
	}
	other := *priced
	other.ProviderMeta.ProviderID = "no-such-provider"
	if registry.HasConfiguredAuth(&other) {
		t.Error("HasConfiguredAuth(no-such-provider) = true")
	}
}

// upstream: model-registry.ts constructor(runtime) and registerProvider(providerName, config): the facade built from a runtime registers through that runtime's registry.
func TestNewModelRegistryRegistersThroughItsRuntime(t *testing.T) {
	services := registryFromJSON(t, `{}`)
	registry := NewModelRegistry(services.ModelRuntime())
	if err := registry.RegisterProvider("facade-registered", ProviderConfigInput{
		BaseURL: "https://facade.test/v1", API: ai.APIOpenAICompletions, APIKey: "literal-key",
		Models: []ai.AnyModel{&ai.Model{ID: "fm", DisplayName: "Facade Model", Capabilities: ai.ModelCapabilities{ContextWindow: 4096, MaxOutputTokens: 512, InputCostPer1M: 3}}},
	}); err != nil {
		t.Fatal(err)
	}
	var registered *ai.Model
	for _, model := range registry.GetAll() {
		if model.ProviderID() == "facade-registered" && model.ID == "fm" {
			registered = model
		}
	}
	if registered == nil {
		t.Fatal("a provider registered by name is missing from GetAll")
	}
	if registered.CostRates().Input != 3 {
		t.Errorf("cost = %+v, want input 3", registered.CostRates())
	}
	// The facade shares the one registry: services.Registry sees the same provider.
	if !slices.ContainsFunc(services.Registry().GetAll(), func(model *ai.Model) bool { return model.ProviderID() == "facade-registered" }) {
		t.Error("Services.Registry does not see a provider registered through a second facade of the same runtime")
	}
	registry.UnregisterProvider("facade-registered")
	if slices.ContainsFunc(registry.GetAll(), func(model *ai.Model) bool { return model.ProviderID() == "facade-registered" }) {
		t.Error("UnregisterProvider left the provider in GetAll")
	}
}

// packages/coding-agent/src/core/model-registry.ts:210-218: registerProvider(provider: Provider) registers the provider object through runtime.registerNativeProvider, so its models enter the registry's catalog and a later registration under the same id replaces them (model-runtime.ts one native-or-legacy namespace).
func TestModelRegistryRegisterProviderObjectUpstream(t *testing.T) {
	_, services := newAvailabilitySession(t)
	registry := services.Registry()
	const id = "object-provider"
	if err := registry.RegisterProviderObject(namespaceProvider(id, "first", true)); err != nil {
		t.Fatal(err)
	}
	if model := services.ModelRuntime().GetModel(id, "first"); model == nil {
		t.Fatal("a Provider object's model is missing from the catalog after registerProvider(provider)")
	}
	if err := registry.RegisterProviderObject(namespaceProvider(id, "second", true)); err != nil {
		t.Fatal(err)
	}
	if services.ModelRuntime().GetModel(id, "first") != nil || services.ModelRuntime().GetModel(id, "second") == nil {
		t.Fatal("re-registering the same provider id must replace its models")
	}
}
