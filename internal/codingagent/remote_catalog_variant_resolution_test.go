package codingagent

// D199 (docs/parity/divergences/D199.md): "A variant is a copy of its base entry in everything but its id, name and JSON record: API, base URL, prices, limits, input types, reasoning and thinking levels are the base model's."
// Pi lists no routing variant, so the record is the oracle. Where the base is a record of the
// user's personalized catalog, the variant's startup entry is built from that record, not from the bundled catalog (a stale window and price, or no model).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// personalizedModel is a model of the saved list of provider with its own window and prices.
func personalizedModel(provider, api, baseURL, id string, window int, input float64) *ai.Model {
	return &ai.Model{
		ID: id, DisplayName: "Saved " + id, Input: []string{"text"},
		ProviderMeta: ai.ProviderMetadata{ProviderID: provider, API: ai.API(api), BaseURL: baseURL},
		Capabilities: ai.ModelCapabilities{ContextWindow: window, MaxOutputTokens: 100, InputCostPer1M: input, OutputCostPer1M: input * 2},
	}
}

func TestResolveRemoteCatalogModelResolvesAVariantFromItsRemoteBaseRecord(t *testing.T) {
	const bundledBase = "openai/gpt-5.2"
	if _, ok := ai.LookupModelExact("openrouter/" + bundledBase); !ok {
		t.Fatalf("the bundled catalog has no openrouter/%s", bundledBase)
	}
	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{openRouterProviderID, "github-copilot"} {
		if err := auth.Set(provider, ai.Credential{Type: ai.CredentialAPIKey, Key: "fixture-key"}); err != nil {
			t.Fatal(err)
		}
	}
	store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
	const openRouterAPI, openRouterURL = "openai-completions", "https://openrouter.ai/api/v1"
	if err := store.Write(context.Background(), openRouterProviderID, ai.ModelsStoreEntry{CheckedAt: new(float64(1)), Models: []ai.AnyModel{
		personalizedModel(openRouterProviderID, openRouterAPI, openRouterURL, "vendor/base", 4321, 7),
		personalizedModel(openRouterProviderID, openRouterAPI, openRouterURL, bundledBase, 1234, 3),
		personalizedModel(openRouterProviderID, openRouterAPI, openRouterURL, "vendor/base:free", 999, 0),
	}}); err != nil {
		t.Fatal(err)
	}
	// A provider whose remote catalog only adds models: an id that ends like a variant is not one there.
	if err := store.Write(context.Background(), "github-copilot", ai.ModelsStoreEntry{CheckedAt: new(float64(1)), LastModified: new(*ai.GetBuiltinModelDataGeneratedAt() + 1), Models: []ai.AnyModel{
		personalizedModel("github-copilot", "anthropic-messages", "https://api.individual.githubcopilot.com", "remote-only", 555, 5),
	}}); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	registry.SetAuthStorage(auth)
	registry.SetModelsStore(store)
	if result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: false, Providers: []string{openRouterProviderID, "github-copilot"}}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("restore = %+v", result)
	}

	cases := []struct {
		name, provider, id string
		found              bool
		window             int
		input              float64
	}{
		// Pi lists no variant (D199), so the base rows are Pi's own; the variants repeat their base.
		{"base only the saved list carries", openRouterProviderID, "vendor/base", true, 4321, 7},
		{"nitro of a base only the saved list carries", openRouterProviderID, "vendor/base:nitro", true, 4321, 7},
		{"floor of a base only the saved list carries", openRouterProviderID, "vendor/base:floor", true, 4321, 7},
		{"base the saved list replaces", openRouterProviderID, bundledBase, true, 1234, 3},
		{"nitro of a base the saved list replaces", openRouterProviderID, bundledBase + ":nitro", true, 1234, 3},
		{"floor of a base the saved list replaces", openRouterProviderID, bundledBase + ":floor", true, 1234, 3},
		// A suffix OpenRouter lists as part of a model's own id is not a routing variant: the saved record answers an exact id only.
		{"listed :free id", openRouterProviderID, "vendor/base:free", true, 999, 0},
		{"online suffix is no variant", openRouterProviderID, "vendor/base:online", false, 0, 0},
		{"extended suffix is no variant", openRouterProviderID, "vendor/base:extended", false, 0, 0},
		{"nitro of a base the saved list lacks", openRouterProviderID, "vendor/absent:nitro", false, 0, 0},
		// Another provider's model, and a variant-shaped id on it, resolve as before.
		{"other provider remote-only model", "github-copilot", "remote-only", true, 555, 5},
		{"other provider id ending :nitro", "github-copilot", "remote-only:nitro", false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry, found := registry.ResolveRemoteCatalogModel(tc.provider, tc.id)
			if found != tc.found {
				t.Fatalf("ResolveRemoteCatalogModel(%s, %s) found = %v, want %v", tc.provider, tc.id, found, tc.found)
			}
			if !found {
				return
			}
			if entry.ProviderID != tc.provider || entry.ModelID != tc.id || entry.ContextWindow != tc.window || entry.InputCost != tc.input {
				t.Errorf("entry %s/%s window=%d input=%v, want %s/%s window=%d input=%v", entry.ProviderID, entry.ModelID, entry.ContextWindow, entry.InputCost, tc.provider, tc.id, tc.window, tc.input)
			}
		})
	}
}
