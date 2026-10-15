package ai

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// Ports providers/{huggingface,opencode-go,xiaomi}.ts: each factory creates the provider with the pinned id, display
// name and API key label, and serves exactly the models of the matching providers/<id>.models.ts export.
func TestBuiltinProviderFactoriesMatchPinnedModules(t *testing.T) {
	idRe := regexp.MustCompile(`\bid: "([^"]+)"`)
	nameRe := regexp.MustCompile(`\bname: "([^"]+)"`)
	authRe := regexp.MustCompile(`envApiKeyAuth\("([^"]+)"`)
	for _, tc := range []struct {
		module   string
		factory  func() *ModelsProvider
		catalogs func() ChatModelCatalog
	}{
		{"huggingface", HuggingFaceProvider, HuggingFaceModels},
		{"opencode-go", OpenCodeGoProvider, OpenCodeGoModels},
		{"xiaomi", XiaomiProvider, XiaomiModels},
	} {
		t.Run(tc.module, func(t *testing.T) {
			source, err := os.ReadFile("../.upstream/current/packages/ai/src/providers/" + tc.module + ".ts")
			if err != nil {
				t.Fatal(err)
			}
			id, name, auth := idRe.FindSubmatch(source), nameRe.FindSubmatch(source), authRe.FindSubmatch(source)
			if id == nil || name == nil || auth == nil {
				t.Fatalf("%s.ts: id/name/auth not found", tc.module)
			}
			provider := tc.factory()
			if provider.ID != string(id[1]) || provider.Name != string(name[1]) {
				t.Errorf("provider = %q %q, upstream %q %q", provider.ID, provider.Name, id[1], name[1])
			}
			if provider.Auth.APIKey == nil || provider.Auth.APIKey.Name != string(auth[1]) {
				t.Errorf("api key auth = %+v, upstream %q", provider.Auth.APIKey, auth[1])
			}
			models, err := provider.GetModels()
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, model := range models {
				got = append(got, model.ID)
			}
			if want := tc.catalogs().IDs(); len(want) == 0 || !slices.Equal(got, want) {
				t.Errorf("models %v, upstream %v", got, want)
			}
		})
	}
}
