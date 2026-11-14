package ai

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// providerStreamEventRejectionCase is one API and credential shape of TestProviderStreamEventObserverRejectionEndsTheStreamOnEveryAPI: run streams one
// reply under options and returns the final message.
type providerStreamEventRejectionCase struct {
	name  string
	shape string
	run   func(t *testing.T, options StreamOptions) *AssistantMessage
}

// Every provider adapter awaits options.onProviderStreamEvent before it maps the event (for await ... { await onProviderStreamEvent(event, model); ... }), so an
// observer that rejects ends the stream at that event: the stream result is the error with the observer's message, the event is not normalized into content, and no
// later event reaches the observer. The shapes cover API key, a custom base URL, an OAuth bearer (the chatgpt Responses model, an sk-ant-oat Anthropic token and
// Vertex's ADC token) and both openai-completions and openai-responses.
func TestProviderStreamEventObserverRejectionEndsTheStreamOnEveryAPI(t *testing.T) {
	const reason = "the observer rejected the first event"
	completionsFirst := `{"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{"content":"hello"}}]}`
	completionsFinal := `{"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	created := `{"type":"response.created","sequence_number":0,"response":{"id":"resp_1"}}`
	completed := `{"type":"response.completed","sequence_number":1,"response":{"id":"resp_1","status":"completed"}}`
	googleFirst := `{"responseId":"resp_google","candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`
	googleSecond := `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`
	mistralFirst := `{"id":"response-1","choices":[{"index":0,"finish_reason":null,"delta":{"content":"hello"}}]}`
	mistralSecond := `{"id":"response-1","choices":[{"index":0,"finish_reason":"stop","delta":{}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

	cases := []providerStreamEventRejectionCase{
		{"openai-completions", "API key, custom base URL", func(t *testing.T, options StreamOptions) *AssistantMessage {
			server := serveSSE(t, sseFrames(completionsFirst, completionsFinal, "[DONE]"))
			model := &Model{ID: "m", DisplayName: "m", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192}, ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom", BaseURL: server.URL}}
			options.APIKey = "test"
			stream, err := StreamSimple(t.Context(), model, helloTranscript(), options)
			if err != nil {
				t.Fatal(err)
			}
			return stream.Result()
		}},
		{"openai-responses", "API key, custom base URL", func(t *testing.T, options StreamOptions) *AssistantMessage {
			server := serveSSE(t, sseFrames(created, completed))
			model := mustGeneratedModel(t, "openai", "gpt-4o-mini").ToModel()
			model.ProviderMeta.BaseURL = server.URL
			options.APIKey = "test"
			stream, err := StreamSimple(t.Context(), model, userTranscript("hello"), options)
			if err != nil {
				t.Fatal(err)
			}
			return stream.Result()
		}},
		{"openai-responses (chatgpt)", "OAuth bearer", func(t *testing.T, options StreamOptions) *AssistantMessage {
			model := chatGPTTestModel("https://api.openai.com/v1", nil)
			provider, err := directAPIProvider(model, "test", nil)
			if err != nil {
				t.Fatal(err)
			}
			options.Fetch = sseResponse(sseFrames(created, completed))
			stream, err := provider.Stream(t.Context(), responsesUpstreamContext(), options)
			if err != nil {
				t.Fatal(err)
			}
			return stream.Result()
		}},
		{"azure-openai-responses", "API key, custom base URL", func(t *testing.T, options StreamOptions) *AssistantMessage {
			server := serveSSE(t, sseFrames(created, completed))
			model := mustGeneratedModel(t, "azure", "gpt-4o-mini").ToModel()
			model.ProviderMeta.BaseURL = server.URL
			options.APIKey = "test-api-key"
			stream, err := StreamSimple(t.Context(), model, userTranscript("hello"), options)
			if err != nil {
				t.Fatal(err)
			}
			return stream.Result()
		}},
		{"anthropic-messages", "API key", func(t *testing.T, options StreamOptions) *AssistantMessage {
			model := mustGeneratedModel(t, "anthropic", "claude-haiku-4-5").ToModel()
			result, _, _ := streamAnthropicFixture(t, AnthropicConfig{ModelMetadata: model, Model: model.ID, ProviderID: "anthropic"}, anthropicMinimalFixture(), options)
			return result
		}},
		{"anthropic-messages", "OAuth bearer (sk-ant-oat)", func(t *testing.T, options StreamOptions) *AssistantMessage {
			model := mustGeneratedModel(t, "anthropic", "claude-haiku-4-5").ToModel()
			result, _, _ := streamAnthropicFixture(t, AnthropicConfig{ModelMetadata: model, Model: model.ID, ProviderID: "anthropic", APIKey: "sk-ant-oat01-test"}, anthropicMinimalFixture(), options)
			return result
		}},
		{"google-generative-ai", "API key, custom base URL", func(t *testing.T, options StreamOptions) *AssistantMessage {
			server := serveSSE(t, sseFrames(googleFirst, googleSecond))
			want := mustGeneratedModel(t, "google", "gemini-2.5-flash").ToModel()
			provider := NewGoogleProvider(GoogleConfig{ModelMetadata: want, Model: want.ID, ProviderID: want.ProviderMeta.ProviderID, APIKey: "test-api-key", BaseURL: server.URL})
			stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
			if err != nil {
				t.Fatal(err)
			}
			return stream.Result()
		}},
	}
	// The Google Vertex and Mistral cases come from provider_stream_event_rejection_google_vertex_test.go and provider_stream_event_rejection_mistral_test.go: a Piglet built with
	// pig_strip_google_vertex or pig_strip_mistral_conversations has no such provider or none of its test helpers, and this file is built for every Piglet.
	cases = slices.Concat(cases, providerStreamEventRejectionGoogleVertexCases(googleFirst, googleSecond), providerStreamEventRejectionMistralCases(mistralFirst, mistralSecond), []providerStreamEventRejectionCase{
		{"pi-messages", "API key, custom base URL", func(t *testing.T, options StreamOptions) *AssistantMessage {
			baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: []any{
				map[string]any{"type": "start"},
				map[string]any{"type": "text_start", "contentIndex": 0},
			}})
			stream, err := newTestPiMessagesProvider(baseURL, nil).Stream(t.Context(), piMessagesTestContext(), options)
			if err != nil {
				t.Fatal(err)
			}
			return stream.Result()
		}},
	})
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.shape, func(t *testing.T) {
			var calls atomic.Int32
			message := tc.run(t, StreamOptions{OnProviderStreamEvent: func(_ context.Context, _ any, _ *Model) error {
				calls.Add(1)
				return errors.New(reason)
			}})
			if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, reason) {
				t.Fatalf("stop reason %q, error %q; the observer's rejection ends the stream as an error", message.StopReason, message.ErrorMessage)
			}
			if len(message.Content) != 0 {
				t.Fatalf("content %#v: the rejected event was normalized", message.Content)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("the observer ran %d times; no event after the rejected one reaches it", got)
			}
		})
	}
}
