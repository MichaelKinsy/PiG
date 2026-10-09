package ai

import "testing"

// upstream: providers/{google,openai,together}.ts createProvider({id, name, models}): each provider serves exactly its catalog's chat models.
func TestGoogleOpenAITogetherProvidersServeTheirCatalogs(t *testing.T) {
	cases := []struct {
		build func() *ModelsProvider
		id    string
		name  string
	}{
		{GoogleProvider, "google", "Google"},
		{OpenAIProvider, "openai", "OpenAI"},
		{TogetherProvider, "together", "Together"},
	}
	for _, tc := range cases {
		provider := tc.build()
		if provider.ID != tc.id || provider.Name != tc.name {
			t.Errorf("%s: provider = %q %q, want %q %q", tc.id, provider.ID, provider.Name, tc.id, tc.name)
		}
		models, err := provider.GetModels()
		if err != nil {
			t.Fatalf("%s: GetModels: %v", tc.id, err)
		}
		want := ListModels(tc.id)
		if len(want) == 0 {
			t.Fatalf("%s: the catalog is empty", tc.id)
		}
		if len(models) != len(want) {
			t.Errorf("%s: serves %d models, catalog has %d", tc.id, len(models), len(want))
		}
		if provider.Auth.APIKey == nil {
			t.Errorf("%s: no api key auth", tc.id)
		}
	}
}
