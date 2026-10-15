package ai_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A loopback port belongs to the host, not to the test that holds it: a process polling a port that another server held before reaches whichever
// server holds it now (on the Linux lab host a Go client outside the tests sends GET /readyz to 127.0.0.1 ports). Counted as an attempt, such a request takes
// the scripted response of the probe client's first attempt, so the attempt count and the message would depend on what else runs on the host, as
// TestProviderRetryMatchesPi's Google probes did under load (Pi 3 attempts, PiG 2). Each side's server must answer only the requests of the client
// the probe drives.
func TestResponseOracleCountsOnlyTheProbeClientsRequests(t *testing.T) {
	failure := responseProbe{ContentType: "application/json", SSE: `{"error":{"message":"x"}}`}
	google := "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"
	targets := []struct {
		module, name, provider string
		api                    ai.API
		stream                 func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
		success                string
		// sides are the servers a stray can reach: Pi answers the OpenAI SDK through options.fetch, without a server.
		sides []string
	}{
		{"google-generative-ai.js", "google-generative-ai", "google", ai.APIGoogleGenerativeAI, ai.StreamGoogle, google, []string{"pi", "pig"}},
		{"google-vertex.js", "google-vertex", "google-vertex", ai.APIGoogleVertex, ai.StreamGoogleVertex, google, []string{"pi", "pig"}},
		{"openai-completions.js", "openai-completions", "openai", ai.APIOpenAICompletions, ai.StreamOpenAICompletions, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", []string{"pig"}},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			t.Parallel()
			// 429, then 500 with the one retry spent: two attempts and the 500's message. A stray counted first would leave the client 500 and then
			// the success, three attempts and content.
			first, second := failure, failure
			first.Status, second.Status = 429, 500
			var probes []responseProbe
			for _, side := range append([]string{""}, target.sides...) {
				probes = append(probes, responseProbe{Provider: target.provider, Options: map[string]any{"maxRetries": float64(1)}, StrayAt: side, Sequence: []responseProbe{first, second, {SSE: target.success}}})
			}
			checkResponsesAllowEmpty(t, target.module, target.name, target.api, target.stream, probes)
		})
	}
}
