package coding

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestModelRegistryRequestAuthUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:2019
	t.Run("getApiKeyAndHeaders resolves authHeader on every request", func(t *testing.T) {
		dir := t.TempDir()
		token := filepath.Join(dir, "token")
		if err := os.WriteFile(token, []byte("token-1"), 0o600); err != nil {
			t.Fatal(err)
		}
		config := registryProviderWithKey(`!sh -c 'cat "` + token + `"'`)
		config["authHeader"] = true
		registry := registryTestServices(t, dir, map[string]any{"custom-provider": config}).Registry()
		model := registry.Find("custom-provider", "test-model")
		for _, key := range []string{"token-1", "token-2"} {
			if err := os.WriteFile(token, []byte(key), 0o600); err != nil {
				t.Fatal(err)
			}
			got := registry.GetAPIKeyAndHeaders(t.Context(), model)
			want := ResolvedRequestAuth{OK: true, APIKey: new(key), Headers: ai.ProviderHeaders{"Authorization": new("Bearer " + key)}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("auth=%+v, want %+v", got, want)
			}
		}
	})
	for _, tc := range []struct {
		name               string
		stored             bool
		wantKey, wantCount string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:2052
		{name: "getApiKeyAndHeaders resolves configured auth exactly once", wantKey: "token-1", wantCount: "1"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:2076
		{name: "stored credentials bypass lower-priority configured auth commands", stored: true, wantKey: "stored-key", wantCount: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			counter := filepath.Join(dir, "auth-counter")
			if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
				t.Fatal(err)
			}
			command := `!sh -c 'count=$(cat "` + counter + `"); count=$((count + 1)); echo "$count" > "` + counter + `"; echo "token-$count"'`
			if tc.stored {
				command = `!sh -c 'echo 1 > "` + counter + `"; echo fallback-key'`
			}
			config := registryProviderWithKey(command)
			if !tc.stored {
				config["authHeader"] = true
			}
			services := registryTestServices(t, dir, map[string]any{"custom-provider": config})
			if tc.stored {
				if err := services.Auth().Set("custom-provider", ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"}); err != nil {
					t.Fatal(err)
				}
			}
			registry := services.Registry()
			auth := registry.GetAPIKeyAndHeaders(t.Context(), registry.Find("custom-provider", "test-model"))
			if !auth.OK || auth.APIKey == nil || *auth.APIKey != tc.wantKey {
				t.Fatalf("auth=%+v", auth)
			}
			if !tc.stored && !reflect.DeepEqual(auth, ResolvedRequestAuth{OK: true, APIKey: new("token-1"), Headers: ai.ProviderHeaders{"Authorization": new("Bearer token-1")}}) {
				t.Fatalf("auth=%+v", auth)
			}
			data, err := os.ReadFile(counter)
			if err != nil || strings.TrimSpace(string(data)) != tc.wantCount {
				t.Fatalf("counter=%q err=%v", data, err)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:2092
	t.Run("getApiKeyAndHeaders preserves the legacy missing-key authHeader error", func(t *testing.T) {
		config := map[string]any{"baseUrl": "https://example.test/v1", "api": "openai-completions", "authHeader": true, "models": []any{map[string]any{"id": "test-model"}}}
		registry := registryTestServices(t, "", map[string]any{"custom-provider": config}).Registry()
		got := registry.GetAPIKeyAndHeaders(t.Context(), registry.Find("custom-provider", "test-model"))
		if !reflect.DeepEqual(got, ResolvedRequestAuth{Error: `No API key found for "custom-provider"`}) {
			t.Fatalf("auth=%+v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:2108
	t.Run("getApiKeyAndHeaders returns an error for failed authHeader resolution", func(t *testing.T) {
		config := registryProviderWithKey("!exit 1")
		config["authHeader"] = true
		registry := registryTestServices(t, "", map[string]any{"custom-provider": config}).Registry()
		auth := registry.GetAPIKeyAndHeaders(t.Context(), registry.Find("custom-provider", "test-model"))
		if auth.OK || !strings.Contains(auth.Error, `Failed to resolve API key for provider "custom-provider"`) {
			t.Fatalf("auth=%+v", auth)
		}
	})
}
