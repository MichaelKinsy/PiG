package coding

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/extensions/types.ts:1884-1888 (ProviderConfig.authHeader: "adds Authorization: Bearer <resolved apiKey>") through registerProvider (model-registry.ts), with the request resolution of model-registry.test.ts:2037 ("getApiKeyAndHeaders resolves authHeader on every request"): the key command runs on each request, and only a provider registered with authHeader sends the Authorization header.
func TestRegisterProviderAuthHeaderAddsBearerAuthorization(t *testing.T) {
	for _, row := range []struct {
		name       string
		authHeader bool
		wantHeader bool
	}{
		{"authHeader on", true, true},
		{"authHeader off", false, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			token := filepath.Join(dir, "token")
			registry := registryTestServices(t, dir, map[string]any{}).Registry()
			err := registry.RegisterExtensionProvider("ext-provider", extension.ProviderConfig{
				API: ai.APIOpenAICompletions, BaseURL: "https://example.test/v1", APIKey: `!sh -c 'cat "` + token + `"'`, AuthHeader: row.authHeader,
				Models: []extension.ProviderModelConfig{{ID: "test-model", Name: "Test", Input: []string{"text"}, ContextWindow: 128, MaxTokens: 16}},
			})
			if err != nil {
				t.Fatal(err)
			}
			model := registry.Find("ext-provider", "test-model")
			if model == nil {
				t.Fatal("registered model not found")
			}
			for _, key := range []string{"token-1", "token-2"} {
				if err := os.WriteFile(token, []byte(key), 0o600); err != nil {
					t.Fatal(err)
				}
				want := ResolvedRequestAuth{OK: true, APIKey: new(key)}
				if row.wantHeader {
					want.Headers = ai.ProviderHeaders{"Authorization": new("Bearer " + key)}
				}
				got := registry.GetAPIKeyAndHeaders(t.Context(), model)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("auth=%+v, want %+v", got, want)
				}
			}
		})
	}
}
