package ai

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// strippedAPIProviders names the catalog provider each strippable API serves alone: stripping the API leaves that provider without a model.
var strippedAPIProviders = map[string]string{
	string(APIBedrockConverseStream): "amazon-bedrock",
	string(APIGoogleVertex):          "google-vertex",
	string(APIMistralConversations):  "mistral",
}

// BuiltStrippedAPIs lists the model APIs this test binary is built without: the API of each pig_strip_<api> build tag, from the tagged twins strip_built_<api>_test.go and strip_built_<api>_stripped_test.go. It is empty in the default build.
func BuiltStrippedAPIs() []string {
	return slices.Concat(bedrockBuiltStripped, googleVertexBuiltStripped, mistralBuiltStripped)
}

// BuiltStrippedProviders lists the catalog providers this test binary is built without: the provider of each of BuiltStrippedAPIs.
func BuiltStrippedProviders() []string {
	var providers []string
	for _, api := range BuiltStrippedAPIs() {
		providers = append(providers, strippedAPIProviders[api])
	}
	return providers
}

// BuiltAPI reports whether this test binary links api: it is not one of BuiltStrippedAPIs.
func BuiltAPI(api API) bool { return !slices.Contains(BuiltStrippedAPIs(), string(api)) }

// strippedAPIMessage is the error a request for provider's api reports in a build that strips the API.
func strippedAPIMessage(provider string, api API) string {
	return fmt.Sprintf("Provider %s: API %s is stripped from this Piglet (strip.apis: %s)", provider, api, api)
}

// BuiltCatalogModel reports whether this test binary links the API of the catalog model named provider/id. An unknown model counts as built, so the test that names it fails on its own lookup.
func BuiltCatalogModel(spec string) bool {
	model, ok := LookupModelExact(spec)
	return !ok || BuiltAPI(model.API)
}

// panics reports whether call panics.
func panics(call func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	call()
	return false
}

// builtCatalogProviders is ListProviders without BuiltStrippedProviders: the providers BuiltinProviders builds in this test binary, in catalog order.
func builtCatalogProviders() []string {
	stripped := BuiltStrippedProviders()
	return slices.DeleteFunc(ListProviders(), func(id string) bool { return slices.Contains(stripped, id) })
}

// The tagged twins agree with the strip state the build recorded: an API is in BuiltStrippedAPIs exactly when it is stripped at start, and its provider is then stripped.
func TestBuiltStrippedProvidersMatchTheStripState(t *testing.T) {
	for _, api := range pigstrip.Known(pigstrip.ListAPIs) {
		provider := strippedAPIProviders[api]
		if provider == "" {
			t.Fatalf("strippable API %s has no provider in strippedAPIProviders", api)
		}
		if stripped, listed := pigstrip.Has(pigstrip.ListAPIs, api), slices.Contains(BuiltStrippedAPIs(), api); stripped != listed {
			t.Errorf("API %s stripped = %v, in BuiltStrippedAPIs = %v", api, stripped, listed)
		}
		if got, want := ProviderStripped(provider), pigstrip.Has(pigstrip.ListAPIs, api); got != want {
			t.Errorf("ProviderStripped(%s) = %v, want %v", provider, got, want)
		}
	}
}
