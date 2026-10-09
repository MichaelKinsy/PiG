package ai_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// .upstream/v0.87.1/packages/ai/test/compat-env.test.ts:46 — dispatches unknown providers through the legacy API registry.
// Go's configured model registry selects the linked API factory; the request observes the same explicit-key override at the actual HTTP boundary.
func TestCompatLegacyAPIFallbackUpstream(t *testing.T) {
	capturedKeys := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKeys <- r.Header.Get("Authorization")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["model"] != "test-model" {
			t.Errorf("model=%#v", payload["model"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"))
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	config := fmt.Sprintf(`{"providers":{"custom-openai":{"api":"openai-responses","baseUrl":%q,"models":[{"id":"test-model","name":"Test Model","contextWindow":128000,"maxTokens":4096}]}}}`, server.URL)
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("custom-openai", "test-model")
	if model == nil {
		t.Fatal("custom provider did not resolve through its API factory")
	}
	result := services.ModelRuntime().Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, ai.StreamOptions{APIKey: "request-key"})
	if result.StopReason == ai.StopReasonError {
		t.Fatal(result.ErrorMessage)
	}
	select {
	case capturedKey := <-capturedKeys:
		if capturedKey != "Bearer request-key" {
			t.Fatalf("captured apiKey=%q, want request-key", capturedKey)
		}
	default:
		t.Fatal("API factory did not send the request")
	}
}
