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

func TestModelsGenerateImagesAuthThroughOpenRouter(t *testing.T) {
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
	oauth, _ := OAuthProviderAuth("openrouter")
	var seenEnv map[string]string
	provider := CreateProvider(CreateProviderOptions{ID: "openrouter", Name: new("OpenRouter"), Auth: ProviderAuth{APIKey: EnvAPIKeyAuth("OpenRouter API key", "OPENROUTER_API_KEY"), OAuth: oauth},
		Models: []AnyModel{}, Images: ProviderImageAPIMap{APIImagesOpenRouter: {GenerateImages: func(ctx context.Context, m *ImageModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
			seenEnv = options.Env
			return GenerateImagesOpenRouter(ctx, *m, request, options), nil
		}}}})
	provider.Auth.APIKey.Resolve = func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
		return &AuthResult{Auth: ModelAuth{APIKey: "provider-key", BaseURL: server.URL, Headers: ProviderHeaders{"X-Provider": new("provider"), "X-Shared": new("provider")}}, Env: map[string]string{"PROVIDER_ONLY": "provider", "SHARED": "provider"}}, nil
	}
	models := CreateModels(CreateModelsOptions{AuthContext: typedAuthContext(nil)})
	models.SetProvider(provider)
	model := &ImageModel{ID: "fixture-image", Name: "Fixture image", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: "http://[invalid", Headers: map[string]string{"X-Model": "model", "X-Removed": "remove-me"}, Input: []string{"text"}, Output: []string{"image"}}
	result := models.GenerateImages(t.Context(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "a red circle"}}}, ModelsImagesOptions{ImagesOptions: ImagesOptions{APIKey: "request-key", Headers: ProviderHeaders{"X-Shared": new("request"), "X-Removed": nil}, Env: map[string]string{"REQUEST_ONLY": "request", "SHARED": "request"}}})
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

func BenchmarkModelsGenerateImagesAuthDispatch(b *testing.B) {
	models := CreateModels()
	provider := typedTestProvider(typedProviderInput{id: "p1"})
	provider.Auth.APIKey.Resolve = func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
		return &AuthResult{Auth: ModelAuth{APIKey: "resolved", Headers: ProviderHeaders{"X-Provider": new("value")}}, Env: map[string]string{"PROVIDER": "value"}}, nil
	}
	models.SetProvider(provider)
	model := imageModelOf(b, models, "p1", "model-a")
	request := ImagesContext{Input: []ContentBlock{TextContent{Text: "a red circle"}}}
	options := ModelsImagesOptions{ImagesOptions: ImagesOptions{Headers: ProviderHeaders{"X-Request": new("request")}, Env: map[string]string{"REQUEST": "request"}}}
	b.ReportAllocs()
	for b.Loop() {
		result := models.GenerateImages(b.Context(), model, request, options)
		if result.StopReason != ImagesStopReasonStop {
			b.Fatal(result.ErrorMessage)
		}
	}
}
