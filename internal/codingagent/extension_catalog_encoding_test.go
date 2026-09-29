package codingagent

import (
	"bytes"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestExtensionCatalogEncodingCurrentContentAndOrder(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	storage, err := ai.NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry.SetAuthStorage(storage)
	cache := &extensionCatalogEncoding{}
	var previous json.RawMessage
	check := func(unchanged bool) {
		t.Helper()
		catalog := registry.GetAllModelData()
		got, err := json.Marshal(cache.state(registry, catalog))
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(ExtensionModelRegistryState(registry, catalog))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("cached publication differs from uncached registry bytes")
		}
		if previous != nil && (&previous[0] == &cache.models[0]) != unchanged {
			t.Fatalf("model encoding reuse = %v, want %v", &previous[0] == &cache.models[0], unchanged)
		}
		previous = cache.models
	}
	check(false)
	check(true)
	for _, provider := range []string{"parity-oauth", "parity-key", "parity-custom-url", "parity-no-default"} {
		if err := registry.RegisterProvider(provider, extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "http://localhost:1234/v1", Models: []extension.ProviderModelConfig{{ID: provider + "-model", Name: provider}}}); err != nil {
			t.Fatal(err)
		}
		check(false)
	}
	if err := storage.Set("parity-oauth", ai.Credential{Type: ai.CredentialOAuth, Access: "temporary-oauth-token"}); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := storage.Set("parity-key", ai.Credential{Type: ai.CredentialAPIKey, Key: "temporary-stored-key"}); err != nil {
		t.Fatal(err)
	}
	check(true)
	registry.SetRuntimeAPIKey("parity-key", "temporary-test-key")
	check(true)
	registry.UnregisterProvider("parity-key")
	check(false)
	if err := registry.RegisterProvider("parity-key", extension.ProviderConfig{API: ai.APIOpenAIResponses, BaseURL: "http://localhost:1234/v2", Models: []extension.ProviderModelConfig{{ID: "replacement"}}}); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestExtensionCatalogEncodingNestedMutationAndEmpty(t *testing.T) {
	cache := &extensionCatalogEncoding{}
	catalog := []*ai.Model{{ID: "model", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "provider", Headers: map[string]string{"test": "before"}, Compat: &ai.ModelCompat{SupportsStore: new(true)}}, SamplingParams: map[string]any{"nested": map[string]any{"value": "before"}}}}
	for _, mutate := range []func(){
		func() {},
		func() { catalog[0].ProviderMeta.Headers["test"] = "after" },
		func() { *catalog[0].ProviderMeta.Compat.SupportsStore = false },
		func() { catalog[0].SamplingParams["nested"].(map[string]any)["value"] = "after" },
		func() { catalog[0].Input = []string{} },
		func() { catalog[0].Input = nil },
		func() { catalog = nil },
	} {
		mutate()
		state := cache.state(nil, catalog)
		got, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(ExtensionModelRegistryState(nil, catalog))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("mutation was stale: %s != %s", got, want)
		}
		state["models"].(json.RawMessage)[0] = '!'
		fresh, err := json.Marshal(cache.state(nil, catalog))
		if err != nil || !bytes.Equal(fresh, want) {
			t.Fatal("caller mutation damaged the retained encoding")
		}
	}
}

func TestExtensionCatalogEncodingDoesNotEvaluateUserMarshalers(t *testing.T) {
	for _, field := range []string{"sampling", "routing", "template", "gateway", "args"} {
		t.Run(field, func(t *testing.T) {
			calls := 0
			data := map[string]any{"nested": []any{map[string]any{"value": catalogCountingValue{calls: &calls}}}}
			model := &ai.Model{ID: "custom", ProviderMeta: ai.ProviderMetadata{Compat: &ai.ModelCompat{}}}
			switch field {
			case "sampling":
				model.SamplingParams = data
			case "routing":
				model.ProviderMeta.Compat.OpenRouterRouting = data
			case "template":
				model.ProviderMeta.Compat.ChatTemplateKwargs = data
			case "gateway":
				model.ProviderMeta.Compat.VercelGatewayRouting = data
			case "args":
				model.ProviderMeta.Compat.ChatTemplateArgs = data
			}
			catalog := []*ai.Model{model}
			want, err := json.Marshal(ExtensionModelRegistryState(nil, catalog))
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := calls
			calls = 0
			cache := &extensionCatalogEncoding{}
			cache.state(nil, []*ai.Model{{ID: "previous"}})
			got, err := json.Marshal(cache.state(nil, catalog))
			if err != nil || calls != wantCalls || !bytes.Equal(got, want) || cache.models != nil {
				t.Fatalf("user marshaler evaluated %d times, want %d; error=%v", calls, wantCalls, err)
			}
		})
	}
}

