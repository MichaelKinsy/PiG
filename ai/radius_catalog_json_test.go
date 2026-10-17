package ai

import "testing"

// Pi's Radius baseline models are `{ ...model, provider: id }` of the radius catalog entries (radius.ts baselineModels),
// so their RPC objects are the entries' own JSON, lab, enabled and providers included. The provider's models are
// clones (cloneRadiusModel), which keep the record they came from.
func TestRadiusBaselineModelsKeepTheirCatalogJSON(t *testing.T) {
	records := map[string]string{}
	for _, generated := range GeneratedModels {
		if generated.Provider == RadiusProviderID {
			records[generated.ID] = generated.Record
		}
	}
	models := NewRadiusGatewayProvider(RadiusProviderOptions{}).GetModels()
	if len(models) == 0 || len(models) != len(records) {
		t.Fatalf("%d baseline models, %d catalog entries", len(models), len(records))
	}
	for _, model := range models {
		data, ok, err := CatalogModelJSON(model)
		if err != nil || !ok || string(data) != records[model.ID] {
			t.Errorf("%s: CatalogModelJSON = %s (ok %t, %v), want the catalog entry %s", model.ID, data, ok, err, records[model.ID])
		}
		if got := CatalogRecordOf(model); got != records[model.ID] {
			t.Errorf("%s: CatalogRecordOf = %.80s, want the catalog entry", model.ID, got)
		}
	}
}
