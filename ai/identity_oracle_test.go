package ai_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/utils/identity.ts getIdentityHeaders, applied by every packages/ai/src/api/*.ts createClient

// The request each API module sends for a request identity, read through the pinned pi-ai's options.fetch and PiG's HTTP client: the custom
// User-Agent, the attribution headers of the OpenRouter, NVIDIA and OpenCode targets, and the Codex originator, which no identity changes. The
// identity names acme, so both sides spell the same values; PiG's own product and attribution values are D26 and D65, locked in identity_test.go.
func TestProviderIdentityHeadersMatchPi(t *testing.T) {
	codexToken := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`)) + ".y"
	identities := []map[string]any{
		{"userAgent": map[string]any{"custom": "acme/1.0"}},
		{"userAgent": map[string]any{"custom": "acme/1.0"}, "attribution": map[string]any{"name": "acme", "url": "https://acme.example", "categories": []string{"cli-agent", "cloud-agent"}}},
		{"userAgent": map[string]any{"custom": "acme/1.0"}, "attribution": map[string]any{"name": "acme", "url": "https://acme.example", "categories": []string{}}},
		{"userAgent": map[string]any{"custom": "acme/1.0"}, "attribution": nil},
		// Pi's own attribution values, which PiG's default attribution replaces (D26): the headers they map to are the same code on both sides.
		{"userAgent": map[string]any{"custom": "acme/1.0"}, "attribution": map[string]any{"name": "pi", "url": "https://pi.dev", "categories": []string{"cli-agent"}}},
	}
	type target struct {
		module, name string
		providers    []string
		keys         []string
		options      map[string]any
		api          ai.API
		stream       func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
	}
	targets := []target{
		{"openai-completions.js", "openai-completions", []string{"custom", "openrouter", "nvidia", "opencode", "opencode-go", "github-copilot"}, []string{"k"}, nil, ai.APIOpenAICompletions, ai.StreamOpenAICompletions},
		{"openai-responses.js", "openai-responses", []string{"openai", "openrouter", "nvidia", "opencode", "github-copilot"}, []string{"k"}, nil, ai.APIOpenAIResponses, ai.StreamOpenAIResponses},
		// An Anthropic OAuth token sends the Claude Code identity instead of the client identity.
		{"anthropic-messages.js", "anthropic-messages", []string{"anthropic", "openrouter", "opencode", "github-copilot"}, []string{"sk-ant-api03-x", "sk-ant-oat01-x"}, nil, ai.APIAnthropicMessages, ai.StreamAnthropic},
		{"mistral-conversations.js", "mistral-conversations", []string{"mistral", "openrouter"}, []string{"k"}, nil, ai.APIMistralConversations, ai.StreamMistral},
		{"google-generative-ai.js", "google-generative-ai", []string{"google", "openrouter"}, []string{"k"}, nil, ai.APIGoogleGenerativeAI, ai.StreamGoogle},
		{"google-vertex.js", "google-vertex", []string{"google-vertex"}, []string{"k"}, nil, ai.APIGoogleVertex, ai.StreamGoogleVertex},
		{"openai-codex-responses.js", "openai-codex-responses", []string{"openai-codex"}, []string{codexToken}, map[string]any{"transport": "sse"}, ai.APIOpenAICodexResponses, ai.StreamOpenAICodexResponses},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			var probes []responseProbe
			for _, provider := range target.providers {
				for _, key := range target.keys {
					for _, identity := range identities {
						options := map[string]any{"identity": identity}
						for name, value := range target.options {
							options[name] = value
						}
						probes = append(probes, responseProbe{Provider: provider, APIKey: key, Status: 400, CheckRequest: true, KeepUserAgent: true, Options: options, SessionID: "s1"})
					}
				}
			}
			checkResponsesAllowEmpty(t, target.module, target.name, target.api, target.stream, probes)
		})
	}
}

// An Anthropic OAuth token sends the Claude Code identity instead of the client identity, and a User-Agent header of the request replaces it: an exact-case
// "User-Agent" and a lower-case "user-agent" both, and on an API-key request too.
func TestAnthropicUserAgentHeaderReplacesTheClaudeCodeIdentityLikePi(t *testing.T) {
	var probes []responseProbe
	for _, key := range []string{"sk-ant-oat01-x", "sk-ant-api03-x"} {
		for _, headers := range []map[string]string{nil, {"User-Agent": "explicit/1.0"}, {"user-agent": "lower/1.0"}, {"User-Agent": "explicit/1.0", "x-app": "mine"}} {
			for _, identity := range []map[string]any{nil, {"userAgent": map[string]any{"custom": "acme/1.0"}}} {
				options := map[string]any{}
				if identity != nil {
					options["identity"] = identity
				}
				probes = append(probes, responseProbe{Provider: "anthropic", APIKey: key, Status: 400, CheckRequest: true, KeepUserAgent: identity != nil || headers != nil || key == "sk-ant-oat01-x", Options: options, Headers: headers})
			}
		}
	}
	checkResponsesAllowEmpty(t, "anthropic-messages.js", "anthropic-messages", ai.APIAnthropicMessages, ai.StreamAnthropic, probes)
}
