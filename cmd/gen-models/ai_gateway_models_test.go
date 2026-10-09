package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func aiGatewayItems(t *testing.T, raw string) []aiGatewayModel {
	t.Helper()
	var items []aiGatewayModel
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatal(err)
	}
	return items
}

// generate-models.ts fetchAiGatewayModels: a model needs the tool-use tag, vision adds image input, an evaluation model is a classifier on the TypeSafe endpoint, and the
// context window and max tokens default to 4096 when absent or zero. getAiGatewayCost prices the chat model, including its prompt-length tiers.
func TestBuildAiGatewayCatalogConvertsChatAndEvaluationModels(t *testing.T) {
	catalog := buildAiGatewayCatalog(aiGatewayItems(t, `[
		{"id":"a/chat","name":"Chat","tags":["tool-use","vision","reasoning"],"context_window":200000,"max_tokens":8000,
		 "pricing":{"input":"0.000001","output":"0.000005","input_tiers":[{"cost":"0.000001","min":0,"max":32001},{"cost":"0.0000018","min":32001}]}},
		{"id":"a/untooled","tags":["vision"]},
		{"id":"a/notags"},
		{"id":"a/plain","tags":["tool-use"]},
		{"id":"t/jev","name":"Jev","type":"evaluation","context_window":0,"pricing":{"input":"0.000002","output":0.000004}}]`))
	if len(catalog.Chat) != 2 || len(catalog.Classifiers) != 1 {
		t.Fatalf("chat = %d, classifiers = %d, want 2 and 1", len(catalog.Chat), len(catalog.Classifiers))
	}
	chat := catalog.Chat[0]
	wantCost := jsonCost{Input: 1, Output: 5, Tiers: []jsonCostTier{{InputTokensAbove: 32000, Input: 1.8, Output: 5}}}
	if chat.API != "anthropic-messages" || chat.Provider != "vercel-ai-gateway" || chat.BaseURL != "https://ai-gateway.vercel.sh" || !chat.Reasoning ||
		!reflect.DeepEqual(chat.Input, []string{"text", "image"}) || chat.ContextWindow != 200000 || chat.MaxTokens != 8000 || chat.Compat == nil || chat.Compat.AllowEmptySignature == nil || !*chat.Compat.AllowEmptySignature ||
		!reflect.DeepEqual(chat.Cost, wantCost) {
		t.Fatalf("chat model = %+v", chat)
	}
	plain := catalog.Chat[1]
	if plain.Name != "a/plain" || plain.ContextWindow != 4096 || plain.MaxTokens != 4096 || !reflect.DeepEqual(plain.Input, []string{"text"}) || plain.Reasoning {
		t.Fatalf("plain model = %+v", plain)
	}
	classifier := catalog.Classifiers[0]
	if classifier.API != "typesafe-system-one" || classifier.BaseURL != "https://ai-gateway.vercel.sh/typesafe/v1" || classifier.ContextWindow != 4096 ||
		!reflect.DeepEqual(classifier.Cost, jsonCost{Input: 2, Output: 4}) || !reflect.DeepEqual(classifier.Input, []string{"text"}) {
		t.Fatalf("classifier = %+v", classifier)
	}
}

// fetchAiGatewayModels: a non-OK response is fatal under strict and reported with an empty catalog otherwise; a response whose data is not an array is an empty listing.
func TestFetchAiGatewayModelsHandlesFailuresLikeTheOpenRouterStage(t *testing.T) {
	var body string
	var status int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	status, body = http.StatusOK, `{"data":[{"id":"a/chat","tags":["tool-use"]}]}`
	if catalog, err := fetchAiGatewayModels(t.Context(), server.URL, true); err != nil || len(catalog.Chat) != 1 {
		t.Fatalf("listing: %d chat models, %v", len(catalog.Chat), err)
	}
	status, body = http.StatusOK, `{"data":{"not":"an array"}}`
	if catalog, err := fetchAiGatewayModels(t.Context(), server.URL, true); err != nil || len(catalog.Chat) != 0 || len(catalog.Classifiers) != 0 {
		t.Fatalf("non-array data: %+v, %v", catalog, err)
	}
	status, body = http.StatusCreated, `{"data":[{"id":"a/odd","tags":["tool-use"],"context_window":"large"},{"id":"a/chat","tags":["tool-use"]}]}`
	if catalog, err := fetchAiGatewayModels(t.Context(), server.URL, true); err != nil || len(catalog.Chat) != 2 || catalog.Chat[0].ContextWindow != 4096 {
		t.Fatalf("2xx listing with a mistyped member: %d chat models, %v; want both listed (response.ok accepts any 2xx)", len(catalog.Chat), err)
	}
	status, body = http.StatusBadGateway, ``
	if catalog, err := fetchAiGatewayModels(t.Context(), server.URL, false); err != nil || len(catalog.Chat) != 0 {
		t.Fatalf("lenient failure: %+v, %v", catalog, err)
	}
	if _, err := fetchAiGatewayModels(t.Context(), server.URL, true); err == nil || err.Error() != "Vercel AI Gateway API returned 502" {
		t.Fatalf("strict failure = %v, want Vercel AI Gateway API returned 502", err)
	}
}
