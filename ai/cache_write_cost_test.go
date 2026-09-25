package ai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

// Upstream: packages/ai/test/anthropic-cache-write-1h-cost.test.ts. Both cases must reach provider parsing and pricing, not just CalculateCost on constructed Usage.
func TestAnthropicStreamCacheWrite1hCost(t *testing.T) {
	for _, tc := range []struct {
		name      string
		breakdown string
		wantLong  int
		wantCost  float64
	}{
		{
			name:      "mixed short and long writes",
			breakdown: `,"cache_creation":{"ephemeral_5m_input_tokens":600000,"ephemeral_1h_input_tokens":400000}`,
			wantLong:  400_000,
			wantCost:  7.75,
		},
		{name: "missing breakdown uses short rate", wantLong: 0, wantCost: 6.25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sse := fmt.Sprintf(`event: message_start
data: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":1000000%s}}}

`, tc.breakdown)
			// message_delta intentionally omits cache_creation: the start event's breakdown must survive.
			sse += `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":1000000}}

event: message_stop
data: {"type":"message_stop"}

`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, sse)
			}))
			defer server.Close()
			provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "claude-opus-4-8", BaseURL: server.URL})
			defer func() { _ = provider.Close() }()
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{
				// The pinned upstream case uses input=$5/M and short cache writes=$6.25/M; long writes cost 2x input.
				ModelCost: ModelCost{Input: 5, CacheWrite: 6.25},
			})
			if err != nil {
				t.Fatal(err)
			}
			assertStreamCacheWriteCost(t, stream, tc.wantLong, tc.wantCost)
		})
	}
}

// Upstream: packages/ai/test/bedrock-cache-write-1h-cost.test.ts. Separate 1h details surround a 5m detail; neither last-value assignment nor summing all TTLs is correct.
func TestBedrockStreamCacheWrite1hCost(t *testing.T) {
	var response bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, event := range []struct{ kind, payload string }{
		{"messageStart", `{"role":"assistant"}`},
		{"metadata", `{"usage":{"inputTokens":100,"outputTokens":5,"totalTokens":1000105,"cacheWriteInputTokens":1000000,"cacheDetails":[{"ttl":"1h","inputTokens":150000},{"ttl":"5m","inputTokens":600000},{"ttl":"1h","inputTokens":250000}]}}`},
		{"messageStop", `{"stopReason":"end_turn"}`},
	} {
		var headers eventstream.Headers
		headers.Set(":message-type", eventstream.StringValue("event"))
		headers.Set(":event-type", eventstream.StringValue(event.kind))
		headers.Set(":content-type", eventstream.StringValue("application/json"))
		if err := encoder.Encode(&response, eventstream.Message{Headers: headers, Payload: []byte(event.payload)}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(response.Bytes())
	}))
	defer server.Close()

	// The real SDK decodes the fixture, but neither ambient profiles nor credential discovery may reach an external service.
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL)
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{
		Env:       ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1", "PI_CACHE_RETENTION": "none"},
		ModelCost: ModelCost{Input: 5, CacheWrite: 6.25},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStreamCacheWriteCost(t, stream, 400_000, 7.75)
}

func assertStreamCacheWriteCost(t *testing.T, stream *AssistantMessageEventStream, wantLong int, wantCost float64) {
	t.Helper()
	result := stream.Result()
	if result.StopReason != StopReasonStop || result.ErrorMessage != "" {
		t.Fatalf("stream failed: stop=%q error=%q", result.StopReason, result.ErrorMessage)
	}
	usage := result.Usage
	if usage.CacheWrite != 1_000_000 || usage.CacheWrite1h == nil || *usage.CacheWrite1h != wantLong {
		t.Fatalf("usage = %#v; want cacheWrite=1000000, cacheWrite1h=%d", usage, wantLong)
	}
	if math.Abs(usage.Cost.CacheWrite-wantCost) > 1e-10 {
		t.Fatalf("cache write cost = %.12f, want %.12f", usage.Cost.CacheWrite, wantCost)
	}
	if usage.Input != 100 || usage.Output != 5 || usage.CacheRead != 0 || usage.TotalTokens != 1_000_105 {
		t.Fatalf("usage = %#v; want input=100 output=5 cacheRead=0 total=1000105", usage)
	}
	var terminal AssistantMessageEvent
	for event := range stream.Events(context.Background()) {
		terminal = event
	}
	done, ok := terminal.(DoneEvent)
	if !ok || done.Message != result {
		t.Fatalf("terminal event = %#v, want DoneEvent with the priced result", terminal)
	}
}
