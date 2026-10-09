package coding

import "testing"

// model-runtime.ts getModels(providerId?) returns every model without an id, only that provider's models with one, and none for an unknown provider.
func TestModelRuntimeGetModelsFiltersByProvider(t *testing.T) {
	runtime := newTestServices(t).ModelRuntime()
	all := runtime.GetModels()
	byProvider := map[string]int{}
	for _, model := range all {
		byProvider[model.ProviderMeta.ProviderID]++
	}
	if len(byProvider) < 2 {
		t.Fatalf("catalog has %d providers; the test needs at least two", len(byProvider))
	}
	for providerID, want := range byProvider {
		got := runtime.GetModels(providerID)
		if len(got) != want {
			t.Fatalf("GetModels(%q) = %d models, want %d", providerID, len(got), want)
		}
		for _, model := range got {
			if model.ProviderMeta.ProviderID != providerID {
				t.Fatalf("GetModels(%q) returned %s/%s", providerID, model.ProviderMeta.ProviderID, model.ID)
			}
		}
	}
	if got := runtime.GetModels("no-such-provider"); len(got) != 0 {
		t.Fatalf("unknown provider returned %d models", len(got))
	}
}
