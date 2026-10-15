package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A build that strips an API offers none of its models (D92, ai.OfferedModels). The available catalog of a plain bundled provider must
// leave them out as the full catalog does, and must not check the auth of a provider whose every model is stripped.
func TestGetAvailableModelDataOmitsTheModelsOfAStrippedAPI(t *testing.T) {
	// Chosen from the catalog: a plain bundled provider with two APIs (one stripped, one offered) and another plain provider whose every
	// model is on the stripped API.
	registry := NewModelRegistry(t.TempDir())
	apisOf := map[string]map[ai.API]bool{}
	var order []string
	for _, model := range ai.GeneratedModels {
		if _, plain := registry.plainGeneratedModels(model.Provider); !plain {
			continue
		}
		if apisOf[model.Provider] == nil {
			apisOf[model.Provider] = map[ai.API]bool{}
			order = append(order, model.Provider)
		}
		apisOf[model.Provider][model.API] = true
	}
	var partly, wholly string
	var strip ai.API
	for _, id := range order {
		if len(apisOf[id]) < 2 {
			continue
		}
		for api := range apisOf[id] {
			for _, other := range order {
				if other != id && len(apisOf[other]) == 1 && apisOf[other][api] && (strip == "" || api < strip) {
					partly, wholly, strip = id, other, api
				}
			}
		}
		if partly != "" {
			break
		}
	}
	if partly == "" {
		t.Fatal("the catalog has no plain provider pair for the test")
	}
	t.Logf("strip %s: %s keeps another API, %s loses every model", strip, partly, wholly)
	defer pigstrip.Strip(pigstrip.ListAPIs, string(strip))()

	for _, id := range []string{partly, wholly} {
		registry.SetRuntimeAPIKey(id, "key-"+id)
	}
	var want []*ai.Model
	for _, model := range registry.GetAllModelData() {
		if registry.HasConfiguredAuth(model.ProviderMeta.ProviderID) {
			want = append(want, model)
		}
	}
	got := registry.GetAvailableModelData()
	for _, model := range got {
		if model.ProviderMeta.API == strip || model.ProviderMeta.ProviderID == wholly {
			t.Fatalf("available catalog offers %s/%s on stripped API %s", model.ProviderMeta.ProviderID, model.ID, model.ProviderMeta.API)
		}
	}
	if len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("available models (%d) differ from the configured providers' offered models (%d)", len(got), len(want))
	}
}

// The available catalog interleaves plain bundled providers (built on demand) with composed ones in the provider order of the full catalog.
// The middle of three keyed bundled providers is composed by a models.json baseUrl override (provider-composer.ts applies it to the built-in models).
func TestGetAvailableModelDataKeepsProviderOrderAcrossPlainAndComposedCatalogs(t *testing.T) {
	keyed := func(id string) bool {
		probe := NewModelRegistry(t.TempDir())
		probe.SetRuntimeAPIKey(id, "key-"+id)
		return probe.HasConfiguredAuth(id) && len(probe.GetAvailableModelData()) > 0
	}
	var ids []string
	for _, id := range NewModelRegistry(t.TempDir()).modelProviderIDs() {
		if len(ids) < 3 && keyed(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) < 3 {
		t.Fatalf("the catalog has %d bundled providers a runtime key configures, want 3", len(ids))
	}
	dir := t.TempDir()
	const baseURL = "https://composed.example/v1"
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"`+ids[1]+`":{"baseUrl":"`+baseURL+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	for i, id := range ids {
		if _, plain := registry.plainGeneratedModels(id); plain != (i != 1) {
			t.Fatalf("provider %s plain = %v", id, plain)
		}
	}
	for _, id := range []string{ids[2], ids[1], ids[0]} {
		registry.SetRuntimeAPIKey(id, "key-"+id)
	}
	var want []*ai.Model
	for _, model := range registry.GetAllModelData() {
		if registry.HasConfiguredAuth(model.ProviderMeta.ProviderID) {
			want = append(want, model)
		}
	}
	got := registry.GetAvailableModelData()
	var order []string
	for _, model := range got {
		if id := model.ProviderMeta.ProviderID; len(order) == 0 || order[len(order)-1] != id {
			order = append(order, id)
		}
		if model.ProviderMeta.ProviderID == ids[1] && model.ProviderMeta.BaseURL != baseURL {
			t.Fatalf("composed provider %s model %s base URL = %q, want the models.json override", ids[1], model.ID, model.ProviderMeta.BaseURL)
		}
	}
	if !slices.Equal(order, ids) {
		t.Fatalf("available provider order = %v, want %v", order, ids)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("available catalog (%d) differs from the full catalog's configured providers (%d)", len(got), len(want))
	}
}
