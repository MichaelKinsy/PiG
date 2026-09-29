package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestExtensionRegistrationValidationBeforeReplacement(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	original := extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://provider.test/v1", APIKey: "provider-test-key", Models: []extension.ProviderModelConfig{{ID: "instant-model", Name: "Instant Model"}}}
	if err := registry.RegisterProvider("instant-provider", original); err != nil {
		t.Fatal(err)
	}
	invalid := extension.ProviderConfig{Models: []extension.ProviderModelConfig{{ID: "invalid-model", Name: "Invalid"}}}
	if err := registry.RegisterProvider("instant-provider", invalid); err == nil || !strings.Contains(err.Error(), `no "api" specified`) {
		t.Fatalf("validation error=%v", err)
	}
	entry, ok := registry.Resolve("instant-provider", "instant-model")
	if !ok || entry.ModelID != "instant-model" || entry.API != string(ai.APIOpenAICompletions) {
		t.Fatalf("old registration replaced: %+v", entry)
	}
}