func TestCatalogCacheEligibilityHandlesCyclesAndSharedData(t *testing.T) {
	shared := map[string]any{"value": []any{"text", true, nil}}
	if !catalogMapCacheable(map[string]any{"first": shared, "second": shared}) {
		t.Fatal("shared acyclic JSON data was rejected")
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if catalogMapCacheable(cycle) {
		t.Fatal("cyclic map was accepted")
	}
	list := make([]any, 1)
	list[0] = list
	if catalogMapCacheable(map[string]any{"list": list}) {
		t.Fatal("cyclic slice was accepted")
	}
}

type catalogCountingValue struct{ calls *int }

func (v catalogCountingValue) MarshalJSON() ([]byte, error) {
	*v.calls++
	return json.Marshal(*v.calls)
}

func TestCatalogCyclesReachJSONError(t *testing.T) {
	object := map[string]any{}
	object["self"] = object
	list := make([]any, 1)
	list[0] = list
	for _, data := range []map[string]any{object, {"list": list}} {
		catalog := []*ai.Model{{ID: "cycle", SamplingParams: data}}
		_, want := json.Marshal(ExtensionModelRegistryState(nil, catalog))
		cache := &extensionCatalogEncoding{}
		_, got := json.Marshal(cache.state(nil, catalog))
		if got == nil || want == nil || got.Error() != want.Error() || cache.models != nil {
			t.Fatalf("cycle errors differ: cached=%v original=%v", got, want)
		}
	}
}

func TestCatalogSnapshotPreservesSignedZeroAndRawJSON(t *testing.T) {
	model := &ai.Model{ID: "signed-zero", ProviderMeta: ai.ProviderMetadata{ProviderID: "provider", Compat: &ai.ModelCompat{VLLMPriority: new(float64(0))}}, SamplingParams: map[string]any{"nested": map[string]any{"f32": float32(0), "f64": float64(0)}}}
	catalog := []*ai.Model{model}
	cache := &extensionCatalogEncoding{}
	mutations := []func(){
		func() {},
		func() { model.Capabilities.InputCostPer1M = math.Copysign(0, -1) },
		func() { *model.ProviderMeta.Compat.VLLMPriority = math.Copysign(0, -1) },
		func() { model.SamplingParams["nested"].(map[string]any)["f32"] = float32(math.Copysign(0, -1)) },
		func() { model.SamplingParams["nested"].(map[string]any)["f64"] = math.Copysign(0, -1) },
		func() { model.Capabilities.InputCostPer1M = 0 },
		func() { model.ProviderMeta.Headers = map[string]string{} },
		func() { model.ProviderMeta.Headers = nil },
		func() { model.SamplingParams["raw"] = json.RawMessage(`{"x":0}`) },
		func() { model.SamplingParams["raw"] = json.RawMessage(`{"x":1}`) },
	}
	for i, mutate := range mutations {
		mutate()
		want, err := json.Marshal(ExtensionModelRegistryState(nil, catalog))
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(cache.state(nil, catalog))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("mutation %d changed encoding: error=%v\ngot %s\nwant %s", i, err, got, want)
		}
	}
}

func TestCatalogCacheHitDoesNotAllocatePerModel(t *testing.T) {
	measure := func(count int) float64 {
		catalog := make([]*ai.Model, count)
		for i := range catalog {
			catalog[i] = &ai.Model{ID: "model", Provider: catalogUnusedProvider{}, ProviderMeta: ai.ProviderMetadata{ProviderID: "provider"}}
		}
		cache := &extensionCatalogEncoding{}
		cache.state(nil, catalog)
		return testing.AllocsPerRun(30, func() { cache.state(nil, catalog) })
	}
	small, large := measure(64), measure(128)
	if large > small {
		t.Fatalf("cache-hit allocations grow with model count: 64=%.0f 128=%.0f", small, large)
	}
}

// ModelInfo uses the metadata provider ID when present. Runtime provider identity is not part of that projection, and the snapshot must not retain the provider's process lifetime.
func TestCatalogCacheHitIgnoresOnlyRuntimeProvider(t *testing.T) {
	cache := &extensionCatalogEncoding{}
	catalog := []*ai.Model{nil, {ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: "provider"}}, nil}
	cache.state(nil, catalog)
	previous := cache.models
	catalog[1].Provider = catalogUnusedProvider{}
	state := cache.state(nil, catalog)
	if &previous[0] != &cache.models[0] || cache.input[0].Provider != nil {
		t.Fatal("runtime provider change replaced the model encoding or retained the provider")
	}
	want, err := json.Marshal(ExtensionModelRegistryState(nil, catalog))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(state)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("runtime provider change affected bytes: got %s, want %s, error %v", got, want, err)
	}
	catalog[1].ProviderMeta.ProviderID = "replacement"
	cache.state(nil, catalog)
	if &previous[0] == &cache.models[0] || cache.input[0].ProviderMeta.ProviderID != "replacement" {
		t.Fatal("metadata provider change did not invalidate the encoding")
	}
}

type catalogUnusedProvider struct{ ai.Provider }

func (catalogUnusedProvider) ID() string {
	panic("eligible metadata must not query its runtime provider")
}

func TestCatalogSnapshotAvoidsMetadataSerialization(t *testing.T) {
	catalog := make([]*ai.Model, len(ai.GeneratedModels))
	for i := range ai.GeneratedModels {
		catalog[i] = ai.GeneratedModels[i].ToModel()
	}
	cache := &extensionCatalogEncoding{}
	cache.state(nil, catalog)
	reference := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			if _, err := json.Marshal(catalog); err != nil {
				b.Fatal(err)
			}
		}
	}).AllocedBytesPerOp()
	got := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			cache.state(nil, catalog)
		}
	}).AllocedBytesPerOp()
	if got >= reference {
		t.Fatalf("unchanged snapshot allocates %d bytes; serializing its metadata alone allocates %d", got, reference)
	}
}

func BenchmarkExtensionCatalogEncoding(b *testing.B) {
	for _, cached := range []bool{false, true} {
		name := "uncached"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			registry := NewModelRegistry(b.TempDir())
			cache := &extensionCatalogEncoding{}
			b.ReportAllocs()
			for b.Loop() {
				catalog := registry.GetAllModelData()
				var state map[string]any
				if cached {
					state = cache.state(registry, catalog)
				} else {
					state = ExtensionModelRegistryState(registry, catalog)
				}
				if _, err := json.Marshal(state); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
