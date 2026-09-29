package ai

import "testing"

// Pi's faux core returns canonical models whose provider field identifies the owning provider (packages/ai/src/providers/faux.ts:648-652).
func TestFauxCanonicalModelRetainsProviderIdentity(t *testing.T) {
	for _, providerID := range []string{"faux", "custom-faux"} {
		t.Run(providerID, func(t *testing.T) {
			provider := NewFauxProvider(FauxConfig{ProviderID: providerID, Models: []FauxModelDefinition{{ID: "first"}, {ID: "second"}}})
			for _, id := range []string{"first", "second"} {
				model := provider.GetModel(id)
				if model == nil || model.ProviderMeta.ProviderID != providerID || model.Provider.ID() != providerID {
					t.Fatalf("canonical %s model = %#v; want provider %q", id, model, providerID)
				}
				if provider.GetModel(id) != model {
					t.Fatal("canonical model identity changed")
				}
			}
		})
	}
}
