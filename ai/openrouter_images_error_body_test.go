package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// Ports the behavioral contract of provider-error-body-passthrough.test.ts. The
// upstream regression targets the openai JS SDK's APIError, which reports
// "<status> status code (no body)" and keeps the parsed body only on error.error,
// so a body-blind catch surfaces an opaque message and hides the gateway's real
// reason. pig's Go image provider reads resp.Body directly, so it surfaces the
// body by construction. This guards that a non-2xx with a body yields an error
// carrying the body reason, never the opaque "no body" form.
func TestGenerateImagesOpenRouter_SurfacesErrorBodyReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream key revoked by gateway","code":403}}`))
	}))
	defer server.Close()

	model := ImageModel{
		ID:       "test-image-model",
		API:      APIImagesOpenRouter,
		Provider: ProviderImagesOpenRouter,
		BaseURL:  server.URL,
		Output:   []string{"image"},
	}
	result := GenerateImagesOpenRouter(context.Background(), model, ImagesContext{Input: []ContentBlock{
		TextContent{Text: "draw"},
	}}, ProviderImagesOptions{APIKey: "sk-test"})

	if result.StopReason != ImagesStopReasonError {
		t.Fatalf("stop reason = %v, want error", result.StopReason)
	}
	if !strings.Contains(result.ErrorMessage, "upstream key revoked by gateway") {
		t.Errorf("error message = %q, want it to carry the response body reason (must not drop the body)", result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, "no body") {
		t.Errorf("error message = %q, must not be the opaque JS-SDK 'no body' form", result.ErrorMessage)
	}
}

// Ports openrouter-images.ts catch: output.errorMessage = formatProviderError(normalizeProviderError(error)).
// The OpenAI SDK APIError carries "<status> <inner error message>" and the inner error object as its body;
// the shared policy renders "<status>: <inner error JSON>" and does not echo the raw response envelope.
func TestGenerateImagesOpenRouter_HTTPErrorUsesProviderErrorFormat(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"inner error object", `{"error":{"message":"bad image request","code":400}}`, `400: {"message":"bad image request","code":400}`},
		{"empty body", ``, `400 status code (no body)`},
		{"non-JSON body", `upstream exploded`, `400 upstream exploded`},
		// openai 7.x makeStatusError makes a body without an `error` member the error object itself.
		{"envelope without error member", `{"message":"top level"}`, `400: {"message":"top level"}`},
		{"null error member", `{"error":null,"detail":"gateway"}`, `400 {"error":null,"detail":"gateway"}`},
		{"array body", `["one"]`, `400 ["one"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			model := ImageModel{ID: "m", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: server.URL, Output: []string{"image"}}
			result := GenerateImagesOpenRouter(context.Background(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "draw"}}}, ProviderImagesOptions{APIKey: "sk-test"})
			if result.StopReason != ImagesStopReasonError {
				t.Fatalf("stop reason = %v, want error", result.StopReason)
			}
			if result.ErrorMessage != tc.want {
				t.Errorf("error message = %q, want %q", result.ErrorMessage, tc.want)
			}
		})
	}
}

// Pi awaits options.onResponse only after client.chat.completions.create(...).withResponse() resolves (openrouter-images.ts generateImages); the SDK rejects first for a non-2xx status or an unparseable body, so the hook never runs on those paths and cannot replace their error.
func TestGenerateImagesOpenRouter_OnResponseRunsOnlyAfterSuccessfulResponse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantCalls  []int
		wantReason ImagesStopReason
		wantError  string
	}{
		{"http error", http.StatusBadRequest, `{"error":{"message":"bad"}}`, nil, ImagesStopReasonError, `400: {"message":"bad"}`},
		{"unparseable 2xx body", http.StatusOK, `not json`, nil, ImagesStopReasonError, ""},
		{"success", http.StatusOK, `{"id":"r1","choices":[]}`, []int{http.StatusOK}, ImagesStopReasonStop, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			model := ImageModel{ID: "m", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: server.URL, Output: []string{"image"}}
			var calls []int
			result := GenerateImagesOpenRouter(context.Background(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: "draw"}}}, ProviderImagesOptions{
				APIKey: "sk-test",
				OnResponse: func(response ProviderResponse, _ ImageModel) error {
					calls = append(calls, response.Status)
					return errors.New("hook failed")
				},
			})
			if !slices.Equal(calls, tc.wantCalls) {
				t.Fatalf("onResponse calls = %v, want %v", calls, tc.wantCalls)
			}
			if tc.wantCalls != nil {
				// A rejecting hook becomes the result error, as Pi's catch reports it.
				if result.StopReason != ImagesStopReasonError || result.ErrorMessage != "hook failed" || result.ResponseID != "" {
					t.Fatalf("result = %+v, want the hook error without a response id", result)
				}
				return
			}
			if result.StopReason != tc.wantReason {
				t.Fatalf("stop reason = %v, want %v", result.StopReason, tc.wantReason)
			}
			if tc.wantError != "" && result.ErrorMessage != tc.wantError {
				t.Fatalf("error message = %q, want %q", result.ErrorMessage, tc.wantError)
			}
			if result.ErrorMessage == "hook failed" {
				t.Fatalf("hook error replaced the response error")
			}
		})
	}
}
