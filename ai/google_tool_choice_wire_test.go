//go:build !pig_strip_google_vertex

package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// upstream: packages/ai/src/api/google-generative-ai.ts:389 and google-vertex.ts:482 pass options.toolChoice ("auto" | "none" | "any") to
// resolveGoogleFunctionCallingMode, which maps it with google-shared.ts mapToolChoice. Go carries the choice in StreamOptions.ToolChoice,
// shared by every API, so the Google and Vertex requests must read it into toolConfig.functionCallingConfig.mode.
func TestGoogleAndVertexRequestsReadToolChoice(t *testing.T) {
	for _, tc := range []struct {
		choice any
		want   any
	}{
		{nil, nil},
		{"auto", "AUTO"},
		{"none", "NONE"},
		{"any", "ANY"},
		{ToolChoiceNone, "NONE"},
	} {
		for _, api := range []string{"google", "vertex"} {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
			}))
			var provider Provider
			if api == "google" {
				provider = NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "gemini-2.5-flash", BaseURL: server.URL, ProviderID: "google-generative-ai"})
			} else {
				provider = NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "test-key", Model: "gemini-2.5-flash", BaseURL: server.URL})
			}
			transcript := NormalizeContext(Context{Messages: []Message{
				SystemMessage{ToolsAdded: []ToolSchema{{Name: "lookup", Description: "lookup", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}},
				UserMessage{Content: UserText("call it")},
			}})
			stream, err := provider.Stream(context.Background(), transcript, StreamOptions{ToolChoice: tc.choice})
			if err != nil {
				t.Fatalf("%s %v: Stream: %v", api, tc.choice, err)
			}
			_ = stream.Result()
			server.Close()
			var mode any
			if config, ok := body["toolConfig"].(map[string]any); ok {
				mode = config["functionCallingConfig"].(map[string]any)["mode"]
			}
			if mode != tc.want {
				t.Errorf("%s toolChoice %v: function calling mode = %#v, want %#v", api, tc.choice, mode, tc.want)
			}
		}
	}
}
