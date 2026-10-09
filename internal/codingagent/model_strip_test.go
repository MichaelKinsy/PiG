package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// stripResolverRuntime resolves against the registry's composed runtime models, as startup does.
type stripResolverRuntime struct{ registry *ModelRegistry }

func (rt stripResolverRuntime) GetModels(providerID string) []RuntimeModel {
	return slices.DeleteFunc(rt.registry.RuntimeModels(), func(model RuntimeModel) bool { return providerID != "" && model.Provider != providerID })
}

func (rt stripResolverRuntime) HasConfiguredAuth(providerID string) bool {
	return rt.registry.HasConfiguredAuth(providerID)
}

// A stripped API's models leave every view of the composed catalog: catalog providers whose only API is stripped,
// and models.json providers on a stripped API. Naming one with --model or --provider reports the strip, as using one
// does. Models of other APIs stay offered.
func TestModelRegistryOmitsStrippedAPIModels(t *testing.T) {
	for _, tc := range []struct {
		api             ai.API
		provider, model string
		env             map[string]string
	}{
		{ai.APIBedrockConverseStream, "amazon-bedrock", "amazon.nova-2-lite-v1:0", map[string]string{"AWS_ACCESS_KEY_ID": "x", "AWS_SECRET_ACCESS_KEY": "y", "AWS_REGION": "us-east-1"}},
		{ai.APIMistralConversations, "mistral", "codestral-latest", map[string]string{"MISTRAL_API_KEY": "x"}},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			dir := t.TempDir()
			modelsJSON := `{"providers": {
  "custom-stripped": {"baseUrl": "http://127.0.0.1:9", "api": "` + string(tc.api) + `", "apiKey": "x", "models": [{"id": "stripped-custom"}]},
  "custom-kept": {"baseUrl": "http://127.0.0.1:9", "api": "openai-completions", "apiKey": "x", "models": [{"id": "kept-custom"}]}
}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			registry := NewModelRegistry(dir)
			if loadErr := registry.LoadError(); loadErr != "" {
				t.Fatalf("models.json: %s", loadErr)
			}
			views := func() map[string][]string {
				refs := map[string][]string{}
				for _, model := range registry.GetAllModelData() {
					refs["GetAllModelData"] = append(refs["GetAllModelData"], model.ProviderMeta.ProviderID+"/"+model.ID)
				}
				for _, entry := range registry.GetAvailable() {
					refs["GetAvailable"] = append(refs["GetAvailable"], entry.ProviderID+"/"+entry.ModelID)
				}
				for _, entry := range registry.GetAll() {
					refs["GetAll"] = append(refs["GetAll"], entry.ProviderID+"/"+entry.ModelID)
				}
				for _, model := range registry.RuntimeModels() {
					refs["RuntimeModels"] = append(refs["RuntimeModels"], model.Provider+"/"+model.ID)
				}
				return refs
			}
			catalogRef, customRef := tc.provider+"/"+tc.model, "custom-stripped/stripped-custom"
			if !pigstrip.Has(pigstrip.ListAPIs, string(tc.api)) {
				before := views()
				if !slices.Contains(before["GetAllModelData"], catalogRef) || !slices.Contains(before["RuntimeModels"], catalogRef) || !slices.Contains(before["GetAvailable"], customRef) {
					t.Fatalf("unstripped views miss %s or %s: %v", catalogRef, customRef, before)
				}
			}
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			for view, refs := range views() {
				for _, ref := range refs {
					if provider, _, _ := strings.Cut(ref, "/"); provider == tc.provider || provider == "custom-stripped" {
						t.Errorf("%s offers %s with %s stripped", view, ref, tc.api)
					}
				}
				if view != "GetAll" && !slices.Contains(refs, "custom-kept/kept-custom") {
					t.Errorf("%s dropped custom-kept/kept-custom with %s stripped: %v", view, tc.api, refs)
				}
			}

			want := "Provider " + tc.provider + ": API " + string(tc.api) + " is stripped from this Piglet (strip.apis: " + string(tc.api) + ")"
			runtime := stripResolverRuntime{registry}
			for _, selection := range [][2]string{{"", catalogRef}, {tc.provider, tc.model}, {tc.provider, catalogRef}, {tc.provider, "not-in-the-catalog"}} {
				if got := ResolveCliModel(selection[0], selection[1], "", runtime); got.Model != nil || got.Error != want {
					t.Errorf("ResolveCliModel(%q, %q) = model %v, error %q; want %q", selection[0], selection[1], got.Model, got.Error, want)
				}
			}
			if got := ResolveCliModel("", "custom-kept/kept-custom", "", runtime); got.Model == nil || got.Error != "" {
				t.Errorf("ResolveCliModel(custom-kept/kept-custom) = %+v with %s stripped", got, tc.api)
			}
		})
	}
}

// A models.json custom model under a built-in provider whose API is stripped takes its base URL from the full catalog, as
// in Stock PiG (provider-composer.ts modelFromJson findModelDefaults): the offered catalog omits that provider's models,
// but the strip must not turn the entry into a "baseUrl is required" models.json error. cmd/pig
// TestStrippedProviderCustomModelIsNeitherOfferedNorALoadError pins that the model stays unoffered.
func TestModelsJSONCustomModelUnderAStrippedProviderIsNoBaseURLError(t *testing.T) {
	for _, tc := range []struct {
		api      ai.API
		provider string
	}{
		{ai.APIBedrockConverseStream, "amazon-bedrock"},
		{ai.APIMistralConversations, "mistral"},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			dir := t.TempDir()
			modelsJSON := `{"providers": {"` + tc.provider + `": {"apiKey": "x", "models": [{"id": "my-custom"}]}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			registry := NewModelRegistry(dir)
			if loadErr := registry.LoadError(); loadErr != "" {
				t.Fatalf("models.json with %s stripped: %s", tc.api, loadErr)
			}
		})
	}
}
