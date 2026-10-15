//go:build live

package ai

import "testing"

// newAnthropicTestProvider is the Anthropic provider of a live end-to-end test; it lives under the live tag with its callers.
func newAnthropicTestProvider(t *testing.T, model *GeneratedModel, key string) Provider {
	t.Helper()
	provider := NewAnthropicProvider(AnthropicConfig{Model: model.ID, ProviderID: model.Provider, BaseURL: model.BaseURL, APIKey: key, Compat: model.Compat, ExtraHeaders: model.Headers, UseBearerAuth: model.Provider == "github-copilot"})
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	return provider
}
