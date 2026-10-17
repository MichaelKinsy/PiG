package ai

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// Provider stream accumulation is linear in the number of deltas. Quadratic accumulation (re-copying the whole text or the whole
// tool-argument string on each delta) once cost 2.6 GB for 16,000 OpenAI text deltas and 10 GB for 8,000 Anthropic input_json_delta
// events (comb-audit row 7, audit-go-pig H2). Quadruple the deltas: linear growth allocates about 4x, quadratic about 16x.
func TestProviderStreamAllocationsGrowLinearlyWithDeltas(t *testing.T) {
	const growthLimit = 6.0
	run := func(t *testing.T, serve func(w *bufio.Writer, n int), provider func(url string) Provider) {
		measure := func(n int) uint64 {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writer := bufio.NewWriter(w)
				serve(writer, n)
				_ = writer.Flush()
			}))
			defer server.Close()
			p := provider(server.URL)
			request := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			stream, err := p.Stream(t.Context(), request, StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if message := stream.Result(); message.StopReason == StopReasonError || message.StopReason == StopReasonAborted {
				t.Fatalf("n=%d: %s", n, message.ErrorMessage)
			}
			runtime.ReadMemStats(&after)
			return after.TotalAlloc - before.TotalAlloc
		}
		measure(256) // warm one-time setup so it is not charged to the small run
		small, large := measure(1000), measure(4000)
		t.Logf("1000 -> 4000 deltas: %d KB -> %d KB", small>>10, large>>10)
		if float64(large) > growthLimit*float64(small) {
			t.Fatalf("4x the deltas allocated %.1fx the bytes; linear accumulation stays under %.0fx", float64(large)/float64(small), growthLimit)
		}
	}
	t.Run("openai text deltas", func(t *testing.T) {
		chunk := strings.Repeat("x", 20)
		run(t, func(w *bufio.Writer, n int) {
			for range n {
				_, _ = fmt.Fprintf(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", chunk)
			}
			_, _ = fmt.Fprint(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}, func(url string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "k", Model: "m"})
		})
	})
	t.Run("anthropic tool argument deltas", func(t *testing.T) {
		run(t, func(w *bufio.Writer, n int) {
			_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			_, _ = fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t\",\"name\":\"write\",\"input\":{}}}\n\n")
			_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"a.txt\\\",\\\"content\\\":\\\"\"}}\n\n")
			for range n {
				_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"abcdefghijklmnopqrst\"}}\n\n")
			}
			_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"}\"}}\n\n")
			_, _ = fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
			_, _ = fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":5}}\n\n")
			_, _ = fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}, func(url string) Provider {
			return NewAnthropicProvider(AnthropicConfig{BaseURL: url, APIKey: "k", Model: "claude-sonnet-4-5"})
		})
	})
}
