//go:build !pig_strip_mistral_conversations

package ai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// BenchmarkProviderRequestLongHistory sends a request carrying a 40-turn history of 50 KB tool results through each provider.
func BenchmarkProviderRequestLongHistory(b *testing.B) {
	file := strings.Repeat("Benchmark fixture line with <tags> & text.\n", 1200)
	messages := make([]Message, 0, 161)
	for i := range 40 {
		id := fmt.Sprintf("call_%d", i)
		messages = append(messages,
			UserMessage{Content: UserText(fmt.Sprintf("Turn %d. Read README.md.", i))},
			AssistantMessage{API: "openai-completions", Provider: "openai", Model: "gpt-bench", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: id, Name: "read", Arguments: JsonObject{"path": "/work/README.md"}}}},
			ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: file + fmt.Sprint(i)}}},
			AssistantMessage{API: "openai-completions", Provider: "openai", Model: "gpt-bench", StopReason: StopReasonStop, Content: []AssistantContentBlock{TextContent{Text: "The file is a fixture."}}},
		)
	}
	messages = append(messages, UserMessage{Content: UserText("next")})
	history := NormalizeContext(Context{Messages: messages, Frozen: true})
	for _, shape := range streamFrameShapes {
		if strings.HasSuffix(shape.name, "toolcall") {
			continue
		}
		b.Run(shape.name, func(b *testing.B) {
			body := shape.body(1)
			var sent int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n, _ := io.Copy(io.Discard, r.Body)
				sent = int(n)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			provider := shape.provider(server.URL)
			// A product binary does not check its requests; the test binary does.
			ctx := requestvalidity.Allow(b.Context())
			run := func() {
				stream, err := provider.Stream(ctx, history, StreamOptions{})
				if err != nil {
					b.Fatal(err)
				}
				for range stream.Events(ctx) {
				}
			}
			run()
			b.ReportMetric(float64(sent), "request-bytes")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				run()
			}
		})
	}
}
