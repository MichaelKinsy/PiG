package ai

import "testing"

// Pi providers/all.ts getBuiltinModels: a provider's catalog models, nothing for an unknown or empty provider.
func TestGetBuiltinModelsReadsCatalogLikeUpstream(t *testing.T) {
	listed := 0
	for _, provider := range ListProviders() {
		for _, model := range GetBuiltinModels(provider) {
			if model.ProviderMeta.ProviderID != provider {
				t.Fatalf("%s lists a model of %q", provider, model.ProviderMeta.ProviderID)
			}
			listed++
		}
	}
	if listed == 0 {
		t.Fatal("catalog has no chat models")
	}
	if got := GetBuiltinModels("no-such-provider"); len(got) != 0 {
		t.Fatalf("unknown provider models = %d", len(got))
	}
	if got := GetBuiltinModels(""); len(got) != 0 {
		t.Fatalf("empty provider listed %d models", len(got))
	}
}
