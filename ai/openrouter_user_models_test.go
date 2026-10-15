package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Pi 1.2.0 packages/ai/test/openrouter-user-models.test.ts.

func openRouterUserTestChat(id string) *Model {
	return &Model{ID: id, DisplayName: id, ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "openrouter", BaseURL: "https://openrouter.ai/api/v1"},
		Input: []string{"text"}, Capabilities: ModelCapabilities{InputCostPer1M: 1, OutputCostPer1M: 2, CacheReadCostPer1M: 0.1, ContextWindow: 200_000, MaxOutputTokens: 50_000}}
}

func openRouterUserTestRefresh(server *httptest.Server) RefreshModelsContext {
	return RefreshModelsContext{
		Credential: &Credential{Type: CredentialAPIKey, Key: "sk-or-test"}, Publish: func(ModelsPublication) (bool, error) { return true, nil },
		AllowNetwork: true, Signal: nil, BaseURL: server.URL + "/api/v1/",
	}
}

// Pi 1.2.0 packages/ai/test/openrouter-user-models.test.ts:64 "keeps only the key's chat models, with the key's limits and prices"
func TestPersonalizeOpenRouterCatalogKeepsOnlyTheKeysChatModelsWithTheKeysLimitsAndPrices(t *testing.T) {
	var path, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, authorization = r.URL.Path, r.Header.Get("authorization")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"listed","context_length":1000000,"top_provider":{"context_length":262144,"max_completion_tokens":32768},"pricing":{"prompt":"0.000003","completion":"0.000015"}},
			{"id":"no-limits","context_length":0,"top_provider":{"context_length":null,"max_completion_tokens":null}},
			{"id":"router","pricing":{"prompt":"-1","completion":"-1"}}]}`))
	}))
	defer server.Close()
	image := &ImageModel{ID: "image", Name: "image", API: "openrouter-images", Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Input: []string{"text"}, Output: []string{"image"}}
	classifier := &ClassifierModel{ID: "classifier", Name: "classifier", API: "typesafe-system-one", Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Input: []string{"text"}, ContextWindow: 64_000}
	catalog := []AnyModel{openRouterUserTestChat("listed"), openRouterUserTestChat("no-limits"), openRouterUserTestChat("router"), openRouterUserTestChat("blocked"), image, classifier}

	result, err := PersonalizeOpenRouterCatalog(catalog, openRouterUserTestRefresh(server))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, model := range result {
		ids = append(ids, model.ModelID())
	}
	if want := []string{"listed", "no-limits", "router", "image", "classifier"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	// The request follows the provider's base URL (a trailing slash is dropped) and sends the key as a bearer token.
	if path != "/api/v1/models/user" || authorization != "Bearer sk-or-test" {
		t.Fatalf("request path %q authorization %q", path, authorization)
	}
	listed := result[0].(*Model).Capabilities
	if listed.ContextWindow != 262_144 || listed.MaxOutputTokens != 32_768 || listed.InputCostPer1M != 3 || listed.OutputCostPer1M != 15 || listed.CacheReadCostPer1M != 0 || listed.CacheWriteCostPer1M != 0 {
		t.Fatalf("listed capabilities = %+v", listed)
	}
	// A missing limit keeps the catalog value, and a model the key lists without prices keeps its catalog cost, so generator fixes for missing OpenRouter data survive.
	if kept, want := result[1].(*Model).Capabilities, openRouterUserTestChat("x").Capabilities; !reflect.DeepEqual(kept, want) {
		t.Fatalf("no-limits capabilities = %+v, want %+v", kept, want)
	}
	// Router models report -1, meaning the price depends on the model they pick.
	if router := result[2].(*Model).Capabilities; router.InputCostPer1M != 0 || router.OutputCostPer1M != 0 || router.CacheReadCostPer1M != 0 || router.CacheWriteCostPer1M != 0 {
		t.Fatalf("router capabilities = %+v", router)
	}
	// /models/user lists text models only, so image and classifier models are not filtered, and the catalog models are not mutated.
	if result[3] != AnyModel(image) || result[4] != AnyModel(classifier) {
		t.Fatalf("image and classifier models were not returned as given: %v %v", result[3], result[4])
	}
	if original := catalog[0].(*Model).Capabilities.ContextWindow; original != 200_000 {
		t.Fatalf("the input catalog model was changed: context window %d", original)
	}
}

// Pi 1.2.0 packages/ai/test/openrouter-user-models.test.ts:102 "returns the catalog unchanged when the endpoint rejects the key or is missing (%i)"
func TestPersonalizeOpenRouterCatalogReturnsTheCatalogUnchangedWhenTheEndpointRejectsTheKeyOrIsMissing(t *testing.T) {
	// For example a proxy configured in models.json that rejects the key or has no /models/user: the user keeps the full list instead of a failing refresh.
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprintf("returns the catalog unchanged when the endpoint rejects the key or is missing (%d)", status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			catalog := []AnyModel{openRouterUserTestChat("a")}
			result, err := PersonalizeOpenRouterCatalog(catalog, openRouterUserTestRefresh(server))
			if err != nil || len(result) != 1 || result[0] != catalog[0] {
				t.Fatalf("result = %v, err = %v", result, err)
			}
		})
	}
}

// openrouter.ts personalizeOpenRouterCatalog: no key means no request and the catalog as given; any other failure rejects, so the refresh keeps the saved list.
func TestPersonalizeOpenRouterCatalogRejectsOtherFailuresAndSkipsTheRequestWithoutAKey(t *testing.T) {
	requests := 0
	status, body := http.StatusInternalServerError, ``
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	catalog := []AnyModel{openRouterUserTestChat("a")}
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"server error", http.StatusInternalServerError, ``, "OpenRouter model list request failed: 500"},
		{"no data member", http.StatusOK, `{"models":[]}`, "Invalid OpenRouter model list response"},
		{"data is not an array", http.StatusOK, `{"data":{}}`, "Invalid OpenRouter model list response"},
		{"body is null", http.StatusOK, `null`, "Invalid OpenRouter model list response"},
		{"body is an array", http.StatusOK, `[]`, "Invalid OpenRouter model list response"},
	} {
		t.Run(c.name, func(t *testing.T) {
			status, body = c.status, c.body
			if result, err := PersonalizeOpenRouterCatalog(catalog, openRouterUserTestRefresh(server)); err == nil || err.Error() != c.want || result != nil {
				t.Fatalf("result = %v, err = %v, want error %q", result, err, c.want)
			}
		})
	}
	before := requests
	refresh := openRouterUserTestRefresh(server)
	refresh.Credential = &Credential{Type: CredentialOAuth, Access: ""}
	if result, err := PersonalizeOpenRouterCatalog(catalog, refresh); err != nil || len(result) != 1 || requests != before {
		t.Fatalf("an OAuth credential without an access token sent a request: result %v err %v requests %d -> %d", result, err, before, requests)
	}
	refresh.Credential = &Credential{Type: CredentialOAuth, Access: "oauth-access"}
	// Entries without a string id are skipped.
	status, body = http.StatusOK, `{"data":[{"nameless":true}, 5, {"id":"a"}]}`
	if result, err := PersonalizeOpenRouterCatalog(catalog, refresh); err != nil || len(result) != 1 || result[0].ModelID() != "a" {
		t.Fatalf("entries without an id: result %v err %v", result, err)
	}
	status, body = http.StatusOK, `{"data":[]}`
	if result, err := PersonalizeOpenRouterCatalog(catalog, refresh); err != nil || len(result) != 0 {
		t.Fatalf("an empty list leaves no chat model: result %v err %v", result, err)
	}
}
