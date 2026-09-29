package codingagent_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortWave11ConfigValueMigration(t *testing.T) {
	// upstream: packages/coding-agent/test/config-value-migration.test.ts:41
	t.Run("leaves uppercase auth.json API key values unchanged", func(t *testing.T) {
		agentDir := configMigrationAgentDir(t)
		authPath := filepath.Join(agentDir, "auth.json")
		writeConfigMigrationFile(t, authPath, `{
  "anthropic": {
    "type": "api_key",
    "key": "ANTHROPIC_API_KEY"
  },
  "openai": {
    "type": "api_key",
    "key": "$OPENAI_API_KEY"
  },
  "opencode": {
    "type": "api_key",
    "key": "public"
  },
  "github": {
    "type": "oauth",
    "access": "ACCESS_TOKEN",
    "refresh": "REFRESH_TOKEN",
    "expires": 1
  }
}`+"\n")

		output := runConfigMigrationsCaptured(t, agentDir)
		var migrated map[string]map[string]any
		if err := json.Unmarshal(readConfigMigrationFile(t, authPath), &migrated); err != nil {
			t.Fatal(err)
		}
		for _, assertion := range []struct {
			provider, field, want string
		}{
			{"anthropic", "key", "ANTHROPIC_API_KEY"},
			{"openai", "key", "$OPENAI_API_KEY"},
			{"opencode", "key", "public"},
			{"github", "access", "ACCESS_TOKEN"},
		} {
			if got := migrated[assertion.provider][assertion.field]; got != assertion.want {
				t.Fatalf("%s.%s = %v, want %q", assertion.provider, assertion.field, got, assertion.want)
			}
		}
		if output != "" {
			t.Fatalf("migration logged %q, want no console output", output)
		}
	})

	// upstream: packages/coding-agent/test/config-value-migration.test.ts:72-75 (both table rows)
	for _, tc := range []struct {
		name, content string
	}{
		{"malformed", "{\n  \"providers\": {\n"},
		{"blank", ""},
	} {
		t.Run("does not throw on "+tc.name+" models.json during migrations", func(t *testing.T) {
			agentDir := configMigrationAgentDir(t)
			modelsPath := filepath.Join(agentDir, "models.json")
			writeConfigMigrationFile(t, modelsPath, tc.content)

			runConfigMigrationsCaptured(t, agentDir)
			if got := string(readConfigMigrationFile(t, modelsPath)); got != tc.content {
				t.Fatalf("models.json = %q, want unchanged %q", got, tc.content)
			}
			registry := newMigrationModelRegistry(t, agentDir)
			loadError := registry.GetError()
			if !strings.Contains(loadError, "Failed to parse models.json") {
				t.Fatalf("getError = %q, want Failed to parse models.json", loadError)
			}
			if !strings.Contains(loadError, "File: "+modelsPath) {
				t.Fatalf("getError = %q, want File: %s", loadError, modelsPath)
			}
		})
	}

	// upstream: packages/coding-agent/test/config-value-migration.test.ts:89
	t.Run("leaves uppercase models.json API key and header values unchanged", func(t *testing.T) {
		agentDir := configMigrationAgentDir(t)
		for _, key := range []string{"CUSTOM_API_KEY", "HEADER_API_KEY", "MODEL_API_KEY", "OVERRIDE_API_KEY"} {
			t.Setenv(key, "env-"+key)
		}
		modelsPath := filepath.Join(agentDir, "models.json")
		writeConfigMigrationFile(t, modelsPath, `{
  "providers": {
    "custom-provider": {
      "baseUrl": "https://example.com/v1",
      "apiKey": "CUSTOM_API_KEY",
      "api": "openai-completions",
      "headers": {
        "x-api-key": "HEADER_API_KEY",
        "x-literal": "literal"
      },
      "models": [
        {
          "id": "model-a",
          "headers": {
            "x-model-key": "MODEL_API_KEY"
          }
        }
      ],
      "modelOverrides": {
        "model-b": {
          "headers": {
            "x-override-key": "OVERRIDE_API_KEY"
          }
        }
      }
    }
  }
}`+"\n")

		output := runConfigMigrationsCaptured(t, agentDir)
		var migrated struct {
			Providers map[string]struct {
				APIKey  string            `json:"apiKey"`
				Headers map[string]string `json:"headers"`
				Models  []struct {
					Headers map[string]string `json:"headers"`
				} `json:"models"`
				ModelOverrides map[string]struct {
					Headers map[string]string `json:"headers"`
				} `json:"modelOverrides"`
			} `json:"providers"`
		}
		if err := json.Unmarshal(readConfigMigrationFile(t, modelsPath), &migrated); err != nil {
			t.Fatal(err)
		}
		provider := migrated.Providers["custom-provider"]
		if provider.APIKey != "CUSTOM_API_KEY" {
			t.Fatalf("provider.apiKey = %q, want CUSTOM_API_KEY", provider.APIKey)
		}
		if provider.Headers["x-api-key"] != "HEADER_API_KEY" {
			t.Fatalf("provider x-api-key = %q, want HEADER_API_KEY", provider.Headers["x-api-key"])
		}
		if provider.Headers["x-literal"] != "literal" {
			t.Fatalf("provider x-literal = %q, want literal", provider.Headers["x-literal"])
		}
		if len(provider.Models) == 0 || provider.Models[0].Headers["x-model-key"] != "MODEL_API_KEY" {
			t.Fatalf("provider.models = %+v, want models[0] x-model-key=MODEL_API_KEY", provider.Models)
		}
		if got := provider.ModelOverrides["model-b"].Headers["x-override-key"]; got != "OVERRIDE_API_KEY" {
			t.Fatalf("model-b x-override-key = %q, want OVERRIDE_API_KEY", got)
		}
		if output != "" {
			t.Fatalf("migration logged %q, want no console output", output)
		}

		registry := newMigrationModelRegistry(t, agentDir)
		model := registry.Find("custom-provider", "model-a")
		if model == nil {
			t.Fatal("custom-provider/model-a must be defined")
		}
		if key := registry.GetAPIKeyForProvider(t.Context(), "custom-provider"); key == nil || *key != "CUSTOM_API_KEY" {
			t.Fatalf("getApiKeyForProvider = %v, want CUSTOM_API_KEY", key)
		}
		resolved := registry.GetAPIKeyAndHeaders(t.Context(), model)
		if !resolved.OK {
			t.Fatalf("getApiKeyAndHeaders = %+v, want ok=true", resolved)
		}
		if resolved.APIKey == nil || *resolved.APIKey != "CUSTOM_API_KEY" {
			t.Fatalf("getApiKeyAndHeaders.apiKey = %v, want CUSTOM_API_KEY", resolved.APIKey)
		}
		for name, want := range map[string]string{
			"x-api-key":   "HEADER_API_KEY",
			"x-literal":   "literal",
			"x-model-key": "MODEL_API_KEY",
		} {
			if got := resolved.Headers[name]; got == nil || *got != want {
				t.Fatalf("getApiKeyAndHeaders.headers[%q] = %v, want %q", name, got, want)
			}
		}
	})
}
