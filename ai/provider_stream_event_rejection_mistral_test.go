//go:build !pig_strip_mistral_conversations

package ai

import (
	"net/http"
	"testing"
)

// providerStreamEventRejectionMistralCases is the Mistral case of TestProviderStreamEventObserverRejectionEndsTheStreamOnEveryAPI.
func providerStreamEventRejectionMistralCases(mistralFirst, mistralSecond string) []providerStreamEventRejectionCase {
	return []providerStreamEventRejectionCase{{"mistral-conversations", "API key", func(t *testing.T, options StreamOptions) *AssistantMessage {
		model := mustGeneratedModel(t, "mistral", "mistral-large-latest").ToModel()
		options.APIKey = "test"
		options.Fetch = &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
			return mistralUpstreamSSE(mistralCreateSSEBody(mistralFirst, mistralSecond)), nil
		})}
		stream, err := StreamSimple(t.Context(), model, mistralUpstreamContext(), options)
		if err != nil {
			t.Fatal(err)
		}
		return stream.Result()
	}}}
}
