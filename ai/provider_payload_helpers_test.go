package ai

import (
	"strings"
	"testing"
)

func cloneGeneratedModel(t *testing.T, spec string) *GeneratedModel {
	t.Helper()
	provider, id, ok := strings.Cut(spec, "/")
	if !ok {
		t.Fatalf("expected provider/model, got %q", spec)
	}
	model := *mustGeneratedModel(t, provider, id)
	model.Compat = cloneCompat(model.Compat)
	return &model
}

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
