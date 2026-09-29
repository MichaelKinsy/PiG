package coding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5661-uppercase-header-values.test.ts:33
func TestStartupMigrationsKeepUppercaseHeaderStringsAsLiterals(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("CUSTOM_API_KEY", "env-CUSTOM_API_KEY")
	t.Setenv("BEARER", "env-BEARER")
	path := filepath.Join(agentDir, "models.json")
	data := []byte(`{"providers":{"my-provider":{"baseUrl":"https://example.com/v1","apiKey":"CUSTOM_API_KEY","api":"openai-completions","headers":{"Authorization":"BEARER"},"models":[{"id":"my-model"}]}}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := icodingagent.RunMigrations(cwd, agentDir); err != nil {
		t.Fatal(err)
	}
	migrated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Providers map[string]struct {
			APIKey  string            `json:"apiKey"`
			Headers map[string]string `json:"headers"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(migrated, &parsed); err != nil {
		t.Fatal(err)
	}
	provider := parsed.Providers["my-provider"]
	if provider.APIKey != "CUSTOM_API_KEY" || provider.Headers["Authorization"] != "BEARER" {
		t.Fatalf("migrated provider = %+v", provider)
	}
	services, err := NewServices(ServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if services.ModelRuntime().GetModel("my-provider", "my-model") == nil {
		t.Fatal("missing configured model")
	}
	probe := &modelOperationBridgeProbe{actions: make(map[string]any)}
	detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
	defer detach()
	getAuth := probe.actions["getModelAuth"].(func(context.Context, string, string) map[string]any)
	result := getAuth(t.Context(), "my-provider", "my-model")
	if result["ok"] != true || result["apiKey"] != "CUSTOM_API_KEY" {
		t.Fatalf("auth = %#v", result)
	}
	headers, ok := result["headers"].(ai.ProviderHeaders)
	if !ok || headers["Authorization"] == nil || *headers["Authorization"] != "BEARER" {
		t.Fatalf("headers = %#v", result["headers"])
	}
}
