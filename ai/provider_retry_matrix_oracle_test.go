package ai_test

import (
	"context"
	"encoding/base64"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/utils/provider-retry.ts retryProviderRequest

// Which statuses and response headers (x-should-retry, retry-after-ms, retry-after, over-long delays) make a provider request retry, how many attempts
// are made for options.maxRetries / maxRetryDelayMs, and the message left when the retries run out, read from the pinned pi-ai and from PiG.
func TestProviderRetryMatchesPi(t *testing.T) {
	successes := map[string]string{
		"openai-completions":     "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		"anthropic-messages":     "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		"openai-responses":       "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
		"google-generative-ai":   "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n",
		"google-vertex":          "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n",
		"azure-openai-responses": "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
		"openai-codex-responses": "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
		"mistral-conversations":  "data: {\"data\":{\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finishReason\":\"stop\"}]}}\n\ndata: [DONE]\n\n",
	}
	type target struct {
		module, name string
		provider     string
		api          ai.API
		stream       func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
	}
	targets := []target{
		{"openai-completions.js", "openai-completions", "openai", ai.APIOpenAICompletions, ai.StreamOpenAICompletions},
		{"anthropic-messages.js", "anthropic-messages", "anthropic", ai.APIAnthropicMessages, ai.StreamAnthropic},
		{"openai-responses.js", "openai-responses", "openai", ai.APIOpenAIResponses, ai.StreamOpenAIResponses},
		{"mistral-conversations.js", "mistral-conversations", "mistral", ai.APIMistralConversations, ai.StreamMistral},
		{"google-generative-ai.js", "google-generative-ai", "google", ai.APIGoogleGenerativeAI, ai.StreamGoogle},
		{"google-vertex.js", "google-vertex", "google-vertex", ai.APIGoogleVertex, ai.StreamGoogleVertex},
		{"azure-openai-responses.js", "azure-openai-responses", "azure-openai-responses", ai.APIAzureOpenAIResponses, ai.StreamAzureOpenAIResponses},
		{"openai-codex-responses.js", "openai-codex-responses", "openai-codex", ai.APIOpenAICodexResponses, ai.StreamOpenAICodexResponses},
	}
	rng := rand.New(rand.NewSource(151))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			var probes []responseProbe
			// A server-requested delay above the cap fails with the SDK error's own message appended, for each shape of error body.
			for _, body := range []string{`{"error":{"message":"x"}}`, `{"message":"m"}`, `plain text`, ``, `[1]`, `null`, `{"error":""}`, `{"error":0}`, `{"error":false}`, `{"error":"text"}`, `{"error":1}`, `{"error":{"code":5,"type":"usage_limit_reached"}}`, `{"error":{"plan_type":7}}`, `{"error":{"resets_at":"1"}}`, `{"error":{"resets_at":"x"}}`, `{"error":{"resets_at":true}}`, `{"error":{"message":5}}`, `{"error":{"message":{"a":1}}}`, "\ufeff\ufeff{\"error\":{\"message\":\"two marks\"}}"} {
				for _, status := range []int{429, 500, 503, 408} {
					// retryProviderRequest reads the delay only when a retry remains (provider-retry.ts:120-125); maxRetries defaults to 0. A Google error carries
					// no headers (google-shared.ts:494-506), so a retry there waits the exponential delay instead; the random sequences below cover it.
					options := map[string]any{}
					if !strings.HasPrefix(target.name, "google") {
						options["maxRetries"] = float64(1)
					}
					if status%2 == 0 {
						options["maxRetryDelayMs"] = float64(1000)
					}
					probe := responseProbe{Provider: target.provider, Options: options, Sequence: []responseProbe{{Status: status, ContentType: "application/json", SSE: body, ResponseHeaders: map[string]string{"retry-after-ms": "100000"}}}}
					if target.name == "openai-codex-responses" {
						probe.APIKey = "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`)) + ".y"
						probe.Options["transport"] = "sse"
					}
					probes = append(probes, probe)
				}
			}
			// openai-codex-responses retries on its own schedule (exponential backoff when the server names no delay), so it gets fewer sequences.
			count := 80
			if target.name == "openai-codex-responses" {
				count = 25
			}
			for range count {
				probe := responseProbe{Provider: target.provider, Options: map[string]any{}}
				if target.name == "openai-codex-responses" {
					probe.APIKey = "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`)) + ".y"
					probe.Options["transport"] = "sse"
				}
				if n := rng.Intn(5); n > 0 {
					probe.Options["maxRetries"] = float64(n - 1)
				}
				if rng.Intn(3) == 0 {
					probe.Options["maxRetryDelayMs"] = float64(pick("0", "50", "5000")[0]-'0') * 10
				}
				for range 1 + rng.Intn(3) {
					step := responseProbe{Status: []int{408, 409, 429, 500, 502, 503, 529, 400, 401, 404, 422}[rng.Intn(11)], ContentType: "application/json", SSE: `{"error":{"message":"x"}}`, ResponseHeaders: map[string]string{}}
					if rng.Intn(5) > 0 {
						switch rng.Intn(5) {
						case 0:
							step.ResponseHeaders["retry-after-ms"] = pick("0", "1", "3", "2.5", "abc", "200", "100000")
						case 1:
							step.ResponseHeaders["retry-after"] = pick("0", "1", "abc", "0.002", "-1", "Wed, 21 Oct 2015 07:28:00 GMT")
						case 2:
							step.ResponseHeaders["retry-after-ms"] = pick("0", "2")
							step.ResponseHeaders["retry-after"] = pick("30", "0")
						default:
							step.ResponseHeaders["retry-after-ms"] = "1"
						}
					}
					if rng.Intn(4) == 0 {
						step.ResponseHeaders["x-should-retry"] = pick("true", "false", "TRUE")
					}
					probe.Sequence = append(probe.Sequence, step)
				}
				if rng.Intn(4) > 0 {
					probe.Sequence = append(probe.Sequence, responseProbe{SSE: successes[target.name]})
				}
				probes = append(probes, probe)
			}
			// The probes are drawn from rng before the subtest pauses here, in target order; the eight comparisons then run in parallel.
			t.Parallel()
			checkResponsesAllowEmpty(t, target.module, target.name, target.api, target.stream, probes)
		})
	}
}
