//go:build !pig_strip_google_vertex

package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// upstream: packages/ai/src/api/google-generative-ai.ts:389 and google-vertex.ts:482 pass options.toolChoice ("auto" | "none" | "any") to
// resolveGoogleFunctionCallingMode, which mapToolChoice turns into the toolConfig function calling mode. Go carries the choice in
// StreamOptions.ToolChoice for both providers.
func TestGoogleAndVertexToolChoiceSelectsTheFunctionCallingMode(t *testing.T) {
	for _, vertex := range []bool{false, true} {
		for _, tc := range []struct {
			choice any
			want   string
		}{
			{"none", "NONE"}, {"any", "ANY"}, {"auto", "AUTO"}, {ToolChoiceNone, "NONE"}, {ToolChoiceAuto, "AUTO"},
		} {
			name := "google"
			if vertex {
				name = "vertex"
			}
			t.Run(name+"/"+toolChoiceLabel(tc.choice), func(t *testing.T) {
				var body map[string]any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode request: %v", err)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
				}))
				defer server.Close()
				var provider Provider
				if vertex {
					provider = NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "test", Model: "gemini-2.5-flash", BaseURL: server.URL})
				} else {
					provider = NewGoogleProvider(GoogleConfig{APIKey: "test", Model: "gemini-2.5-flash", ProviderID: "google-generative-ai", BaseURL: server.URL})
				}
				defer func() { _ = provider.Close() }()
				transcript := NormalizeContext(Context{Messages: []Message{
					SystemMessage{ToolsAdded: []ToolSchema{{Name: "lookup", Description: "lookup", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}},
					UserMessage{Content: UserText("call it")},
				}})
				stream, err := provider.Stream(t.Context(), transcript, StreamOptions{ToolChoice: tc.choice})
				if err != nil {
					t.Fatal(err)
				}
				_ = stream.Result()
				config, _ := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
				if config["mode"] != tc.want {
					t.Fatalf("toolConfig = %#v, want mode %s", body["toolConfig"], tc.want)
				}
			})
		}
	}
}

func toolChoiceLabel(choice any) string {
	name, _ := toolChoiceName(choice)
	return name
}
