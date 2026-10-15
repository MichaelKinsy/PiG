package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi types.ts:1930 ProviderConfig.refreshModels and provider-composer.ts:613-640: a registered provider's refresh callback runs when the provider refreshes, and the models it returns replace the extension-provided models.
func TestRegisterProviderConfigRefreshModelsReplacesTheModels(t *testing.T) {
	_, services := newAvailabilitySession(t)
	runtime := services.ModelRuntime()
	const id = "refresh-config"
	var contexts []ai.RefreshModelsContext
	config := extension.ProviderConfig{
		BaseURL: "https://example.invalid/v1", APIKey: "configured", API: ai.APIOpenAICompletions,
		Models: []extension.ProviderModelConfig{{ID: "old", Name: "old", Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100}},
		RefreshModels: func(context extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error) {
			contexts = append(contexts, context)
			return []extension.ProviderModelConfig{{ID: "refreshed", Name: "refreshed", Input: []string{"text"}, ContextWindow: 2000, MaxTokens: 200}}, nil
		},
	}
	if err := services.Registry().RegisterExtensionProvider(id, config); err != nil {
		t.Fatal(err)
	}
	if runtime.GetModel(id, "old") == nil {
		t.Fatal("the registered model is available before a refresh")
	}
	result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{Providers: []string{id}, AllowNetwork: new(false)})
	if result.Aborted || len(result.Errors) != 0 || len(contexts) == 0 {
		t.Fatalf("refresh=%+v callback calls=%d", result, len(contexts))
	}
	if contexts[0].Signal == nil || contexts[0].Publish == nil {
		t.Fatalf("the callback receives the refresh context: %+v", contexts[0])
	}
	refreshed := runtime.GetModel(id, "refreshed")
	if refreshed == nil || refreshed.Capabilities.ContextWindow != 2000 {
		t.Fatalf("refreshed model = %+v", refreshed)
	}
	if runtime.GetModel(id, "old") != nil {
		t.Fatal("the refreshed list replaces the extension-provided models")
	}
}
