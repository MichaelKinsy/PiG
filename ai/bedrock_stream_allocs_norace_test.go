//go:build !race && !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"net"
	"runtime"
	"testing"
)

// bedrockFrameAllocCeiling is the allocations one more contentBlockDelta frame costs on the Bedrock ConverseStream path (event-stream decode, builder, event stream), measured as the marginal cost between a short and a long response so the request setup cancels. It only goes down.
const bedrockFrameAllocCeiling = 68

// bedrockStreamMallocs is the heap allocations of one whole streamed response of frames deltas, the least of three runs.
func bedrockStreamMallocs(t *testing.T, frames int) uint64 {
	t.Helper()
	body := bedrockBenchFrame("messageStart", `{"role":"assistant"}`)
	for range frames {
		body = append(body, bedrockBenchFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"word "}}`)...)
	}
	body = append(body, bedrockBenchFrame("contentBlockStop", `{"contentBlockIndex":0}`)...)
	body = append(body, bedrockBenchFrame("messageStop", `{"stopReason":"end_turn"}`)...)
	body = append(body, bedrockBenchFrame("metadata", `{"usage":{"inputTokens":1,"outputTokens":2,"totalTokens":3}}`)...)
	run := func() uint64 {
		server := newBedrockFixtureServer(t, nil, body, true)
		server.openOnce()
		_, port, _ := net.SplitHostPort(server.listener.Addr().String())
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		provider := bedrockObservationProvider(port)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{
			APIKey: "test", Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		events := 0
		for range stream.Events(ctx) {
			events++
		}
		runtime.ReadMemStats(&after)
		if result := stream.Result(); result.StopReason != StopReasonStop || events != frames+4 {
			t.Fatalf("stop=%s events=%d (want %d) error=%q", result.StopReason, events, frames+4, result.ErrorMessage)
		}
		return after.Mallocs - before.Mallocs
	}
	run()
	best := ^uint64(0)
	for range 3 {
		best = min(best, run())
	}
	return best
}

func TestBedrockStreamFrameAllocationCeiling(t *testing.T) {
	const short, long = 400, 1400
	perFrame := float64(bedrockStreamMallocs(t, long)-bedrockStreamMallocs(t, short)) / float64(long-short)
	t.Logf("bedrock-text: %.1f allocs/frame (ceiling %d)", perFrame, bedrockFrameAllocCeiling)
	if perFrame > bedrockFrameAllocCeiling {
		t.Errorf("bedrock-text allocates %.1f times per frame, ceiling %d", perFrame, bedrockFrameAllocCeiling)
	}
}
