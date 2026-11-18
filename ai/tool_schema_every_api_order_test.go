package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A tool whose schema arrives as JSON text (an extension over the wire, an MCP server, a built-in's source literal) is sent to every provider in
// the order its author wrote, at the top level and inside properties: the bytes are part of the prompt and of the provider's prompt-cache key.
func TestToolSchemaTextKeepsItsMemberOrderOnEveryProviderWire(t *testing.T) {
	const schema = `{"type":"object","properties":{"zeta":{"type":"string","description":"Z"},"alpha":{"type":"number"}},"required":["zeta"]}`
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"lookup","description":"Look up","parameters":`+schema+`}`), &tool); err != nil {
		t.Fatal(err)
	}
	strictTrue := true
	cases := []struct {
		name  string
		build func(url string) Provider
	}{
		{"anthropic-messages", func(url string) Provider {
			return NewAnthropicProvider(AnthropicConfig{BaseURL: url, APIKey: "k", Model: "claude-haiku-4-5", ProviderID: "anthropic"})
		}},
		{"openai-completions", func(url string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "k", Model: "grok-2", ProviderID: "xai", Compat: &OpenAICompat{SupportsStore: new(false)}})
		}},
		{"openai-responses", func(url string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "k", Model: "gpt-5", ProviderID: "openai"})
		}},
		{"openai-responses strict", func(url string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "k", Model: "gpt-5", ProviderID: "openai", Compat: &OpenAIResponsesCompat{SupportsStrictMode: &strictTrue}})
		}},
		{"azure-openai-responses", func(url string) Provider {
			return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "k", Model: "gpt-4", ProviderID: "azure", Env: ProviderEnv{"AZURE_OPENAI_BASE_URL": url}})
		}},
		{"openai-codex-responses", func(url string) Provider {
			return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{BaseURL: url, APIKey: codexTestToken(t, "acct"), Model: "gpt-5.2", ProviderID: "openai-codex"})
		}},
		{"google-generative-ai", func(url string) Provider {
			return NewGoogleProvider(GoogleConfig{BaseURL: url + "/v1beta", APIKey: "k", Model: "gemini-1.5-flash", ProviderID: "google"})
		}},
		{"google-vertex", func(url string) Provider {
			return newGoogleVertexTestProvider(GoogleVertexConfig{BaseURL: url + "/v1/publishers/google", APIKey: "k", Model: "gemini-1.5-flash", ProviderID: "google-vertex"}, nil)
		}},
		{"mistral-conversations", func(url string) Provider {
			return newMistralTestProvider(MistralConfig{BaseURL: url, APIKey: "k", Model: "codestral-latest", ProviderID: "mistral"}, nil)
		}},
		{"pi-messages", func(url string) Provider {
			return NewPiMessagesProvider(PiMessagesConfig{BaseURL: url, APIKey: "k", Model: "m", ProviderID: "pi"})
		}},
	}
	// A build with the API's pig_strip tag has no provider for the row.
	strippable := map[string]API{"google-vertex": APIGoogleVertex, "mistral-conversations": APIMistralConversations}
	for _, c := range cases {
		if api, ok := strippable[c.name]; ok && !BuiltAPI(api) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				if r.Header.Get("Content-Encoding") == "zstd" {
					var err error
					if body, err = decodeZstdFrameForTest(body); err != nil {
						t.Error(err)
					}
				}
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte(`{"error":{"message":"probe stop"}}`))
			}))
			defer server.Close()
			provider := c.build(server.URL)
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}, Tools: []ToolSchema{tool}}), StreamOptions{})
			if err == nil { // google and mistral hand the 418 back as the error of Stream
				stream.Result()
			}
			if !strings.Contains(string(body), schema) {
				t.Errorf("the request body does not carry the schema as written\nschema %s\nbody   %s", schema, body)
			}
		})
	}
}
