package ai

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Ports packages/ai/test/github-copilot-request-size-overflow.test.ts (Pi 73f8de80): Copilot rejects a request over its byte-size
// limit with HTTP 413 and a generic body, and the classifier must see it through the real provider stream, not only as a string.
func TestGitHubCopilotRequestSizeErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		nested   bool
		overflow bool
	}{
		{"classifies HTTP 413 with nested=true through the real SDK", http.StatusRequestEntityTooLarge, true, true},
		{"classifies HTTP 413 with nested=false through the real SDK", http.StatusRequestEntityTooLarge, false, true},
		{"classifies HTTP 400 with nested=true through the real SDK", http.StatusBadRequest, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"message":"failed to parse request","code":""}`
			if tc.nested {
				body = `{"error":` + body + `}`
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test-key", ProviderID: "github-copilot", Model: "copilot-test-model", BaseURL: server.URL})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Continue")}}}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if calls != 1 {
				t.Errorf("requests = %d, want 1", calls)
			}
			if result.Provider != "github-copilot" || result.StopReason != StopReasonError || result.Usage.TotalTokens != 0 {
				t.Fatalf("result = provider %q stop %q usage %+v", result.Provider, result.StopReason, result.Usage)
			}
			if !strings.Contains(result.ErrorMessage, "("+strconv.Itoa(tc.status)+")") || !strings.Contains(result.ErrorMessage, "failed to parse request") {
				t.Fatalf("error message = %q", result.ErrorMessage)
			}
			if got := IsContextOverflow(*result, 1_000_000); got != tc.overflow {
				t.Fatalf("IsContextOverflow = %v, want %v (message %q)", got, tc.overflow, result.ErrorMessage)
			}
		})
	}
}
