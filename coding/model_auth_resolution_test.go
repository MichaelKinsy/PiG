package coding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi model-registry.ts:66-103 resolves a catalog model through the same auth facade, retaining compatibility headers when credentials are absent.
func TestExtensionModelAuthUsesSharedCompatibilityResolution(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	for _, tc := range []struct {
		name, config, key, model string
		want                     ResolvedRequestAuth
	}{
		{name: "stored", key: "stored-key", model: "gpt-5.4", want: ResolvedRequestAuth{OK: true, APIKey: new("stored-key")}},
		{name: "unconfigured", model: "gpt-5.4", want: ResolvedRequestAuth{OK: true}},
		{name: "unknown", model: "missing-model", want: ResolvedRequestAuth{Error: "model not found"}},
		{name: "compatibility headers", config: `{"providers":{"openai":{"headers":{"X-Only":"value"}}}}`, model: "gpt-5.4", want: ResolvedRequestAuth{OK: true, Headers: ai.ProviderHeaders{"X-Only": new("value")}}},
		{name: "required key", config: `{"providers":{"openai":{"authHeader":true}}}`, model: "gpt-5.4", want: ResolvedRequestAuth{Error: `No API key found for "openai"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if tc.key != "" {
				if err := services.Auth().Set("openai", ai.Credential{Type: ai.CredentialAPIKey, Key: tc.key}); err != nil {
					t.Fatal(err)
				}
			}
			probe := &modelOperationBridgeProbe{actions: map[string]any{}}
			detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
			defer detach()
			value := probe.actions["getModelAuth"].(func(context.Context, string, string) map[string]any)(t.Context(), "openai", tc.model)
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var got ResolvedRequestAuth
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("auth=%s, want %+v", data, tc.want)
			}
		})
	}
}

func BenchmarkCatalogModelAuth(b *testing.B) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	if err := services.Auth().Set("openai", ai.Credential{Type: ai.CredentialAPIKey, Key: "benchmark-key"}); err != nil {
		b.Fatal(err)
	}
	probe := &modelOperationBridgeProbe{actions: map[string]any{}}
	detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
	b.Cleanup(detach)
	auth := probe.actions["getModelAuth"].(func(context.Context, string, string) map[string]any)
	b.ReportAllocs()
	for b.Loop() {
		if result := auth(b.Context(), "openai", "gpt-5.4"); result["ok"] != true || result["apiKey"] != "benchmark-key" {
			b.Fatalf("auth=%+v", result)
		}
	}
}
