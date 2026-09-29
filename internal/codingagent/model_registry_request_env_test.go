package codingagent

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi model-runtime.ts:615-631 carries credential-scoped env into request options for every registry-backed provider, not only custom model definitions.
func TestGeneratedModelRequestCarriesCredentialEnvironment(t *testing.T) {
	for _, provider := range []string{"openai", "groq", "cloudflare-ai-gateway", "custom-no-default", "openai-codex"} {
		t.Run(provider, func(t *testing.T) {
			dir := t.TempDir()
			store, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"REQUEST_SETTING": "credential-value"}
			credential := ai.Credential{Type: ai.CredentialAPIKey, Key: "key", Env: env}
			if provider == "openai-codex" {
				credential = ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh"}
				env = nil // OAuth credentials do not have the API-key env field.
			}
			if err := store.Set(provider, credential); err != nil {
				t.Fatal(err)
			}
			registry := NewModelRegistry(dir)
			registry.SetAuthStorage(store)
			model := &ai.GeneratedModel{ID: "selected", Provider: provider, API: ai.APIOpenAICompletions, BaseURL: "https://custom.invalid/v1"}
			entry := registry.ResolveGeneratedModel(provider, model.ID, model)
			if !reflect.DeepEqual(entry.Env, env) {
				t.Fatalf("request env=%v, want %v", entry.Env, env)
			}
			if env != nil {
				entry.Env["REQUEST_SETTING"] = "modified"
				next := registry.ResolveGeneratedModel(provider, model.ID, model)
				if !reflect.DeepEqual(next.Env, env) {
					t.Fatalf("request mutated stored env=%v", next.Env)
				}
			}
		})
	}
}
