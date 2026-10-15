package ai

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// strippedAPICatalogCases are the strippable APIs with one catalog model each: bedrock-converse-stream (amazon-bedrock),
// mistral-conversations (mistral) and google-vertex (google-vertex), each the only API of its provider.
var strippedAPICatalogCases = []struct {
	api             API
	provider, model string
}{
	{APIBedrockConverseStream, "amazon-bedrock", "amazon.nova-2-lite-v1:0"},
	{APIMistralConversations, "mistral", "codestral-latest"},
	{APIGoogleVertex, "google-vertex", "gemini-2.5-flash"},
}

// A stripped API's models are not offered: the catalog listing and every Models collection read omit them, while an
// exact lookup still finds one so that using it reports the strip. Models of other APIs stay offered.
func TestStrippedAPIModelsAreNotOffered(t *testing.T) {
	for _, tc := range strippedAPICatalogCases {
		t.Run(string(tc.api), func(t *testing.T) {
			if !pigstrip.Has(pigstrip.ListAPIs, string(tc.api)) && len(ListModels(tc.provider)) == 0 {
				t.Fatalf("unstripped ListModels(%s) is empty", tc.provider)
			}
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			if got := ListModels(tc.provider); len(got) != 0 {
				t.Fatalf("ListModels(%s) lists %d models of stripped %s", tc.provider, len(got), tc.api)
			}
			all := ListModels("")
			if slices.ContainsFunc(all, func(model GeneratedModel) bool { return model.API == tc.api }) {
				t.Fatalf("ListModels(\"\") lists a model of stripped %s", tc.api)
			}
			if !slices.ContainsFunc(all, func(model GeneratedModel) bool { return model.Provider == "anthropic" }) {
				t.Fatalf("ListModels(\"\") dropped anthropic with %s stripped", tc.api)
			}
			if _, ok := LookupModelExact(tc.provider + "/" + tc.model); !ok {
				t.Fatalf("LookupModelExact(%s/%s) lost the stripped model; using it must report the strip", tc.provider, tc.model)
			}

			kept := &Model{ID: "kept", ProviderMeta: ProviderMetadata{ProviderID: "custom", API: APIOpenAICompletions}}
			stripped := &Model{ID: "stripped", ProviderMeta: ProviderMetadata{ProviderID: "custom", API: tc.api}}
			models := CreateModels()
			check := func(context.Context, APIKeyAuthInput) (*AuthCheck, error) {
				return &AuthCheck{Type: CredentialAPIKey}, nil
			}
			models.SetProvider(&ModelsProvider{
				ID:        "custom",
				Auth:      ProviderAuth{APIKey: &APIKeyAuth{Name: "Custom", Check: check}},
				GetModels: func() ([]*Model, error) { return []*Model{kept, stripped}, nil },
			})
			wantIDs := []string{"kept"}
			if got := chatModelIDs(AnyModels(models.GetModels())); !slices.Equal(got, wantIDs) {
				t.Fatalf("GetModels() = %v, want %v", got, wantIDs)
			}
			if got := chatModelIDs(models.GetAllModels("custom")); !slices.Equal(got, wantIDs) {
				t.Fatalf("GetAllModels(custom) = %v, want %v", got, wantIDs)
			}
			if models.GetModel("custom", "stripped") != nil {
				t.Fatal("GetModel(custom, stripped) found a model of a stripped API")
			}
			available, err := models.GetAvailable(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got := chatModelIDs(AnyModels(available)); !slices.Equal(got, wantIDs) {
				t.Fatalf("GetAvailable() = %v, want %v", got, wantIDs)
			}
			allAvailable, err := models.GetAllAvailable(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got := chatModelIDs(allAvailable); !slices.Equal(got, wantIDs) {
				t.Fatalf("GetAllAvailable() = %v, want %v", got, wantIDs)
			}
			models.operations.Wait()
		})
	}
}

func chatModelIDs(models []AnyModel) []string {
	var ids []string
	for _, model := range models {
		ids = append(ids, model.ModelID())
	}
	return ids
}

// StrippedModelError names the strip of a catalog model, or of a provider whose catalog models are all on stripped
// APIs, so that `--model` resolution reports the strip for a model the catalog no longer offers.
func TestStrippedModelErrorNamesTheStrip(t *testing.T) {
	for _, tc := range strippedAPICatalogCases {
		t.Run(string(tc.api), func(t *testing.T) {
			if !pigstrip.Has(pigstrip.ListAPIs, string(tc.api)) {
				if err := StrippedModelError(tc.provider, tc.model); err != nil {
					t.Fatalf("unstripped StrippedModelError = %v", err)
				}
			}
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			want := "Provider " + tc.provider + ": API " + string(tc.api) + " is stripped from this Piglet (strip.apis: " + string(tc.api) + ")"
			for _, query := range [][2]string{{tc.provider, tc.model}, {tc.provider, "not-in-the-catalog"}, {"", tc.model}} {
				if err := StrippedModelError(query[0], query[1]); err == nil || err.Error() != want {
					t.Fatalf("StrippedModelError(%q, %q) = %v, want %q", query[0], query[1], err, want)
				}
			}
			if err := StrippedModelError("anthropic", "claude-haiku-4-5"); err != nil {
				t.Fatalf("StrippedModelError(anthropic) with %s stripped = %v", tc.api, err)
			}
			if err := StrippedModelError("", "no-such-model"); err != nil {
				t.Fatalf("StrippedModelError(no-such-model) = %v", err)
			}
		})
	}
}
