package codingagent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// legacyProviderModelData is GetProviderModelData as it was before the Radius branch moved first: the bundled catalog (and a remote overlay) is built for every provider, and a Radius provider's models then replace it.
func legacyProviderModelData(r *ModelRegistry, id string) []*ai.Model {
	if plain, ok := r.plainGeneratedModels(id); ok {
		return plainModelData(plain)
	}
	if r.GetProvider(id) != nil {
		return r.GetNativeModels(id)
	}
	generatedModels := builtinChatCatalogListed(id)
	var baseline []*ai.Model
	if len(generatedModels) > 0 {
		baseline = make([]*ai.Model, 0, len(generatedModels))
	}
	for _, generated := range generatedModels {
		baseline = append(baseline, generated.ToModel())
	}
	// The remote catalog overlay adds models to, and replaces same-id models of, the bundled catalog (remote-catalog-provider.ts:getModels).
	// A personalized catalog (the user's own list) replaces the bundled one instead (remote-catalog-provider.ts:withPersonalizedCatalog getModels).
	if overlay := r.remoteOverlayModels(id); len(overlay) > 0 {
		chatOverlay := slices.DeleteFunc(overlay, func(model ai.AnyModel) bool { return !ai.IsModelType(model, ai.ModelTypeChat) })
		merged := mergeRemoteCatalogModels(ai.AnyModels(baseline), chatOverlay)
		if r.remoteOverlayReplaces(id) {
			// pig divergence (D199): the saved list replaces the bundled one, so OpenRouter's routing variants are derived from it, as they are from the bundled models.
			merged = ai.WithOpenRouterRoutingVariantModels(chatOverlay)
		}
		baseline = baseline[:0:0]
		for _, model := range merged {
			baseline = append(baseline, model.(*ai.Model))
		}
	}
	r.mu.RLock()
	radius := r.radiusProviderLocked(id)
	r.mu.RUnlock()
	if radius != nil {
		baseline = nil
		for _, model := range radius.GetModels() {
			baseline = append(baseline, nativeModelFromEntry(radiusModelEntry(model)))
		}
	}
	r.mu.RLock()
	dynamic, registered := r.dynamic[id]
	r.mu.RUnlock()
	var input *ProviderConfigInput
	compose := r.composeNativeProvider
	if registered {
		if r.NativeProvider(id) != nil {
			input, compose = new(carrierModelInput(id, dynamic)), r.composeCarrierProvider
		} else {
			input = new(providerModelInput(id, dynamic))
		}
	}
	provider, err := compose(&ai.ModelsProvider{ID: id, Name: id, GetModels: func() ([]*ai.Model, error) { return baseline, nil }}, input)
	if err != nil {
		return nil
	}
	models, err := provider.GetModels()
	if err != nil {
		return nil
	}
	return ai.OfferedModels(models)
}

// Reading the Radius branch first builds no bundled catalog to throw away, and lists exactly what it listed: the same models in the same order, equal field by field and as JSON bytes, for the default registry, a models.json Radius gateway with its own models, an overridden built-in Radius provider, a Radius provider with a stored catalog, and an extension that takes the Radius ID over.
func TestGetProviderModelDataListsTheSameModelsWithTheRadiusBranchFirst(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "")
	shapes := map[string]func(t *testing.T) *ModelRegistry{
		"default": func(t *testing.T) *ModelRegistry { registry, _, _ := radiusTestRegistry(t, "", nil); return registry },
		"models.json gateway with a model": func(t *testing.T) *ModelRegistry {
			registry, _, _ := radiusTestRegistry(t, `{"providers":{"acme":{"oauth":"radius","baseUrl":"https://acme.invalid","headers":{"X-Acme":"1"},"models":[{"id":"custom","api":"pi-messages","name":"Custom"}]}}}`, nil)
			return registry
		},
		"models.json override of the built-in provider": func(t *testing.T) *ModelRegistry {
			registry, _, _ := radiusTestRegistry(t, `{"providers":{"radius":{"headers":{"X-Radius":"1"},"modelOverrides":{"auto":{"name":"Override","contextWindow":4242}}}}}`, nil)
			return registry
		},
		"stored catalog": func(t *testing.T) *ModelRegistry {
			registry, _, store := radiusTestRegistry(t, `{"providers":{"radius-dev":{"oauth":"radius","baseUrl":"https://configured.example/v1"}}}`, nil)
			config, _ := ai.GetRadiusCredentialConfig(&ai.Credential{GatewayConfig: json.RawMessage(radiusTestConfigJSON("https://catalog.example/v1"))})
			models := ai.GetRadiusModelsFromConfig("radius-dev", config)
			if err := store.Write(t.Context(), "radius-dev", ai.ModelsStoreEntry{Models: []ai.AnyModel{models[0]}}); err != nil {
				t.Fatal(err)
			}
			registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{})
			return registry
		},
		"extension takes the Radius ID": func(t *testing.T) *ModelRegistry {
			registry, _, _ := radiusTestRegistry(t, "", nil)
			if err := registry.RegisterExtensionProvider(RadiusProviderID, extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://ext.invalid/v1", Models: []extension.ProviderModelConfig{{ID: "ext-1", Name: "Ext"}}}); err != nil {
				t.Fatal(err)
			}
			return registry
		},
	}
	for name, build := range shapes {
		t.Run(name, func(t *testing.T) {
			registry := build(t)
			radius := 0
			for _, id := range registry.modelProviderIDs() {
				if registry.isRadiusCatalog(id) {
					radius++
				}
				got, want := registry.GetProviderModelData(id), legacyProviderModelData(registry, id)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: %d models differ from the %d the bundled-catalog-first read gave", id, len(got), len(want))
				}
				gotJSON, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				wantJSON, _ := json.Marshal(want)
				if !bytes.Equal(gotJSON, wantJSON) {
					t.Fatalf("%s: JSON of the models differs", id)
				}
			}
			if name != "extension takes the Radius ID" && radius == 0 {
				t.Fatal("no Radius provider was compared")
			}
			if !slices.ContainsFunc(registry.GetAllModelData(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == RadiusProviderID }) {
				t.Fatal("no model of the Radius ID was compared")
			}
		})
	}
}

// The Radius provider's models are built once, not twice: reading the bundled catalog first cost a ToModel of every Radius catalog entry that the Radius branch then discarded.
func TestGetProviderModelDataDoesNotBuildTheBundledCatalogOfARadiusProvider(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "")
	registry, _, _ := radiusTestRegistry(t, "", nil)
	bundled := len(builtinChatCatalogListed(RadiusProviderID))
	if bundled == 0 || !registry.isRadiusCatalog(RadiusProviderID) {
		t.Fatal("the built-in Radius provider has no bundled catalog")
	}
	now := testing.AllocsPerRun(10, func() { registry.GetProviderModelData(RadiusProviderID) })
	before := testing.AllocsPerRun(10, func() { legacyProviderModelData(registry, RadiusProviderID) })
	if now > before-float64(bundled) {
		t.Fatalf("GetProviderModelData(%s) allocated %.0f, the bundled-catalog-first read %.0f; want at least one fewer per bundled model (%d)", RadiusProviderID, now, before, bundled)
	}
}
