package ai

import "testing"

// Pi 177e2ad7 (#11434, scripts/generate-models.ts): OpenCode and OpenCode Go models use the documented protocol-specific endpoints, not the shared base:
// openai-completions and openai-responses at <base>/openai/v1, anthropic-messages at <base>/anthropic (the SDK appends /v1/messages),
// google-generative-ai at <base>/google/v1beta, and the Jev classifier models at <base>/systemone/v1. OpenCode Go uses the same paths under /inference/go.
func TestOpenCodeCatalogUsesProtocolSpecificEndpoints(t *testing.T) {
	endpoints := map[string]string{
		"openai-completions":   "/openai/v1",
		"openai-responses":     "/openai/v1",
		"anthropic-messages":   "/anthropic",
		"google-generative-ai": "/google/v1beta",
		"typesafe-system-one":  "/systemone/v1",
	}
	for provider, base := range map[string]string{"opencode": "https://opencode.ai/inference", "opencode-go": "https://opencode.ai/inference/go"} {
		t.Run(provider, func(t *testing.T) {
			seen := map[string]int{}
			for _, model := range GetBuiltinModels(provider) {
				api := string(model.ProviderMeta.API)
				suffix, ok := endpoints[api]
				if !ok {
					t.Errorf("%s/%s has api %q, which has no OpenCode endpoint", provider, model.ID, api)
					continue
				}
				seen[api]++
				if want := base + suffix; model.ProviderMeta.BaseURL != want {
					t.Errorf("%s/%s (%s) baseUrl = %q, want %q", provider, model.ID, api, model.ProviderMeta.BaseURL, want)
				}
			}
			for _, classifier := range GetBuiltinClassifierModels(provider) {
				api := string(classifier.API)
				suffix, ok := endpoints[api]
				if !ok {
					t.Errorf("%s/%s has classifier api %q, which has no OpenCode endpoint", provider, classifier.ID, api)
					continue
				}
				seen[api]++
				if want := base + suffix; classifier.BaseURL != want {
					t.Errorf("%s/%s (%s) baseUrl = %q, want %q", provider, classifier.ID, api, classifier.BaseURL, want)
				}
			}
			for _, api := range []string{"openai-completions", "openai-responses", "anthropic-messages"} {
				if seen[api] == 0 {
					t.Errorf("%s has no %s model", provider, api)
				}
			}
		})
	}
}
