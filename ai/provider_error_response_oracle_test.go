package ai_test

import (
	"context"
	"encoding/base64"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/*.ts error handling, packages/ai/src/utils/provider-errors

// Error responses (a status other than 200 with a JSON, text or empty body, a 200 that is not an event stream) answered to each API module: the
// pinned pi-ai and PiG must end with the same events, stop reason and error message. Statuses that Pi retries (408, 409, 429, 5xx) are left out.
func TestProviderErrorResponseEventsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(139))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	bodies := []string{
		``,
		`{"error":{"message":"bad request","type":"invalid_request_error","code":"invalid"}}`,
		`{"error":"plain string error"}`,
		`{"message":"top level message"}`,
		`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
		`<html>nope</html>`,
		`not json`,
		`{"error":{"message":"` + "é😀 <&>" + `"}}`,
		`{"detail":"fastapi style"}`,
		`[]`,
		`{"message":""}`, `{"message":0}`, `{"message":{"b":1,"a":2}}`, `{"message":["x"]}`, `"string body"`, `0`, `null`, `false`, `""`,
		` {"message" : "padded", "x":1} `, `{ "b" : 1, "a" : "\u00e9", "1": true }`, `{"error":{"message":"dup"},"error":"last"}`,
	}
	type target struct {
		module, name, provider, key string
		api                         ai.API
		stream                      func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
	}
	targets := []target{
		{"openai-completions.js", "openai-completions", "custom", "k", ai.APIOpenAICompletions, ai.StreamOpenAICompletions},
		{"anthropic-messages.js", "anthropic-messages", "anthropic", "sk-ant-api03-x", ai.APIAnthropicMessages, ai.StreamAnthropic},
		{"openai-responses.js", "openai-responses", "openai", "k", ai.APIOpenAIResponses, ai.StreamOpenAIResponses},
		{"mistral-conversations.js", "mistral-conversations", "mistral", "k", ai.APIMistralConversations, ai.StreamMistral},
		{"google-generative-ai.js", "google-generative-ai", "google", "k", ai.APIGoogleGenerativeAI, ai.StreamGoogle},
		{"google-vertex.js", "google-vertex", "google-vertex", "k", ai.APIGoogleVertex, ai.StreamGoogleVertex},
		{"azure-openai-responses.js", "azure-openai-responses", "azure-openai-responses", "k", ai.APIAzureOpenAIResponses, ai.StreamAzureOpenAIResponses},
		{"openai-codex-responses.js", "openai-codex-responses", "openai-codex", "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`)) + ".y", ai.APIOpenAICodexResponses, ai.StreamOpenAICodexResponses},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			var probes []responseProbe
			for range 120 {
				probe := responseProbe{Provider: target.provider, APIKey: target.key, Status: []int{400, 401, 403, 404, 422, 200}[rng.Intn(6)], SSE: bodies[rng.Intn(len(bodies))]}
				if target.name == "openai-codex-responses" {
					probe.Options = map[string]any{"transport": "sse"}
				}
				probe.ContentType = pick("application/json", "text/plain", "text/event-stream", "text/html")
				probes = append(probes, probe)
			}
			checkResponsesAllowEmpty(t, target.module, target.name, target.api, target.stream, probes)
		})
	}
}
