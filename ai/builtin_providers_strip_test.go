package ai

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// resetCompatModels drops the built-in collection the compat streams cache, now and when the test ends, so that it is built under the test's strip state.
func resetCompatModels(t *testing.T) {
	reset := func() {
		compatRegistry.mu.Lock()
		compatRegistry.compatMod = nil
		compatRegistry.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// A stripped provider is absent from BuiltinProviders, BuiltinModels and the collection the compat streams consult, for each strippable API alone and for all of them together. The other providers are built in catalog order, a compat stream of a model on a stripped API ends with the strip error, and a compat stream of another API runs.
func TestStrippedProvidersAreNotBuilt(t *testing.T) {
	apis := pigstrip.Known(pigstrip.ListAPIs)
	sets := make([][]string, 0, len(apis)+1)
	for _, api := range apis {
		sets = append(sets, []string{api})
	}
	for _, set := range append(sets, apis) {
		t.Run(strings.Join(set, "+"), func(t *testing.T) {
			absent := BuiltStrippedProviders()
			for _, api := range set {
				t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, api))
				absent = append(absent, strippedAPIProviders[api])
			}
			resetCompatModels(t)
			want := slices.DeleteFunc(ListProviders(), func(id string) bool { return slices.Contains(absent, id) })
			var built []string
			for _, provider := range BuiltinProviders() {
				built = append(built, provider.ID)
			}
			if !slices.Equal(built, want) {
				t.Fatalf("BuiltinProviders() ids = %v\nwant the catalog without %v: %v", built, absent, want)
			}
			models := BuiltinModels()
			for _, id := range absent {
				if !panics(func() { builtinProviderConstructors[id]() }) {
					t.Errorf("the constructor of stripped provider %s built a provider", id)
				}
				if models.GetProvider(id) != nil || len(models.GetAllModels(id)) != 0 {
					t.Errorf("BuiltinModels() holds stripped provider %s with %d models", id, len(models.GetAllModels(id)))
				}
			}
			if got := len(models.GetProviders()); got != len(want) {
				t.Errorf("BuiltinModels() holds %d providers, want %d", got, len(want))
			}
			request := Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}
			for _, api := range set {
				index := slices.IndexFunc(GeneratedModels, func(model GeneratedModel) bool { return string(model.API) == api })
				if index < 0 {
					t.Fatalf("no catalog model on API %s", api)
				}
				model := GeneratedModels[index].ToModel()
				if builtinProviderForModel(model) != nil {
					t.Errorf("the compat streams found a built-in provider for %s/%s", modelProviderID(model), model.ModelID())
				}
				wantError := fmt.Sprintf("Provider %s: API %s is stripped from this Piglet (strip.apis: %s)", strippedAPIProviders[api], api, api)
				result, err := Complete(t.Context(), model, request, StreamOptions{APIKey: "k"})
				if got := compatFailure(result, err); got != wantError {
					t.Errorf("Complete(%s/%s) failed with %q, want %q", modelProviderID(model), model.ModelID(), got, wantError)
				}
			}
			anthropic := mustGeneratedModel(t, "anthropic", "claude-haiku-4-5").ToModel()
			if provider := builtinProviderForModel(anthropic); provider == nil || provider.ID != "anthropic" {
				t.Errorf("the compat streams found no built-in provider for an Anthropic model: %v", provider)
			}
		})
	}
	if len(BuiltinProviders()) != len(builtCatalogProviders()) {
		t.Fatalf("after the strips are undone BuiltinProviders() = %d providers, want %d", len(BuiltinProviders()), len(builtCatalogProviders()))
	}
}

// compatFailure is the error text a compat call ended with: the returned error or the final message's.
func compatFailure(result *AssistantMessage, err error) string {
	if err != nil {
		return err.Error()
	}
	if result == nil {
		return ""
	}
	return result.ErrorMessage
}
