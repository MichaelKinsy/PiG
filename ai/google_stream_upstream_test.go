//go:build !pig_strip_google_vertex

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGoogleRawStopReasonUpstream(t *testing.T) {
	for _, adapter := range []string{"Google Generative AI", "Google Vertex"} {
		for _, tc := range []struct {
			name, finish string
			tool         bool
			stop         StopReason
		}{
			// .upstream/v0.87.1/packages/ai/test/google-raw-stop-reason.test.ts:113,128
			{"preserves raw Gemini finish reasons for errors", map[bool]string{false: "MALFORMED_FUNCTION_CALL", true: "SAFETY"}[adapter == "Google Vertex"], false, StopReasonError},
			// .upstream/v0.87.1/packages/ai/test/google-raw-stop-reason.test.ts:162
			{"preserves MAX_TOKENS with a tool call as length", "MAX_TOKENS", true, StopReasonLength},
			// .upstream/v0.87.1/packages/ai/test/google-raw-stop-reason.test.ts:173
			{"maps STOP with a tool call to toolUse", "STOP", true, StopReasonToolUse},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				candidate := map[string]any{"finishReason": tc.finish}
				if tc.tool {
					candidate["content"] = map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"id": "call-1", "name": "echo", "args": map[string]any{"value": "truncated"}}}}}
				}
				chunk, err := json.Marshal(map[string]any{"responseId": "google-response-id", "candidates": []any{candidate}, "usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 0, "totalTokenCount": 1}})
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
				}))
				defer server.Close()
				var provider Provider
				if adapter == "Google Vertex" {
					provider = NewGoogleVertexProvider(GoogleVertexConfig{Model: "gemini-3-flash-preview", Project: "test-project", Location: "us-central1", BaseURL: server.URL})
					provider.(*googleVertexProvider).accessToken = func(context.Context, ProviderEnv) (string, error) { return "test-adc", nil }
				} else {
					provider = NewGoogleProvider(GoogleConfig{Model: "gemini-2.5-flash", APIKey: "test-api-key", BaseURL: server.URL})
				}
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				message := stream.Result()
				if message.StopReason != tc.stop || message.RawStopReason != tc.finish {
					t.Fatalf("message=%#v", message)
				}
				if tc.stop == StopReasonError && message.ErrorMessage != "Provider stopped with: "+tc.finish {
					t.Fatalf("error=%q", message.ErrorMessage)
				}
				if tc.tool {
					found := false
					for _, block := range message.Content {
						if _, ok := block.(ToolCall); ok {
							found = true
						}
					}
					if !found {
						t.Fatalf("content=%#v", message.Content)
					}
				}
			})
		}
	}
	for _, tc := range []struct {
		name    string
		headers ProviderHeaders
		want    string
	}{
		// .upstream/v0.87.1/packages/ai/test/google-raw-stop-reason.test.ts:186
		{"uses pi's User-Agent by default", nil, PiUserAgent()},
		// .upstream/v0.87.1/packages/ai/test/google-raw-stop-reason.test.ts:190
		{"lets explicit headers override the default User-Agent", ProviderHeadersFromStrings(map[string]string{"User-Agent": "custom-agent"}), "custom-agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := make(chan http.Header, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Clone()
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
			}))
			defer server.Close()
			provider := NewGoogleProvider(GoogleConfig{Model: "gemini-2.5-flash", APIKey: "test-api-key", BaseURL: server.URL})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{Headers: tc.headers})
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			if got := (<-headers).Get("User-Agent"); got != tc.want {
				t.Fatalf("User-Agent=%q; want %q", got, tc.want)
			}
		})
	}
}

// Go uses HTTP status responses rather than SDK ApiError objects. These cases exercise the same headers-less response policy through the actual Google provider.
func TestGoogleSharedRetryUpstream(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		status, maxRetries, wantAttempts int
		success                          bool
	}{
		// .upstream/v0.87.1/packages/ai/test/google-shared-retry.test.ts:14
		{"retries a headers-less SDK error with a retryable status", 429, 1, 2, true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-retry.test.ts:25
		{"does not retry when maxRetries is unset", 429, 0, 1, false},
		// .upstream/v0.87.1/packages/ai/test/google-shared-retry.test.ts:33
		{"does not retry a non-retryable status", 400, 2, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if attempts.Add(1) == 1 || !tc.success {
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprintf(w, "got status: %d", tc.status)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
			}))
			defer server.Close()
			provider := NewGoogleProvider(GoogleConfig{Model: "gemini-2.5-flash", APIKey: "test", BaseURL: server.URL})
			ctx := t.Context()
			if tc.maxRetries != 0 {
				ctx = WithProviderMaxRetries(ctx, tc.maxRetries)
			}
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
			if tc.success {
				if err != nil {
					t.Fatal(err)
				}
				result := stream.Result()
				if result.StopReason != StopReasonStop || len(result.Content) != 1 || result.Content[0].(TextContent).Text != "ok" {
					t.Fatal(result)
				}
			} else if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("got status: %d", tc.status)) {
				t.Fatalf("error=%v", err)
			}
			if got := int(attempts.Load()); got != tc.wantAttempts {
				t.Fatalf("attempts=%d want=%d", got, tc.wantAttempts)
			}
		})
	}
}
