package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
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

	model := ImagesModel{
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
