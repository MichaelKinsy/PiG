package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/model-runtime.ts:882-897 (registerNativeProvider) and :300-304 (composeProvider): a registered Provider object is the
// composition base, and with no models.json entry and no extension configuration it is used untouched, so its models keep the base URL they have, or none.
// A baseUrl is required only of the models an extension's registerProvider configuration defines (provider-composer.ts applyExtension).
func TestNativeProviderObjectWithoutBaseURLComposesUntouched(t *testing.T) {
	for _, tc := range []struct {
		name, providerBaseURL string
	}{
		{"no base URL anywhere", ""},
		{"provider base URL is not copied to the models", "https://provider.invalid/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewModelRegistry(t.TempDir())
			provider := &extension.NativeProvider{
				ID: "bare-native", Name: "Bare Native", BaseURL: tc.providerBaseURL,
				Models: []extension.ProviderModelConfig{{ID: "n1", Name: "N One", API: ai.APIOpenAICompletions}},
				CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
					return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
				},
				ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
					return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}}, nil, nil
				},
				ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
				Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					return ai.NewAssistantMessageEventStream(), nil
				},
			}
			if err := registry.RegisterNativeProvider(t.Context(), provider); err != nil {
				t.Fatal(err)
			}
			models := registry.GetProviderModelData("bare-native")
			if len(models) != 1 || models[0].ID != "n1" {
				t.Fatalf("GetProviderModelData = %v, want the provider's model n1", models)
			}
			if got := models[0].ProviderMeta.BaseURL; got != "" {
				t.Errorf("model base URL = %q, want none: Pi composes the Provider object untouched", got)
			}
			typed := registry.GetTypedProvider("bare-native", nil)
			if typed == nil {
				t.Fatal("GetTypedProvider = nil, want the composed provider")
			}
			if typed.Name != "Bare Native" {
				t.Errorf("provider name = %q, want %q", typed.Name, "Bare Native")
			}
			if tc.providerBaseURL != "" && typed.BaseURL != tc.providerBaseURL {
				t.Errorf("provider base URL = %q, want %q (the refresh context's base URL)", typed.BaseURL, tc.providerBaseURL)
			}
		})
	}
}
