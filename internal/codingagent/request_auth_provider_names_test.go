package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream packages/coding-agent/test/model-runtime-auth-options.test.ts:73 projects provider-owned names and auth methods, not labels derived from provider IDs.
func TestRuntimeProjectsProviderOwnedMethodsUpstream(t *testing.T) {
	r, err := NewRequestAuthRuntime(t.Context(), RequestAuthRuntimeOptions{Credentials: ai.NewInMemoryAuthStorage(nil)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, name, method string
		oauth            bool
	}{{"amazon-bedrock", "Amazon Bedrock", "AWS credentials or bearer token", false}, {"google-vertex", "Google Vertex AI", "Google Cloud credentials", false}, {"anthropic", "Anthropic", "", true}, {"cloudflare-ai-gateway", "Cloudflare AI Gateway", "", false}, {"cloudflare-workers-ai", "Cloudflare Workers AI", "", false}} {
		provider := r.GetProvider(tc.id)
		if provider == nil {
			t.Fatalf("missing provider %q", tc.id)
		}
		if provider.Name != tc.name {
			t.Fatalf("provider %q name = %q, want %q", tc.id, provider.Name, tc.name)
		}
		if tc.oauth {
			if provider.Auth.OAuth == nil {
				t.Fatal("missing OAuth")
			}
		} else if provider.Auth.APIKey == nil || tc.method != "" && provider.Auth.APIKey.Name != tc.method {
			t.Fatalf("auth=%#v", provider.Auth)
		}
	}
	if r.GetProvider("openai-codex").Auth.APIKey != nil {
		t.Fatal("fabricated Codex API-key method")
	}
	for _, kind := range []string{"api_key", "oauth"} {
		for _, p := range r.GetProviders() {
			if kind == "api_key" && p.Auth.APIKey != nil && p.Auth.APIKey.Name == "" {
				t.Fatal("unnamed API key method")
			}
			if kind == "oauth" && p.Auth.OAuth != nil && p.Auth.OAuth.Name == "" {
				t.Fatal("unnamed OAuth method")
			}
		}
	}
}

// model-runtime.ts:254-267 keeps the base provider on composition failure; provider-composer.ts:517 gives models.json names precedence over the base name and falls back to the provider ID.
func TestRequestAuthProviderNamesFollowComposition(t *testing.T) {
	for _, tc := range []struct {
		name, config, id, want string
		compositionError       bool
	}{
		{name: "builtin without overlays", config: `{}`, id: "anthropic", want: "Anthropic"},
		{name: "builtin overlay retains name", config: `{"anthropic":{"apiKey":"test-key"}}`, id: "anthropic", want: "Anthropic"},
		{name: "builtin configured name", config: `{"anthropic":{"name":"Configured Anthropic","apiKey":"test-key"}}`, id: "anthropic", want: "Configured Anthropic"},
		{name: "custom configured name", config: `{"custom-api":{"name":"Private Service","baseUrl":"https://example.invalid/v1","apiKey":"test-key"}}`, id: "custom-api", want: "Private Service"},
		{name: "custom unnamed provider", config: `{"custom-api":{"baseUrl":"https://example.invalid/v1","apiKey":"test-key"}}`, id: "custom-api", want: "custom-api"},
		{name: "radius builtin", config: `{}`, id: "radius", want: "Radius"},
		{name: "radius named gateway", config: `{"radius-dev":{"name":"Dev Gateway","baseUrl":"https://gateway.invalid/v1","oauth":"radius"}}`, id: "radius-dev", want: "Dev Gateway"},
		{name: "radius unnamed gateway", config: `{"radius-dev":{"baseUrl":"https://gateway.invalid/v1","oauth":"radius"}}`, id: "radius-dev", want: "radius-dev"},
		{name: "composition failure retains base name", config: `{"anthropic":{"name":"Invalid Overlay","oauth":"radius"}}`, id: "anthropic", want: "Anthropic", compositionError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":`+tc.config+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			runtime, err := NewRequestAuthRuntime(t.Context(), RequestAuthRuntimeOptions{Credentials: ai.NewInMemoryAuthStorage(nil), AgentDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if got := runtime.GetError(); (got != "") != tc.compositionError {
				t.Fatalf("composition error = %q, want error = %v", got, tc.compositionError)
			}
			provider := runtime.GetProvider(tc.id)
			if provider == nil {
				t.Fatalf("missing provider %q", tc.id)
			}
			if provider.Name != tc.want {
				t.Fatalf("provider %q name = %q, want %q", tc.id, provider.Name, tc.want)
			}
			found := false
			for _, listed := range runtime.GetProviders() {
				if listed.ID == tc.id {
					found = true
					if listed != provider {
						t.Fatal("provider lookup and enumeration do not retain the same projection")
					}
				}
			}
			if !found {
				t.Fatalf("provider %q missing from enumeration", tc.id)
			}
		})
	}
}

func BenchmarkRequestAuthProviderProjection(b *testing.B) {
	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{
		"anthropic": {Type: ai.CredentialAPIKey, Key: "benchmark-key"},
	})
	for b.Loop() {
		runtime, err := NewRequestAuthRuntime(b.Context(), RequestAuthRuntimeOptions{Credentials: credentials})
		if err != nil {
			b.Fatal(err)
		}
		provider := runtime.GetProvider("anthropic")
		if provider == nil || provider.Name != "Anthropic" {
			b.Fatal("missing provider identity")
		}
		check, err := runtime.CheckAuth(b.Context(), provider.ID)
		if err != nil || check == nil || check.Type != ai.CredentialAPIKey {
			b.Fatalf("auth = %v, %v", check, err)
		}
	}
}
