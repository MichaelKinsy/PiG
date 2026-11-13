package ai

import (
	"context"
	"errors"
	"testing"
)

// .upstream/v0.87.1/packages/ai/test/pre-generation-error.test.ts:36 — all seven direct adapters reject missing auth before stream creation.
func TestDirectAPIAuthenticationThrowsSynchronouslyUpstream(t *testing.T) {
	for _, api := range []API{APIAnthropicMessages, APIAzureOpenAIResponses, APIGoogleGenerativeAI, APIMistralConversations, APIOpenAICodexResponses, APIOpenAICompletions, APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			model := &Model{ID: "test-model", DisplayName: "Test", ProviderMeta: ProviderMetadata{API: api, ProviderID: "test-provider", BaseURL: "https://example.invalid"}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
			called := false
			stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{}), StreamOptions{OnPayload: func(any, *Model) (any, error) { called = true; return nil, errors.New("payload reached") }})
			if stream != nil {
				for range stream.Events(context.Background()) {
				}
			}
			want := "No API key for provider: test-provider"
			if !BuiltAPI(api) {
				// A build with this API's pig_strip tag reports the strip before it looks for a key (D92).
				want = strippedAPIMessage("test-provider", api)
			}
			if err == nil || err.Error() != want || stream != nil || called {
				t.Fatalf("stream=%v error=%v payload=%v", stream != nil, err, called)
			}
		})
	}
}
