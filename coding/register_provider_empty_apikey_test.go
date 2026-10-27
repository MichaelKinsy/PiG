package coding

import (
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// pig divergence (D203): Pi's registerProvider(id, {apiKey: ""}) keeps the empty string as a configured raw key (provider-composer.ts:397 `extension?.apiKey ?? config?.apiKey`), so the provider reads configured and its models available after the refresh settles, and every request then fails with "No API key". PiG reads an empty apiKey as no key: the provider stays unconfigured until a key, a credential or an environment variable supplies one. A non-empty key is configured in both.
func TestRegisterProviderWithEmptyAPIKeyIsNotConfigured(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newRegistrationServices(t)
		runtime := services.ModelRuntime()
		for _, tc := range []struct {
			id, key string
			want    bool
		}{
			{"empty-key", "", false},
			{"literal-key", "literal", true},
		} {
			if err := runtime.RegisterProvider(tc.id, ProviderConfigInput{API: ai.APIOpenAICompletions, APIKey: tc.key, BaseURL: "https://example.invalid/v1", Models: []ai.AnyModel{nativeCompatModel("model", tc.id, "https://example.invalid/v1")}}); err != nil {
				t.Fatal(err)
			}
			if result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
				t.Fatalf("refresh = %+v", result)
			}
			if got := runtime.HasConfiguredAuth(tc.id); got != tc.want {
				t.Errorf("apiKey %q: HasConfiguredAuth = %v, want %v", tc.key, got, tc.want)
			}
			if got := runtime.GetProviderAuthStatus(tc.id).Configured; got != tc.want {
				t.Errorf("apiKey %q: auth status configured = %v, want %v", tc.key, got, tc.want)
			}
		}
	})
}
