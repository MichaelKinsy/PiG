package ai

import (
	"context"
	"errors"
	"testing"
)

// A base URL resolver that throws (azure-openai-config.ts:89 "Azure OpenAI base URL is required…") rejects the stream with
// that error itself; Pi shows its message unchanged, with no provider or "resolve base URL" prefix.
func TestBaseURLResolverErrorIsTheMessageLikePi(t *testing.T) {
	const message = "Azure OpenAI base URL is required. Set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME, or pass azureBaseUrl, azureResourceName, or model.baseUrl."
	resolve := func(context.Context) (string, error) { return "", errors.New(message) }
	for name, provider := range map[string]Provider{
		"openai-completions": NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", ProviderID: "azure", GetBaseURL: resolve}),
		"openai-responses":   NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: "m", ProviderID: "azure", GetBaseURL: resolve}),
	} {
		t.Run(name, func(t *testing.T) {
			if result := transportErrorTestResult(t, provider); result.ErrorMessage != message {
				t.Fatalf("error = %q, want %q", result.ErrorMessage, message)
			}
		})
	}
}
