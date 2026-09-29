package coding

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Exercise real HTTP, provider parsing, runtime forwarding, FIFO draining, and terminal-result ownership rather than a prebuilt event stream.
func BenchmarkModelRuntimeObservation(b *testing.B) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		b.Run(string(api), func(b *testing.B) {
			var body strings.Builder
			if api == ai.APIOpenAIResponses {
				body.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"response-bench\"}}\n\n")
				body.WriteString("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"message-bench\",\"role\":\"assistant\",\"content\":[]}}\n\n")
			}
			var text strings.Builder
			for range 128 {
				delta := "A representative streamed text fragment. "
				text.WriteString(delta)
				if api == ai.APIOpenAICompletions {
					fmt.Fprintf(&body, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\n", delta)
				} else {
					fmt.Fprintf(&body, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":%q}\n\n", delta)
				}
			}
			if api == ai.APIOpenAICompletions {
				body.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			} else {
				fmt.Fprintf(&body, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"message-bench\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n\n", text.String())
				body.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-bench\",\"status\":\"completed\"}}\n\n")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, body.String())
			}))
			defer server.Close()
			agentDir := b.TempDir()
			config := fmt.Sprintf(`{"providers":{"observation":{"baseUrl":%q,"api":%q,"apiKey":"test","models":[{"id":"probe","name":"Probe"}]}}}`, server.URL, api)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				b.Fatal(err)
			}
			services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: agentDir})
			if err != nil {
				b.Fatal(err)
			}
			model, err := BuildModel("observation/probe", services)
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = model.Provider.Close() }()
			request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(strings.Repeat("prompt ", 4096))}}}
			b.ReportAllocs()
			for b.Loop() {
				stream := services.ModelRuntime().StreamSimple(b.Context(), model, request, ai.StreamOptions{})
				for range stream.Events(b.Context()) {
				}
				result := stream.Result()
				if result.StopReason != ai.StopReasonStop || len(result.Content) != 1 || result.Content[0].(ai.TextContent).Text != text.String() {
					b.Fatalf("unexpected result: %#v", result)
				}
			}
		})
	}
}
