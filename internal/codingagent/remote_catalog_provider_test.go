package codingagent

// pi: packages/coding-agent/src/core/remote-catalog-provider.ts

import (
	"encoding/json"
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

// remote-catalog-provider.ts:139-146 stores Date.parse(last-modified): any date text V8 reads is a time, and text it does not read is 0.
func TestRemoteCatalogStoresTheLastModifiedHeaderAsDateParseReadsIt(t *testing.T) {
	for header, want := range map[string]float64{
		"Thu, 01 Jan 2099 00:00:00 GMT":    4070908800000,
		"2099-01-01T00:00:00Z":             4070908800000,
		"2099-01-01T00:00:00.5+01:00":      4070905200500,
		"Thu, 01 Jan 2099 01:00:00 +0100":  4070908800000,
		"Thursday, 01-Jan-99 00:00:00 GMT": 915148800000,
		"not a date":                       0,
		"":                                 0,
	} {
		t.Run(header, func(t *testing.T) {
			headers := map[string]string{}
			if header != "" {
				headers["last-modified"] = header
			}
			server := newCatalogServer(t, false, catalogJSONResponse(headers, map[string]any{"dynamic": catalogChatModel("dynamic")}))
			provider := remoteCatalogTestProvider(server.URL, nil)
			store := ai.NewInMemoryModelsStore()
			requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
			stored, err := store.Read(t.Context(), "test-provider")
			if err != nil || stored == nil || stored.LastModified == nil {
				t.Fatalf("stored = %+v, err = %v", stored, err)
			}
			if *stored.LastModified != want {
				t.Fatalf("lastModified = %v, want %v", *stored.LastModified, want)
			}
		})
	}
}

// parseCatalog maps every model to { ...model, provider: providerId } (remote-catalog-provider.ts:49): the requested provider id replaces a missing or foreign provider field, and an entry without an id or with an unsupported type is dropped (:47-48).
func TestParseRemoteCatalogStampsTheRequestedProvider(t *testing.T) {
	models, err := parseRemoteCatalog("test-provider", []byte(`[{"id":"a","provider":"other"},{"id":"b"},{"provider":"x"},{"id":"c","type":"video"}]`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, model := range models {
		var fields struct{ ID, Provider string }
		if err := json.Unmarshal(model, &fields); err != nil {
			t.Fatal(err)
		}
		got = append(got, fields.ID+"/"+fields.Provider)
	}
	if want := []string{"a/test-provider", "b/test-provider"}; !slices.Equal(got, want) {
		t.Fatalf("parsed models = %v, want %v", got, want)
	}
}
