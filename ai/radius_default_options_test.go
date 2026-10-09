package ai

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// radius.ts:22 radiusProvider(options = {}): calling it with no options is calling it with the empty options object. A second options value is not Pi's shape, so the first one is the one used.
func TestNewRadiusProviderWithoutOptionsIsTheEmptyOptions(t *testing.T) {
	bare := NewRadiusProvider()
	empty := NewRadiusProvider(RadiusProviderOptions{})
	if bare.ID != RadiusProviderID || bare.Name != "Radius" || bare.ID != empty.ID || bare.Name != empty.Name {
		t.Fatalf("bare id=%q name=%q, empty id=%q name=%q", bare.ID, bare.Name, empty.ID, empty.Name)
	}
	bareModels, emptyModels := radiusProviderModels(t, bare), radiusProviderModels(t, empty)
	if len(bareModels) == 0 || !reflect.DeepEqual(radiusModelIDs(bareModels), radiusModelIDs(emptyModels)) {
		t.Fatalf("the default gateway ships the published baseline: %v vs %v", radiusModelIDs(bareModels), radiusModelIDs(emptyModels))
	}
	custom := NewRadiusProvider(RadiusProviderOptions{ID: "gw", Name: "Gateway"})
	if custom.ID != "gw" || custom.Name != "Gateway" {
		t.Fatalf("options still apply: id=%q name=%q", custom.ID, custom.Name)
	}
}

func radiusProviderModels(t *testing.T, provider *ModelsProvider) []*Model {
	t.Helper()
	models, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	return models
}

func radiusModelIDs(models []*Model) []string {
	ids := make([]string, len(models))
	for i, model := range models {
		ids[i] = model.ID
	}
	return ids
}

// The pinned pi-ai answers radiusProvider(), radiusProvider({}) and radiusProvider({id, name}); the Go provider built the same ways agrees.
func TestNewRadiusProviderMatchesPiForOptionalOptions(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "node", "testdata/radius_default_options.mjs", pigversion.UpstreamVersion)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v", err)
	}
	type described struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Models int    `json:"models"`
	}
	var want map[string]described
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for name, provider := range map[string]*ModelsProvider{"bare": NewRadiusProvider(), "empty": NewRadiusProvider(RadiusProviderOptions{}), "custom": NewRadiusProvider(RadiusProviderOptions{ID: "gw", Name: "Gateway"})} {
		got := described{provider.ID, provider.Name, len(radiusProviderModels(t, provider))}
		if got != want[name] {
			t.Errorf("%s: %+v, Pi %+v", name, got, want[name])
		}
	}
}
