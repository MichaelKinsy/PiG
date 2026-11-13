package ai

import (
	"encoding/base64"
	"math/rand"
	"testing"
)

// providerErrorResponses returns seeded non-2xx responses: client, rate-limit and server statuses with JSON error envelopes of several shapes, plain text, HTML and empty bodies.
func providerErrorResponses() ([][][]byte, []int) {
	random := rand.New(rand.NewSource(20261109))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	bodyOptions := []string{
		`{"error":{"message":"bad things","type":"invalid_request_error","code":"x"}}`,
		`{"error":{"message":"bad things"}}`,
		`{"error":"bad things"}`,
		`{"message":"bad things"}`,
		`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		`{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`,
		`{"detail":"nope"}`,
		`[]`,
		`null`,
		`"text"`,
		`plain text failure`,
		`<html>502 Bad Gateway</html>`,
		``,
		`{"error":{"message":"é𝄞 unicode"}}`,
		// Ill-formed UTF-8: fetch's text() replaces each maximal subpart with one U+FFFD.
		"{\"error\":{\"message\":\"bad \xf0\x9d\x84 end\"}}",
		"plain \xe6\x97 failure\xff\xfe",
		"\xef\xbb\xbf{\"error\":{\"message\":\"bom\"}}",
		// undici's text() drops one byte order mark and its TextDecoder drops another; a third stays in the text.
		"\xef\xbb\xbf\xef\xbb\xbf{\"error\":{\"message\":\"two marks\"}}",
		"\xef\xbb\xbf\xef\xbb\xbf\xef\xbb\xbf{\"error\":{\"message\":\"three marks\"}}",
		"{\"error\":\"\xc0\xaf\xed\xa0\x80\"}",
	}
	statuses := []int{400, 401, 403, 404, 408, 409, 413, 422, 429, 500, 502, 503, 529}
	var bodies [][][]byte
	var codes []int
	for range 600 {
		codes = append(codes, statuses[random.Intn(len(statuses))])
		bodies = append(bodies, [][]byte{[]byte(pick(bodyOptions...))})
	}
	return bodies, codes
}

// providerErrorCase is one provider of TestProviderErrorResponsesMatchPi: Pi's module names and PiG's provider at a base URL.
type providerErrorCase struct {
	name string
	pi   [4]string
	make func(baseURL string) Provider
}

// providerDroppedCase is one provider of TestProviderDroppedConnectionMatchesPi: its stream bodies and whether D120 applies.
type providerDroppedCase struct {
	name   string
	pi     [4]string
	d120   bool
	bodies func() [][][]byte
	make   func(baseURL string) Provider
}

func TestProviderErrorResponsesMatchPi(t *testing.T) {
	bodies, statuses := providerErrorResponses()
	// The Mistral and Google Vertex cases come from provider_error_oracle_mistral_test.go and provider_error_oracle_google_vertex_test.go:
	// a Piglet built with pig_strip_mistral_conversations or pig_strip_google_vertex has no such provider, and this file is built for every Piglet.
	cases := []providerErrorCase{
		{"anthropic", [4]string{"anthropic-messages", "anthropicMessagesApi", "anthropic-messages", "anthropic"}, func(baseURL string) Provider {
			return NewAnthropicProvider(AnthropicConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"openai-completions", [4]string{"openai-completions", "openAICompletionsApi", "openai-completions", "openai"}, func(baseURL string) Provider {
			return NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"openai-responses", [4]string{"openai-responses", "openAIResponsesApi", "openai-responses", "openai"}, func(baseURL string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"google", [4]string{"google-generative-ai", "googleGenerativeAIApi", "google-generative-ai", "google"}, func(baseURL string) Provider {
			return NewGoogleProvider(GoogleConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
	}
	cases = append(cases, providerErrorMistralCases()...)
	cases = append(cases, providerErrorCase{"azure-openai-responses", [4]string{"azure-openai-responses", "azureOpenAIResponsesApi", "azure-openai-responses", "azure-openai-responses"}, func(baseURL string) Provider {
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	}})
	cases = append(cases, providerErrorGoogleVertexCases()...)
	cases = append(cases, providerErrorCase{"pi-messages", [4]string{"pi-messages", "piMessagesApi", "pi-messages", "pi"}, func(baseURL string) Provider {
		return NewPiMessagesProvider(PiMessagesConfig{APIKey: "k", Model: "m", ProviderID: "pi", BaseURL: baseURL})
	}})
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			providerResponseOracle(t, test.pi, true, bodies, statuses, false, test.make)
		})
	}
}

// TestOpenAICodexErrorResponsesMatchPi sends the same failed responses to Pi's openai-codex-responses provider (SSE transport) and Pig's: Codex reads the
// body with `await response.text()` (openai-codex-responses.ts:423) and builds its own error message from it.
func TestOpenAICodexErrorResponsesMatchPi(t *testing.T) {
	bodies, statuses := providerErrorResponses()
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`)) + ".sig"
	t.Setenv("PI_ORACLE_API_KEY", token)
	t.Setenv("PI_ORACLE_TRANSPORT", "sse")
	providerOracleTransport = TransportSSE
	t.Cleanup(func() { providerOracleTransport = "" })
	providerResponseOracle(t, [4]string{"openai-codex-responses", "openAICodexResponsesApi", "openai-codex-responses", "openai-codex"}, true, bodies, statuses, false, func(baseURL string) Provider {
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: token, Model: "m", BaseURL: baseURL})
	})
}

// A response whose connection breaks after some of the body: the stream ends with the failure and keeps what was received. the events, the partial content and every other error text must match.
func TestProviderDroppedConnectionMatchesPi(t *testing.T) {
	cases := []providerDroppedCase{
		{"anthropic", [4]string{"anthropic-messages", "anthropicMessagesApi", "anthropic-messages", "anthropic"}, true, anthropicStreamBodies, func(baseURL string) Provider {
			return NewAnthropicProvider(AnthropicConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"openai-completions", [4]string{"openai-completions", "openAICompletionsApi", "openai-completions", "openai"}, true, openAICompletionsStreamBodies, func(baseURL string) Provider {
			return NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"openai-responses", [4]string{"openai-responses", "openAIResponsesApi", "openai-responses", "openai"}, true, openAIResponsesStreamBodies, func(baseURL string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
		{"google", [4]string{"google-generative-ai", "googleGenerativeAIApi", "google-generative-ai", "google"}, false, googleStreamBodies, func(baseURL string) Provider {
			return NewGoogleProvider(GoogleConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
		}},
	}
	for _, test := range append(cases, providerDroppedMistralCases()...) {
		t.Run(test.name, func(t *testing.T) {
			providerResponseOracle(t, test.pi, test.d120, test.bodies(), nil, true, test.make)
		})
	}
}
