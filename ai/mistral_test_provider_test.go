//go:build !pig_strip_mistral_conversations

package ai

import "net/http"

// newMistralTestProvider builds the Mistral provider for multi-provider tests that run in builds with and without Mistral. A non-nil client replaces the provider's HTTP client.
func newMistralTestProvider(cfg MistralConfig, client *http.Client) Provider {
	provider := NewMistralProvider(cfg).(*mistralProvider)
	if client != nil {
		provider.client = client
	}
	return provider
}
