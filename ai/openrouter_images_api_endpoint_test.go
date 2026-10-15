package ai

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// pi-mono #10652: OpenRouter answers 404 "<model> is an image generation model and cannot be used with the chat/completions endpoint.
// Use the /api/v1/images endpoint instead." for openai/gpt-image-2.5-flare, so Pi returns stopReason "error" with no images.
// pig divergence (D128): PiG sends the request to the Images API when OpenRouter says so.
func TestOpenRouterImagesUsesTheImagesAPIWhenChatCompletionsRefusesTheModel(t *testing.T) {
	const refusal = `{"error":{"message":"openai/gpt-image-2.5-flare is an image generation model and cannot be used with the chat/completions endpoint. Use the /api/v1/images endpoint instead.","code":404}}`
	var mu sync.Mutex
	var paths []string
	var imagesBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/images" {
			_ = json.Unmarshal(body, &imagesBody)
			_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"aGk=","media_type":"image/webp"},{"b64_json":"eW8="}],"usage":{"prompt_tokens":10,"completion_tokens":1000}}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, refusal)
	}))
	defer server.Close()
	model := ImageModel{ID: "openai/gpt-image-2.5-flare", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: server.URL + "/api/v1", Output: []string{"image"}, Cost: ModelCost{Input: 8, Output: 8}}
	result := GenerateImagesOpenRouter(t.Context(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "A red circle"}, ImageContent{MimeType: "image/png", Data: "cmVm"}}}, ProviderImagesOptions{APIKey: "k"})
	if result.StopReason != ImagesStopReasonStop {
		t.Fatalf("stop=%s error=%q", result.StopReason, result.ErrorMessage)
	}
	if len(paths) != 2 || paths[0] != "/api/v1/chat/completions" || paths[1] != "/api/v1/images" {
		t.Fatalf("paths = %v", paths)
	}
	if imagesBody["model"] != model.ID || imagesBody["prompt"] != "A red circle" || imagesBody["input_references"] == nil {
		t.Fatalf("images request = %v", imagesBody)
	}
	if len(result.Output) != 2 || result.Output[0] != (ImageContent{MimeType: "image/webp", Data: "aGk="}) || result.Output[1] != (ImageContent{MimeType: "image/png", Data: "eW8="}) {
		t.Fatalf("output = %#v", result.Output)
	}
	if result.Usage == nil || result.Usage.Output != 1000 || result.Usage.Cost.Total == 0 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}

