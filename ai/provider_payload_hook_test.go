package ai

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// upstream: packages/ai/src/api/{anthropic-messages,azure-openai-responses,google-generative-ai,google-vertex,mistral-conversations,openai-codex-responses,openai-completions,openai-responses,pi-messages}.ts
// each stream function calls `options?.onPayload?.(params, model)` once with the selected model (which always has its api) and the built payload,
// and a hook error ends the stream with that error. A provider configured without a selected model hands the hook the identity it knows, api included.
func TestProviderPayloadHookReceivesModelAPIAndPayload(t *testing.T) {
	const modelID = "hook-model"
	fail := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("no network") })}
	for _, tc := range []struct {
		name     string
		api      API
		provider Provider
	}{
		{"openai-completions", APIOpenAICompletions, NewOpenAIProvider(OpenAIConfig{Model: modelID, ProviderID: "openai", APIKey: "k", BaseURL: "https://example.invalid/v1"})},
		{"openai-responses", APIOpenAIResponses, NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: modelID, ProviderID: "openai", APIKey: "k", BaseURL: "https://example.invalid/v1"})},
		{"openai-codex-responses", APIOpenAICodexResponses, NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{Model: modelID, APIKey: codexToken(), BaseURL: "https://example.invalid"})},
		{"azure-openai-responses", APIAzureOpenAIResponses, NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{Model: modelID, APIKey: "k", ProviderID: "azure-openai-responses", BaseURL: "https://example.invalid/openai/v1"})},
		{"anthropic-messages", APIAnthropicMessages, NewAnthropicProvider(AnthropicConfig{Model: modelID, ProviderID: "anthropic", APIKey: "k", BaseURL: "https://example.invalid"})},
		{"mistral-conversations", APIMistralConversations, newMistralTestProvider(MistralConfig{Model: modelID, APIKey: "k", BaseURL: "https://example.invalid"}, nil)},
		{"google-generative-ai", APIGoogleGenerativeAI, NewGoogleProvider(GoogleConfig{Model: modelID, ProviderID: "google", APIKey: "k", BaseURL: "http://127.0.0.1:1"})},
		{"google-vertex", APIGoogleVertex, newGoogleVertexTestProvider(GoogleVertexConfig{Model: modelID, APIKey: "k", BaseURL: "http://127.0.0.1:1"}, nil)},
		{"pi-messages", APIPiMessages, NewPiMessagesProvider(PiMessagesConfig{Model: modelID, ProviderID: "pi", APIKey: "k", BaseURL: "https://example.invalid"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() { _ = tc.provider.Close() }()
			for _, reject := range []bool{false, true} {
				var calls int
				var gotAPI API
				var gotPayload []byte
				opts := StreamOptions{Fetch: fail, OnPayload: func(payload any, model *Model) (any, error) {
					calls++
					gotAPI = model.ProviderMeta.API
					gotPayload, _ = json.Marshal(payload)
					if reject {
						return nil, errors.New("hook rejected")
					}
					return nil, nil
				}}
				if strings.HasPrefix(tc.name, "google-") {
					// The Google adapters reject a custom fetch, so they dial the closed port of their base URL.
					opts.Fetch = nil
				}
				stream, err := tc.provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}}), opts)
				message := ""
				if err != nil {
					message = err.Error()
				} else {
					message = stream.Result().ErrorMessage
				}
				if calls != 1 {
					t.Fatalf("reject=%v: hook called %d times, want 1", reject, calls)
				}
				if gotAPI != tc.api {
					t.Errorf("reject=%v: hook model api=%q, want %q", reject, gotAPI, tc.api)
				}
				if !strings.Contains(string(gotPayload), modelID) || !strings.Contains(string(gotPayload), "hello") {
					t.Errorf("reject=%v: hook payload=%s, want the serialized request for %s", reject, gotPayload, modelID)
				}
				if reject && !strings.Contains(message, "hook rejected") {
					t.Errorf("hook error not propagated: %q", message)
				}
			}
		})
	}
}
