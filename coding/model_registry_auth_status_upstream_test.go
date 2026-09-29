package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestModelRegistryCopilotAvailabilityUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1997
	t.Run("getAvailable filters GitHub Copilot OAuth models to account picker availability", func(t *testing.T) {
		models := ai.ListModels("github-copilot")
		if len(models) == 0 {
			t.Fatal("expected GitHub Copilot models")
		}
		ids, err := json.Marshal([]string{models[0].ID})
		if err != nil {
			t.Fatal(err)
		}
		services := registryTestServices(t, "", nil)
		if err := services.Auth().Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Refresh: "github-access-token", Access: "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;", Expires: time.Now().Add(time.Minute).UnixMilli(), AvailableModelIDs: ids}); err != nil {
			t.Fatal(err)
		}
		var actual []string
		for _, model := range services.Registry().GetAvailable() {
			if model.ProviderID == "github-copilot" {
				actual = append(actual, model.ModelID)
			}
		}
		if !reflect.DeepEqual(actual, []string{models[0].ID}) {
			t.Fatalf("available=%v", actual)
		}
	})
}

func TestModelRegistryAuthStatusUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		env       map[string]string
		want      ai.AuthStatus
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1834
		{"provider auth status reports apiKey environment variables from models.json", "$TEST_API_KEY_STATUS_TEST_98765", map[string]string{"TEST_API_KEY_STATUS_TEST_98765": "status-test-key"}, ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: "TEST_API_KEY_STATUS_TEST_98765"}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1861
		{"provider auth status reports interpolated apiKey environment variables", "${TEST_API_KEY_STATUS_PART_A_98765}_${TEST_API_KEY_STATUS_PART_B_98765}", map[string]string{"TEST_API_KEY_STATUS_PART_A_98765": "left", "TEST_API_KEY_STATUS_PART_B_98765": "right"}, ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: "TEST_API_KEY_STATUS_PART_A_98765, TEST_API_KEY_STATUS_PART_B_98765"}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1896
		{"provider auth status reports non-env apiKey values from models.json as a config key", "literal_api_key_value", nil, ai.AuthStatus{Configured: true, Source: ai.AuthSourceModelsJSONKey}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1909
		{"missing explicit env apiKey keeps provider unavailable", "$TEST_API_KEY_MISSING_TEST_98765", map[string]string{"TEST_API_KEY_MISSING_TEST_98765": ""}, ai.AuthStatus{Configured: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
				if value == "" {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			registry := registryTestServices(t, "", map[string]any{"custom-provider": registryProviderWithKey(tc.key)}).Registry()
			if got := registry.GetProviderAuthStatus("custom-provider"); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("status=%+v, want %+v", got, tc.want)
			}
			if !tc.want.Configured {
				for _, model := range registry.GetAvailable() {
					if model.ProviderID == "custom-provider" {
						t.Fatal("unconfigured provider was available")
					}
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1932
	t.Run("provider auth status reports command apiKey values from models.json without executing them", func(t *testing.T) {
		dir := t.TempDir()
		counter := filepath.Join(dir, "status-counter")
		if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
		key := `!sh -c 'echo 1 > "` + counter + `"; echo key-value'`
		registry := registryTestServices(t, dir, map[string]any{"custom-provider": registryProviderWithKey(key)}).Registry()
		if got := registry.GetProviderAuthStatus("custom-provider"); got != (ai.AuthStatus{Configured: true, Source: ai.AuthSourceModelsJSONCommand}) {
			t.Errorf("status=%+v", got)
		}
		if value, err := os.ReadFile(counter); err != nil || string(value) != "0" {
			t.Fatalf("counter=%q err=%v", value, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1950
	t.Run("environment variables are not cached (changes are picked up)", func(t *testing.T) {
		t.Setenv("TEST_API_KEY_CACHE_TEST_98765", "first-value")
		registry := registryTestServices(t, "", map[string]any{"custom-provider": registryProviderWithKey("$TEST_API_KEY_CACHE_TEST_98765")}).Registry()
		if got := registry.GetAPIKeyForProvider(t.Context(), "custom-provider"); got == nil || *got != "first-value" {
			t.Fatalf("first=%v", got)
		}
		t.Setenv("TEST_API_KEY_CACHE_TEST_98765", "second-value")
		if got := registry.GetAPIKeyForProvider(t.Context(), "custom-provider"); got == nil || *got != "second-value" {
			t.Fatalf("second=%v", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1979
	t.Run("getAvailable does not execute command-backed apiKey resolution", func(t *testing.T) {
		dir := t.TempDir()
		counter := filepath.Join(dir, "counter")
		if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
		key := `!sh -c 'count=$(cat "` + counter + `"); echo $((count + 1)) > "` + counter + `"; echo "key-value"'`
		registry := registryTestServices(t, dir, map[string]any{"custom-provider": registryProviderWithKey(key)}).Registry()
		found := false
		for _, model := range registry.GetAvailable() {
			if model.ProviderID == "custom-provider" {
				found = true
			}
		}
		if !found {
			t.Fatal("command-configured provider missing")
		}
		if value, err := os.ReadFile(counter); err != nil || strings.TrimSpace(string(value)) != "0" {
			t.Fatalf("counter=%q err=%v", value, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1798
	t.Run("different commands resolve independently", func(t *testing.T) {
		registry := registryTestServices(t, "", map[string]any{"provider-a": registryProviderWithKey("!echo key-a"), "provider-b": registryProviderWithKey("!echo key-b")}).Registry()
		a, b := registry.GetAPIKeyForProvider(t.Context(), "provider-a"), registry.GetAPIKeyForProvider(t.Context(), "provider-b")
		if a == nil || b == nil || *a != "key-a" || *b != "key-b" {
			t.Fatalf("keys=%v,%v", a, b)
		}
	})
}
