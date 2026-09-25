package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImageModelRegistry_OpenRouterGeneratedModels(t *testing.T) {
	providers := GetImageProviders()
	if len(providers) != 1 || providers[0] != ProviderImagesOpenRouter {
		t.Fatalf("GetImageProviders() = %v, want [openrouter]", providers)
	}
	models := GetImageModels(ProviderImagesOpenRouter)
	if len(models) != 55 {
		t.Fatalf("GetImageModels(openrouter) length = %d, want published pi-ai 0.87.1 image-catalog count 55", len(models))
	}
	for _, id := range []string{"google/gemini-3-pro-image", "google/gemini-3.1-flash-image", "microsoft/mai-image-2.5-pro", "openai/gpt-image-2", "krea/krea-2-large", "qwen/qwen-image-3", "qwen/qwen-image-3-pro"} {
		if _, ok := GetImageModel(ProviderImagesOpenRouter, id); !ok {
			t.Fatalf("expected %s", id)
		}
	}
	model, ok := GetImageModel(ProviderImagesOpenRouter, "google/gemini-2.5-flash-image")
	if !ok {
		t.Fatal("expected google/gemini-2.5-flash-image")
	}
	if model.API != APIImagesOpenRouter || model.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("model = %+v", model)
	}
	if !slicesContainsString(model.Output, "text") || !slicesContainsString(model.Output, "image") {
		t.Fatalf("model output = %v, want image+text", model.Output)
	}
}

func TestGenerateImages_NoProvider(t *testing.T) {
	_, err := GenerateImages(context.Background(), ImagesModel{API: "missing"}, ImagesContext{}, ProviderImagesOptions{})
	if err == nil || err.Error() != "No API provider registered for api: missing" {
		t.Fatalf("GenerateImages error = %v", err)
	}
}

func TestGenerateImagesOpenRouter_RequestAndResponse(t *testing.T) {
	var gotAuth string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"img-resp-1",
			"choices":[{"message":{"content":"done","images":[{"image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}}],
			"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":1}}
		}`))
	}))
	defer server.Close()

	model := ImagesModel{
		ID:       "test-image-model",
		API:      APIImagesOpenRouter,
		Provider: ProviderImagesOpenRouter,
		BaseURL:  server.URL,
		Output:   []string{"image", "text"},
	}
	result := GenerateImagesOpenRouter(context.Background(), model, ImagesContext{Input: []ContentBlock{
		TextContent{Text: "draw"},
		ImageContent{MimeType: "image/png", Data: "aW5wdXQ="},
	}}, ProviderImagesOptions{APIKey: "sk-test"})

	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	modalities, ok := gotPayload["modalities"].([]any)
	if !ok || len(modalities) != 2 || modalities[0] != "image" || modalities[1] != "text" {
		t.Fatalf("modalities = %#v, want [image text]", gotPayload["modalities"])
	}
	if result.StopReason != ImagesStopReasonStop || result.ResponseID != "img-resp-1" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Output) != 2 {
		t.Fatalf("output length = %d, want 2", len(result.Output))
	}
	if text, ok := result.Output[0].(TextContent); !ok || text.Text != "done" {
		t.Fatalf("output[0] = %#v", result.Output[0])
	}
	if image, ok := result.Output[1].(ImageContent); !ok || image.MimeType != "image/png" || image.Data != "aGVsbG8=" {
		t.Fatalf("output[1] = %#v", result.Output[1])
	}
	if result.Usage == nil || result.Usage.Input != 6 || result.Usage.Output != 3 || result.Usage.CacheRead != 3 || result.Usage.CacheWrite != 1 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}
