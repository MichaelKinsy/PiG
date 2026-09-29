package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Pi 0.87.1 google-generative-ai.ts reads responseId from each chunk but never reads modelVersion, so a Gemini stream never sets responseModel, even when the reported version differs from the requested model.
func TestGoogleStreamNeverSetsResponseModel(t *testing.T) {
	for _, version := range []string{"gemini-2.5-flash", "gemini-2.5-flash-001"} {
		t.Run(version, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: {\"modelVersion\":%q,\"responseId\":\"gemini-response\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n", version)
			}))
			defer server.Close()
			provider := NewGoogleProvider(GoogleConfig{Model: "gemini-2.5-flash", APIKey: "test-api-key", BaseURL: server.URL})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			message := stream.Result()
			if message.ResponseID != "gemini-response" || message.ResponseModel != "" {
				t.Fatalf("responseId=%q responseModel=%q", message.ResponseID, message.ResponseModel)
			}
		})
	}
}
