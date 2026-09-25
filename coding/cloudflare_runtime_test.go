package coding

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// cloudflareStreams forwards stream and streamSimple unchanged after endpoint
// substitution. Exercise the Services-owned runtime and real provider request encoders.
func TestCloudflareModelRuntimeRequestEnvironment(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "ambient-account")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "ambient-gateway")
	for _, tc := range []struct {
		api           ai.API
		suffix, reply string
	}{
		{ai.APIOpenAICompletions, "/chat/completions", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{ai.APIOpenAIResponses, "/responses", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
		{ai.APIAnthropicMessages, "/v1/messages", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"usage\":{\"input_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			requests := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != "fixture" || body["stream"] != true || r.Header.Get("X-Request") != "kept" {
					t.Errorf("body=%v headers=%v", body, r.Header)
				}
				requests <- r.URL.Path
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.reply)
			}))
			defer server.Close()
			agentDir := t.TempDir()
			baseURL := server.URL + "/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/end"
			config := fmt.Sprintf(`{"providers":{"cloudflare-ai-gateway":{"apiKey":"fixture","baseUrl":%q,"api":%q,"models":[{"id":"fixture","name":"Fixture"}]}}}`, baseURL, tc.api)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			model, err := BuildModel("cloudflare-ai-gateway/fixture", services)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"stream", "streamSimple"} {
				t.Run(mode, func(t *testing.T) {
					options := ai.StreamOptions{Env: ai.ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "", "CLOUDFLARE_GATEWAY_ID": "scoped-gateway"}, Headers: ai.ProviderHeaders{"X-Request": new("kept")}}
					input := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
					var stream *ai.AssistantMessageEventStream
					if mode == "stream" {
						stream = services.ModelRuntime().Stream(t.Context(), model, input, options)
					} else {
						stream = services.ModelRuntime().StreamSimple(t.Context(), model, input, options)
					}
					result := stream.Result()
					if result == nil || result.StopReason != ai.StopReasonStop || result.Model != "fixture" || result.API != tc.api {
						t.Fatalf("result=%#v", result)
					}
					var terminal *ai.AssistantMessage
					for event := range stream.Events(t.Context()) {
						if done, ok := event.(ai.DoneEvent); ok {
							terminal = done.Message
						}
					}
					if result != terminal {
						t.Fatal("runtime replaced terminal result")
					}
					select {
					case path := <-requests:
						if want := "//scoped-gateway/end" + tc.suffix; path != want {
							t.Errorf("path=%q, want %q", path, want)
						}
					default:
						t.Fatal("no provider request")
					}
					if model.ProviderMeta.BaseURL != baseURL || !reflect.DeepEqual(options.Env, ai.ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "", "CLOUDFLARE_GATEWAY_ID": "scoped-gateway"}) || input.Messages[0].(ai.UserMessage).Content != ai.UserText("hello") {
						t.Fatal("request mutated original model/context/options")
					}
				})
			}
		})
	}
}
