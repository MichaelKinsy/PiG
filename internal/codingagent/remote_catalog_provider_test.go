package codingagent

import (
	"net/http"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi builds the catalog request as new URL(`/api/models/providers/${encodeURIComponent(provider.id)}`, catalogBaseUrl)
// (remote-catalog-provider.ts:105): the absolute route replaces the base URL's path and query, and the provider id is escaped as a
// URI component.
func TestRemoteCatalogRouteResolvesAgainstTheBaseURLOrigin(t *testing.T) {
	for _, tc := range []struct{ name, suffix string }{
		{"origin", ""},
		{"base path", "/catalog/v2/"},
		{"base path and query", "/catalog?token=x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newCatalogServer(t, true, catalogResponse{status: http.StatusNotImplemented})
			provider := remoteCatalogTestProvider(server.URL+tc.suffix, nil)
			requireNoRefreshError(t, refreshCatalogProvider(t, provider, ai.NewInMemoryModelsStore(), refreshOverrides{}))
			request := server.request(0)
			if request.path != "/api/models/providers/test-provider" {
				t.Fatalf("path = %q, want /api/models/providers/test-provider", request.path)
			}
			if got := request.query.Encode(); got != "types=chat%2Cimage%2Cclassifier" {
				t.Fatalf("query = %q, want only the types parameter", got)
			}
		})
	}
}

func TestEncodeURIComponentMatchesJavaScript(t *testing.T) {
	// Expected values are JavaScript's encodeURIComponent results.
	for input, want := range map[string]string{
		"openai":           "openai",
		"llama.cpp":        "llama.cpp",
		"a b/c?d#e":        "a%20b%2Fc%3Fd%23e",
		"-_.!~*'()":        "-_.!~*'()",
		":@&=+$,;":         "%3A%40%26%3D%2B%24%2C%3B",
		"caf\u00e9":        "caf%C3%A9",
		"100%":             "100%25",
		"\U0001F600 emoji": "%F0%9F%98%80%20emoji",
	} {
		if got := encodeURIComponent(input); got != want {
			t.Errorf("encodeURIComponent(%q) = %q, want %q", input, got, want)
		}
	}
}

// A catalog body that is not JSON rejects the refresh with JSON.parse's SyntaxError (remote-catalog-provider.ts:144 response.json()), and
// valid JSON that is not a catalog with parseCatalog's message (:47). Expected messages are node's.
func TestRemoteCatalogRejectsBodiesThatAreNotCatalogs(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"<html>", `Unexpected token '<', "<html>" is not valid JSON`},
		{"", "Unexpected end of JSON input"},
		{"[1,", "Unexpected end of JSON input"},
		{"null", `Invalid model catalog for provider "test-provider"`},
		{"42", `Invalid model catalog for provider "test-provider"`},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := newCatalogServer(t, true, catalogResponse{status: http.StatusOK, body: tc.body})
			provider := remoteCatalogTestProvider(server.URL, nil)
			err := refreshCatalogProvider(t, provider, ai.NewInMemoryModelsStore(), refreshOverrides{})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("refresh error = %v, want %q", err, tc.want)
			}
		})
	}
}

// Pi's mergeModels (remote-catalog-provider.ts, 0.99.2) writes the baseline and then the dynamic models into one Map keyed by
// type and id: a later model replaces the earlier one of the same key at the earlier position, a model of another type with
// the same id is a different key, and a repeated baseline entry collapses to one.
func TestMergeRemoteCatalogModelsKeysByTypeAndIDInFirstSeenOrder(t *testing.T) {
	chat := func(id, name string) *ai.Model { return &ai.Model{ID: id, DisplayName: name} }
	image := func(id, name string) *ai.ImageModel { return &ai.ImageModel{ID: id, Name: name} }
	baseline := []ai.AnyModel{chat("a", "base-a"), image("a", "base-image-a"), chat("b", "base-b"), chat("a", "base-a-again")}
	dynamic := []ai.AnyModel{chat("b", "dyn-b"), chat("c", "dyn-c"), image("a", "dyn-image-a")}

	var got []string
	for _, model := range mergeRemoteCatalogModels(baseline, dynamic) {
		got = append(got, string(ai.GetModelType(model))+"/"+model.ModelID()+"="+modelDisplayName(model))
	}
	want := []string{"chat/a=base-a-again", "image/a=dyn-image-a", "chat/b=dyn-b", "chat/c=dyn-c"}
	if !slices.Equal(got, want) {
		t.Fatalf("merged = %q, want %q", got, want)
	}
}

func modelDisplayName(model ai.AnyModel) string {
	switch m := model.(type) {
	case *ai.Model:
		return m.DisplayName
	case *ai.ImageModel:
		return m.Name
	}
	return ""
}
