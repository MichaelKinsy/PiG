package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Port of Pi main 117eac3a0cdfce28b205d29d7b97e7a8a86af9ad (#12357, #12330).
func TestGoogleStreamPromptBlockVsIncompleteStream(t *testing.T) {
	cases := []struct {
		name               string
		newProvider        func(baseURL string) Provider
		feedbackJSON       string
		expectedBlockError string
		expectedEmptyError string
	}{
		{
			name: "Google Generative AI",
			newProvider: func(baseURL string) Provider {
				return NewGoogleProvider(GoogleConfig{
					APIKey:  "test-key",
					Model:   "test-model",
					BaseURL: baseURL,
				})
			},
			feedbackJSON:       `{"blockReason":"PROHIBITED_CONTENT","safetyRatings":[{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","probability":"HIGH"}]}`,
			expectedBlockError: "Google prompt blocked: PROHIBITED_CONTENT. Safety rating: HARM_CATEGORY_SEXUALLY_EXPLICIT (HIGH)",
			expectedEmptyError: "Google stream ended without a finish reason after receiving 0 chunks",
		},
		{
			name: "Google Vertex",
			newProvider: func(baseURL string) Provider {
				return NewGoogleProvider(GoogleConfig{
					APIKey:  "test-key",
					Model:   "test-model",
					BaseURL: baseURL,
					api:     APIGoogleVertex,
				})
			},
			feedbackJSON:       `{"blockReason":"PROHIBITED_CONTENT","blockReasonMessage":"Blocked by policy","safetyRatings":[{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","probability":"HIGH"}]}`,
			expectedBlockError: "Google Vertex prompt blocked: PROHIBITED_CONTENT. Blocked by policy. Safety rating: HARM_CATEGORY_SEXUALLY_EXPLICIT (HIGH)",
			expectedEmptyError: "Google Vertex stream ended without a finish reason after receiving 0 chunks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runStream := func(body string) *AssistantMessage {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte(body))
				}))
				defer server.Close()
				provider := tc.newProvider(server.URL)
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{
					Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}},
				}), StreamOptions{})
				if err != nil {
					t.Fatalf("Stream failed: %v", err)
				}
				return stream.Result()
			}

			blocked := runStream(fmt.Sprintf("data: {\"promptFeedback\":%s}\n\n", tc.feedbackJSON))
			if blocked.StopReason != StopReasonError || blocked.ErrorMessage != tc.expectedBlockError {
				t.Fatalf("blocked = stop=%q err=%q, want error / %q", blocked.StopReason, blocked.ErrorMessage, tc.expectedBlockError)
			}
			if IsRetryableAssistantError(*blocked) {
				t.Fatalf("blocked prompt should not be retryable: %q", blocked.ErrorMessage)
			}

			empty := runStream("")
			if empty.StopReason != StopReasonError || empty.ErrorMessage != tc.expectedEmptyError {
				t.Fatalf("empty = stop=%q err=%q, want error / %q", empty.StopReason, empty.ErrorMessage, tc.expectedEmptyError)
			}
			if !IsRetryableAssistantError(*empty) {
				t.Fatalf("empty stream should be retryable: %q", empty.ErrorMessage)
			}
		})
	}
}

func TestGoogleStreamHandlesMalformedPromptFeedbackGracefully(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"promptFeedback":"not-an-object","candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}` + "\n\n"))
	}))
	defer server.Close()
	provider := NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{
		Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}},
	}), StreamOptions{})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	res := stream.Result()
	if res.StopReason != StopReasonStop {
		t.Fatalf("stop=%q err=%q, want stop", res.StopReason, res.ErrorMessage)
	}
}
