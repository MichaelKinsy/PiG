package ai_test

import (
	"context"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/*.ts createClient / buildHeaders

// The URL path, method and headers each API module sends (authorization forms, API-specific headers, session-affinity headers, caller header
// overrides) for a stream, read through the pinned pi-ai's options.fetch and PiG's HTTP client. Headers naming the client library or the
// transport are not compared (see comparableRequest).
func TestProviderRequestHeadersMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(149))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	type target struct {
		module, name string
		providers    []string
		keys         []string
		api          ai.API
		stream       func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
	}
	targets := []target{
		{"openai-completions.js", "openai-completions", []string{"custom", "openrouter", "github-copilot", "openai", "zai"}, []string{"k"}, ai.APIOpenAICompletions, ai.StreamOpenAICompletions},
		{"anthropic-messages.js", "anthropic-messages", []string{"anthropic", "openrouter", "github-copilot", "custom"}, []string{"sk-ant-api03-x", "sk-ant-oat01-x", "plain"}, ai.APIAnthropicMessages, ai.StreamAnthropic},
		{"openai-responses.js", "openai-responses", []string{"openai", "github-copilot", "xai", "custom"}, []string{"k"}, ai.APIOpenAIResponses, ai.StreamOpenAIResponses},
		{"mistral-conversations.js", "mistral-conversations", []string{"mistral"}, []string{"k"}, ai.APIMistralConversations, ai.StreamMistral},
		{"google-generative-ai.js", "google-generative-ai", []string{"google", "custom"}, []string{"k"}, ai.APIGoogleGenerativeAI, ai.StreamGoogle},
		{"google-vertex.js", "google-vertex", []string{"google-vertex", "custom"}, []string{"k"}, ai.APIGoogleVertex, ai.StreamGoogleVertex},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			var probes []responseProbe
			for range 60 {
				probe := responseProbe{Provider: pick(target.providers...), APIKey: pick(target.keys...), SSE: "", CheckRequest: true, Status: 400}
				if rng.Intn(2) == 0 {
					probe.SessionID = pick("s1", "session-with-a-longer-id-0123456789")
				}
				if rng.Intn(3) == 0 {
					probe.Headers = map[string]string{pick("x-custom", "X-Mixed-Case", "authorization", "x-api-key"): pick("v1", "v two")}
				}
				probes = append(probes, probe)
			}
			checkResponsesAllowEmpty(t, target.module, target.name, target.api, target.stream, probes)
		})
	}
}
