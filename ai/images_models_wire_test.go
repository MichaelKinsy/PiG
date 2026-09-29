package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestOpenRouterImagesHeaderUndefinedOverride(t *testing.T) {
	// Pi api/openrouter-images.ts:128 spreads model and option headers before providerHeadersToRecord removes undefined.
	model := map[string]string{"X-Removed": "remove-me", "X-Kept": "keep-me", "X-Shared": "model"}
	options := ProviderHeaders{"X-Removed": nil, "x-kept": nil, "X-Shared": new("request")}
	got := openRouterImagesHeaders("key", model, options)
	want := http.Header{"Authorization": {"Bearer key"}, "Content-Type": {"application/json"}, "X-Kept": {"keep-me"}, "X-Shared": {"request"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headers=%v, want %v", got, want)
	}
	if model["X-Removed"] != "remove-me" || options["X-Removed"] != nil {
		t.Fatal("header conversion mutated its input")
	}
}

func TestImagesModelsAuthThroughOpenRouter(t *testing.T) {
	type wire struct {
		Authorization  string         `json:"authorization"`
		Body           map[string]any `json:"body"`
		ModelHeader    string         `json:"modelHeader"`
		ProviderHeader string         `json:"providerHeader"`
		RemovedHeader  bool           `json:"removedHeader"`
		SharedHeader   string         `json:"sharedHeader"`
	}
	requests := make(chan wire, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		_, removed := r.Header["X-Removed"]
		requests <- wire{r.Header.Get("Authorization"), body, r.Header.Get("X-Model"), r.Header.Get("X-Provider"), removed, r.Header.Get("X-Shared")}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"image-response","choices":[{"message":{"images":[{"image_url":{"url":"data:image/png;base64,aGk="}}]}}]}`))
	}))
	defer server.Close()
	models := BuiltinImagesModels(CreateModelsOptions{AuthContext: imageRuntimeAuthContext(nil)})
	provider := models.GetProvider("openrouter")
	provider.Auth.APIKey.Resolve = func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
		return &AuthResult{Auth: ModelAuth{APIKey: "provider-key", BaseURL: server.URL, Headers: ProviderHeaders{"X-Provider": new("provider"), "X-Shared": new("provider")}}, Env: map[string]string{"PROVIDER_ONLY": "provider", "SHARED": "provider"}}, nil
	}
	var seenEnv map[string]string
	generate := provider.GenerateImages
	provider.GenerateImages = func(ctx context.Context, m ImagesModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
		seenEnv = options.Env
		return generate(ctx, m, request, options)
	}
	model := ImagesModel{ID: "fixture-image", Name: "Fixture image", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: "http://[invalid", Headers: map[string]string{"X-Model": "model", "X-Removed": "remove-me"}, Input: []string{"text"}, Output: []string{"image"}}
	result := models.GenerateImages(t.Context(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "a red circle"}}}, ImagesOptions{APIKey: "request-key", Headers: ProviderHeaders{"X-Shared": new("request"), "X-Removed": nil}, Env: map[string]string{"REQUEST_ONLY": "request", "SHARED": "request"}})
	if result.StopReason != ImagesStopReasonStop || result.ResponseID != "image-response" || !reflect.DeepEqual(result.Output, []ContentBlock{ImageContent{Data: "aGk=", MimeType: "image/png"}}) {
		t.Fatalf("result=%+v", result)
	}
	got := <-requests
	if got.Authorization != "Bearer request-key" || got.ModelHeader != "model" || got.ProviderHeader != "provider" || got.SharedHeader != "request" || got.RemovedHeader {
		t.Fatalf("wire=%+v", got)
	}
	if model.BaseURL != "http://[invalid" {
		t.Fatal("request auth mutated the caller's model")
	}
	wantEnv := map[string]string{"PROVIDER_ONLY": "provider", "REQUEST_ONLY": "request", "SHARED": "request"}
	if !reflect.DeepEqual(seenEnv, wantEnv) {
		t.Fatalf("env=%v", seenEnv)
	}
	if os.Getenv("PIG_PARITY_PROBE") == "1" {
		data, err := json.Marshal(struct {
			Env        map[string]string `json:"env"`
			Output     []ContentBlock    `json:"output"`
			ResponseID string            `json:"responseId"`
			StopReason ImagesStopReason  `json:"stopReason"`
			Wire       wire              `json:"wire"`
		}{seenEnv, result.Output, result.ResponseID, result.StopReason, got})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("IMAGES_RUNTIME %s\n", data)
	}
}

func TestImagesModelsRetainsCatalogAndProviderOrder(t *testing.T) {
	models := CreateImagesModels()
	first := imageRuntimeTestProvider("first", "", nil, nil)
	second := imageRuntimeTestProvider("second", "", nil, nil)
	models.SetProvider(first)
	models.SetProvider(second)
	replacement := imageRuntimeTestProvider("first", "", []ImagesModel{imageRuntimeTestModel("first", "replacement")}, nil)
	models.SetProvider(replacement)
	if list := models.GetProviders(); len(list) != 2 || list[0] != replacement || list[1] != second {
		t.Fatalf("providers=%v", list)
	}
	second.GetModels = func() ([]ImagesModel, error) { return nil, fmt.Errorf("source failure") }
	if len(models.GetModels("second")) != 0 || len(models.GetModels()) != 1 {
		t.Fatal("failed source was not isolated")
	}
	models.ClearProviders()
	if len(models.GetProviders()) != 0 || len(models.GetModels()) != 0 {
		t.Fatal("clear retained providers")
	}
}

func BenchmarkImagesModelsAuthDispatch(b *testing.B) {
	models := CreateImagesModels()
	provider := imageRuntimeTestProvider("p1", "", nil, nil)
	provider.Auth.APIKey.Resolve = func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
		return &AuthResult{Auth: ModelAuth{APIKey: "resolved", Headers: ProviderHeaders{"X-Provider": new("value")}}, Env: map[string]string{"PROVIDER": "value"}}, nil
	}
	models.SetProvider(provider)
	model := *models.GetModel("p1", "model-a")
	request := ImagesContext{Input: []ContentBlock{TextContent{Text: "a red circle"}}}
	options := ImagesOptions{Headers: ProviderHeaders{"X-Request": new("request")}, Env: map[string]string{"REQUEST": "request"}}
	b.ReportAllocs()
	for b.Loop() {
		result := models.GenerateImages(b.Context(), model, request, options)
		if result.StopReason != ImagesStopReasonStop {
			b.Fatal(result.ErrorMessage)
		}
	}
}