// Any other chat/completions failure stays a single request with Pi's error.
func TestOpenRouterImagesOtherFailuresDoNotUseTheImagesAPI(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"No endpoints found for x/y","code":404}}`)
	}))
	defer server.Close()
	model := ImageModel{ID: "x/y", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: server.URL, Output: []string{"image"}}
	result := GenerateImagesOpenRouter(t.Context(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "hi"}}}, ProviderImagesOptions{APIKey: "k"})
	if result.StopReason != ImagesStopReasonError || requests != 1 {
		t.Fatalf("stop=%s requests=%d", result.StopReason, requests)
	}
}

// The Images API request is a second provider request: the payload hook sees each request's own payload and can replace the second, the
// second request carries the same authorization, onResponse runs only for the 2xx /images response (the refused chat request never
// resolves withResponse()), and every text input joins the prompt.
func TestOpenRouterImagesAPIRequestRunsTheHooksAndJoinsTheTextInput(t *testing.T) {
	const refusal = `{"error":{"message":"black-forest-labs/flux-3-image is an image generation model and cannot be used with the chat/completions endpoint. Use the /api/v1/images endpoint instead.","code":404}}`
	var mu sync.Mutex
	var imagesBody map[string]any
	var imagesAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/api/v1/images" {
			mu.Lock()
			_ = json.Unmarshal(body, &imagesBody)
			imagesAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.Header().Set("X-Request-Id", "img-1")
			_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"aGk=","media_type":"image/png"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, refusal)
	}))
	defer server.Close()
	model := ImageModel{ID: "black-forest-labs/flux-3-image", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: server.URL + "/api/v1", Output: []string{"image"}}
	var payloads []map[string]any
	var responses []ProviderResponse
	options := ProviderImagesOptions{
		APIKey: "k",
		OnPayload: func(payload any, _ ImageModel) (any, bool, error) {
			copied := map[string]any{}
			maps.Copy(copied, payload.(map[string]any))
			payloads = append(payloads, copied)
			if _, ok := copied["prompt"]; ok {
				copied["seed"] = 7
				return copied, true, nil
			}
			return nil, false, nil
		},
		OnResponse: func(response ProviderResponse, _ ImageModel) error {
			responses = append(responses, response)
			return nil
		},
	}
	input := []ContentBlock{TextContent{Text: "A red circle"}, ImageContent{MimeType: "image/png", Data: "cmVm"}, TextContent{Text: "on white"}}
	result := GenerateImagesOpenRouter(t.Context(), model, ImagesContext{Input: input}, options)
	if result.StopReason != ImagesStopReasonStop || len(result.Output) != 1 {
		t.Fatalf("stop=%s error=%q output=%#v", result.StopReason, result.ErrorMessage, result.Output)
	}
	if len(payloads) != 2 || payloads[0]["messages"] == nil || payloads[1]["prompt"] != "A red circle\non white" {
		t.Fatalf("payloads = %v", payloads)
	}
	references, _ := payloads[1]["input_references"].([]map[string]any)
	if len(references) != 1 || references[0]["image_url"].(map[string]any)["url"] != "data:image/png;base64,cmVm" {
		t.Fatalf("input_references = %#v", payloads[1]["input_references"])
	}
	mu.Lock()
	defer mu.Unlock()
	if imagesBody["seed"] != float64(7) || imagesAuth != "Bearer k" {
		t.Fatalf("images request = %v, authorization %q", imagesBody, imagesAuth)
	}
	if len(responses) != 1 || responses[0].Status != http.StatusOK || responses[0].Headers["x-request-id"] != "img-1" {
		t.Fatalf("onResponse calls = %+v", responses)
	}
}

// Only OpenRouter's statement that the model needs /images moves the request, and only once: a refusal from /images itself, the same
// text with a server error status, and a chat/completions refusal that names no other endpoint all end with the request's own error.
func TestOpenRouterImagesAPIFallbackIsNarrowAndRunsOnce(t *testing.T) {
	const refusal = `{"error":{"message":"m/x is an image generation model and cannot be used with the chat/completions endpoint. Use the /api/v1/images endpoint instead.","code":404}}`
	cases := []struct {
		name         string
		chatStatus   int
		chatBody     string
		imagesStatus int
		wantPaths    []string
		wantError    string
	}{
		{"images also refuses", http.StatusNotFound, refusal, http.StatusNotFound, []string{"/chat/completions", "/images"}, "404"},
		{"server error with the refusal text", http.StatusInternalServerError, refusal, http.StatusNotFound, []string{"/chat/completions"}, "500"},
		{"refusal without the images endpoint", http.StatusNotFound, `{"error":{"message":"m/x cannot be used with the chat/completions endpoint.","code":404}}`, http.StatusNotFound, []string{"/chat/completions"}, "404"},
		{"bad request refusal", http.StatusBadRequest, refusal, http.StatusBadRequest, []string{"/chat/completions", "/images"}, "400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				if r.URL.Path == "/images" {
					w.WriteHeader(tc.imagesStatus)
					_, _ = io.WriteString(w, refusal)
					return
				}
				w.WriteHeader(tc.chatStatus)
				_, _ = io.WriteString(w, tc.chatBody)
			}))
			defer server.Close()
			model := ImageModel{ID: "m/x", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: server.URL, Output: []string{"image"}}
			result := GenerateImagesOpenRouter(t.Context(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "hi"}}}, ProviderImagesOptions{APIKey: "k"})
			mu.Lock()
			defer mu.Unlock()
			if result.StopReason != ImagesStopReasonError || !strings.HasPrefix(result.ErrorMessage, tc.wantError) || !slices.Equal(paths, tc.wantPaths) {
				t.Fatalf("stop=%s error=%q paths=%v, want error %s and paths %v", result.StopReason, result.ErrorMessage, paths, tc.wantError, tc.wantPaths)
			}
		})
	}
}
