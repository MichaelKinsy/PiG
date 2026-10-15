package ai

import (
	"encoding/json"
	"slices"
	"testing"
)

func copilotFilterIDs(models []*Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// Mirrors github-copilot.ts filterModels: an OAuth credential whose availableModelIds is an array of strings keeps only those models; everything else keeps every model.
func TestGitHubCopilotProviderFiltersModelsByTheAccountsAvailableModelIDs(t *testing.T) {
	provider := builtinProvider("github-copilot")
	if provider.FilterModels == nil {
		t.Fatal("github-copilot has no filterModels")
	}
	models := []*Model{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	oauth := func(available string) *Credential {
		credential := &Credential{Type: CredentialOAuth}
		if available != "" {
			credential.AvailableModelIDs = json.RawMessage(available)
		}
		return credential
	}
	for _, tc := range []struct {
		name       string
		credential *Credential
		want       []string
	}{
		{"no credential", nil, []string{"a", "b", "c"}},
		{"api key credential", &Credential{Type: CredentialAPIKey, AvailableModelIDs: json.RawMessage(`["a"]`)}, []string{"a", "b", "c"}},
		{"oauth without the list", oauth(""), []string{"a", "b", "c"}},
		{"listed models", oauth(`["c","a","unknown"]`), []string{"a", "c"}},
		{"explicit empty list", oauth(`[]`), []string{}},
		{"null", oauth(`null`), []string{"a", "b", "c"}},
		{"not an array", oauth(`"a"`), []string{"a", "b", "c"}},
		{"non-string element", oauth(`["a",1]`), []string{"a", "b", "c"}},
		{"null element", oauth(`["a",null]`), []string{"a", "b", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := copilotFilterIDs(provider.FilterModels(slices.Clone(models), tc.credential))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("models = %v, want %v", got, tc.want)
			}
		})
	}
	if len(models) != 3 || models[0].ID != "a" || models[2].ID != "c" {
		t.Fatalf("filter modified its input: %v", copilotFilterIDs(models))
	}
	if builtinProvider("openai").FilterModels != nil {
		t.Fatal("only github-copilot filters models")
	}
}
