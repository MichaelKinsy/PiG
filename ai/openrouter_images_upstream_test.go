package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const openRouterImagesUpstreamResponse = `{"id":"img-1","usage":{"prompt_tokens":12,"completion_tokens":34,"prompt_tokens_details":{"cached_tokens":0}},"choices":[{"message":{"content":"Here is your image.","images":[{"image_url":"data:image/png;base64,ZmFrZS1wbmc="}]}}]}`

func TestOpenRouterImagesUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/openrouter-images.test.ts:67
	t.Run("returns text plus images in final output", func(t *testing.T) {
		model := ImagesModel{ID: "google/gemini-3.1-flash-image-preview", Name: "Gemini 3.1 Flash Image Preview", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: "https://openrouter.ai/api/v1", Input: []string{"text", "image"}, Output: []string{"text", "image"}, Cost: ImagesCost{Input: 0.015, Output: 0.03}, Headers: map[string]string{"HTTP-Referer": "https://example.com"}}
		var payload map[string]any
		client := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(openRouterImagesUpstreamResponse)), Request: request}, nil
		})}
		result, err := GenerateImages(t.Context(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "Generate a dog"}}}, ProviderImagesOptions{APIKey: "test", Fetch: client})
		if err != nil {
			t.Fatal(err)
		}
		if result.StopReason != ImagesStopReasonStop || result.ResponseID != "img-1" {
			t.Fatalf("result=%+v", result)
		}
		want := []ContentBlock{TextContent{Text: "Here is your image."}, ImageContent{MimeType: "image/png", Data: "ZmFrZS1wbmc="}}
		if !reflect.DeepEqual(result.Output, want) {
			t.Fatalf("output=%#v", result.Output)
		}
		if payload["stream"] != false || !reflect.DeepEqual(payload["modalities"], []any{"image", "text"}) {
			t.Fatalf("payload=%#v", payload)
		}
		content := payload["messages"].([]any)[0].(map[string]any)["content"].([]any)[0]
		if !reflect.DeepEqual(content, map[string]any{"type": "text", "text": "Generate a dog"}) {
			t.Fatalf("content=%#v", content)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openrouter-images.test.ts:99
	t.Run("passes through abort signal and returns aborted result", func(t *testing.T) {
		// The upstream SDK mock rejects with this exact error. A canceled context carrying that cause models the same rejected request without depending on net/http's URL wrapper.
		failure := errors.New("Request aborted")
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(failure)
		var captured context.Context
		client := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) { captured = request.Context(); return nil, failure })}
		result, err := GenerateImages(ctx, openRouterFluxModel(), ImagesContext{Input: []ContentBlock{TextContent{Text: "Generate a dog"}}}, ProviderImagesOptions{APIKey: "test", Fetch: client})
		if err != nil {
			t.Fatal(err)
		}
		if result.StopReason != ImagesStopReasonAborted || result.ErrorMessage != "Request aborted" || captured != ctx {
			t.Fatalf("result=%+v same signal=%v", result, captured == ctx)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/openrouter-images.test.ts:122
	t.Run("generateImages resolves the final assistant images result", func(t *testing.T) {
		client := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(openRouterImagesUpstreamResponse)), Request: request}, nil
		})}
		result, err := GenerateImages(t.Context(), openRouterFluxModel(), ImagesContext{Input: []ContentBlock{TextContent{Text: "Generate a dog"}}}, ProviderImagesOptions{APIKey: "test", Fetch: client})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, block := range result.Output {
			if _, ok := block.(ImageContent); ok {
				found = true
			}
		}
		if !found {
			t.Fatalf("no image in final result: %#v", result)
		}
	})
}

func openRouterFluxModel() ImagesModel {
	return ImagesModel{ID: "black-forest-labs/flux.2-pro", Name: "FLUX.2 Pro", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: "https://openrouter.ai/api/v1", Input: []string{"text", "image"}, Output: []string{"image"}, Cost: ImagesCost{Input: 0.015, Output: 0.03}}
}
