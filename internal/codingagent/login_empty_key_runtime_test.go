package codingagent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The shared Model Runtime auth preflight must preserve the provider's empty-key semantics after the credential-store read, not silently borrow an ambient cloud key.
func TestLoginEmptyKeyRuntimeAuthPreflight(t *testing.T) {
	for _, tc := range []struct {
		provider, variable string
		ready              bool
	}{
		{"cloudflare-workers-ai", "CLOUDFLARE_API_KEY", false},
		{"cloudflare-ai-gateway", "CLOUDFLARE_API_KEY", false},
		{"google-vertex", "GOOGLE_CLOUD_API_KEY", false},
		{"openai", "OPENAI_API_KEY", true},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(tc.variable, "ambient-fixture-key")
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "missing-credentials.json"))
			t.Setenv("GOOGLE_CLOUD_PROJECT", "")
			t.Setenv("GCLOUD_PROJECT", "")
			t.Setenv("GOOGLE_CLOUD_LOCATION", "")
			store, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			var credential ai.Credential
			if err := json.Unmarshal([]byte(`{"type":"api_key","key":"","env":{"CLOUDFLARE_ACCOUNT_ID":"account","CLOUDFLARE_GATEWAY_ID":"gateway"}}`), &credential); err != nil {
				t.Fatal(err)
			}
			if err := store.Set(tc.provider, credential); err != nil {
				t.Fatal(err)
			}
			registry := NewModelRegistry(dir)
			registry.SetAuthStorage(store)
			check, err := registry.CheckRegistryAuth(t.Context(), tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			if (check != nil) != tc.ready {
				t.Fatalf("auth check=%+v, ready=%v", check, tc.ready)
			}
		})
	}
}
