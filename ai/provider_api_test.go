package ai

// pi: packages/ai/src/compat.ts

import "testing"

// Pi uses a built-in provider's own stream only for an api that provider's catalog lists (compat.ts:236-239); the rule is the same for every provider.
func TestProviderCatalogServesAPI(t *testing.T) {
	for _, row := range []struct {
		provider string
		api      API
		want     bool
	}{
		{"github-copilot", APIAnthropicMessages, true},
		{"github-copilot", APIOpenAIResponses, true},
		{"github-copilot", APIOpenAICompletions, true},
		{"github-copilot", APIGoogleGenerativeAI, false},
		{"github-copilot", "", false},
		{"github-copilot", "nosuchapi", false},
		{"anthropic", APIAnthropicMessages, true},
		{"anthropic", APIOpenAICompletions, false},
		{"anthropic", "", false},
		{"google", APIGoogleGenerativeAI, true},
		{"google", APIOpenAICompletions, false},
		{"groq", APIOpenAICompletions, true},
		{"groq", APIOpenAIResponses, false},
		{"groq", "", false},
		{"nobody-lists-this-provider", APIOpenAICompletions, false},
	} {
		if got := ProviderCatalogServesAPI(row.provider, row.api); got != row.want {
			t.Errorf("ProviderCatalogServesAPI(%q, %q) = %v, want %v", row.provider, row.api, got, row.want)
		}
	}
}

// A provider registered with its own stream is used only for an api its catalog lists; a provider with no stream of its own never is.
func TestProviderHasOwnStreamFor(t *testing.T) {
	for _, row := range []struct {
		provider string
		api      API
		want     bool
	}{
		{"github-copilot", APIAnthropicMessages, true},
		{"github-copilot", APIOpenAICompletions, true},
		{"github-copilot", "", false},
		{"github-copilot", "nosuchapi", false},
		{"groq", APIOpenAICompletions, false},
		{"nobody-lists-this-provider", APIOpenAICompletions, false},
	} {
		if got := ProviderHasOwnStreamFor(row.provider, row.api); got != row.want {
			t.Errorf("ProviderHasOwnStreamFor(%q, %q) = %v, want %v", row.provider, row.api, got, row.want)
		}
	}
}
