package coding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/benchalloc"
	"github.com/MichaelKinsy/PiG/internal/catalogfixture"
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

// modelAuthFixture is a session's services over the fixture of internal/catalogfixture (models.json of catalogfixture.ModelsJSON, providers without a key), with a stored API key for the provider of the first fixture entry of provider openai: the API-key provider and the model whose auth the benchmark resolves. The built-in catalog is not an input of the benchmark.
func modelAuthFixture(tb testing.TB) (services *AgentSessionServices, providerID, modelID string) {
	tb.Helper()
	dir := tb.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), catalogfixture.ModelsJSON(""), 0o600); err != nil {
		tb.Fatal(err)
	}
	for _, row := range catalogfixture.GeneratedModels() {
		if row.Provider == "openai" {
			providerID, modelID = catalogfixture.ProviderPrefix+row.Provider, row.ID
			break
		}
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: tb.TempDir(), AgentDir: dir})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(services.Close)
	if loadError := services.Registry().LoadError(); loadError != "" {
		tb.Fatalf("the fixture's models.json does not load: %s", loadError)
	}
	if err := services.Auth().Set(providerID, ai.Credential{Type: ai.CredentialAPIKey, Key: "benchmark-key"}); err != nil {
		tb.Fatal(err)
	}
	return services, providerID, modelID
}

// The model of BenchmarkCatalogModelAuthCustomProvider is a fixture entry, not a built-in model: its provider is a fixture provider and the registry resolves it.
func TestCatalogModelAuthBenchmarkModelIsAFixtureEntry(t *testing.T) {
	services, providerID, modelID := modelAuthFixture(t)
	if !strings.HasPrefix(providerID, catalogfixture.ProviderPrefix) || modelID == "" {
		t.Fatalf("benchmark model %s/%s is not a fixture entry", providerID, modelID)
	}
	if services.ModelRuntime().GetModel(providerID, modelID) == nil {
		t.Fatalf("the fixture model %s/%s is not in the registry", providerID, modelID)
	}
}

// builtinAuthServices is a session's services over the built-in catalog with a stored API key for openai, and the number of openai models of the runtime: the models the auth lookup composes and scans (getModelAuth reads GetProviderModelData of the provider, which composes every model of the provider, to find one), counted through the runtime's own listing.
func builtinAuthServices(tb testing.TB) (services *AgentSessionServices, entries int) {
	tb.Helper()
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: tb.TempDir(), AgentDir: tb.TempDir()})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(services.Close)
	if err := services.Auth().Set("openai", ai.Credential{Type: ai.CredentialAPIKey, Key: "benchmark-key"}); err != nil {
		tb.Fatal(err)
	}
	return services, len(services.ModelRuntime().GetModels("openai"))
}

// The divisor of BenchmarkCatalogModelAuth is the number of models the lookup composes, not a constant: it equals the provider's model data the lookup reads.
func TestCatalogModelAuthBenchmarkDivisorIsTheModelsItComposes(t *testing.T) {
	services, entries := builtinAuthServices(t)
	if walked := len(services.Registry().GetProviderModelData("openai")); entries == 0 || entries != walked {
		t.Fatalf("divisor %d, the lookup composes %d openai models", entries, walked)
	}
}

// BenchmarkCatalogModelAuth resolves the auth of a built-in openai model. The lookup composes every model of the provider to find the one asked for, so its cost follows the provider's model count: allocs/op is allocations per 1,000 openai models (internal/benchalloc), and a catalog refresh that adds models does not move the row.
func BenchmarkCatalogModelAuth(b *testing.B) {
	services, entries := builtinAuthServices(b)
	probe := &modelOperationBridgeProbe{actions: map[string]any{}}
	detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
	b.Cleanup(detach)
	auth := probe.actions["getModelAuth"].(func(context.Context, string, string) map[string]any)
	b.Run("per-1000-entries", func(b *testing.B) {
		benchalloc.Per1000Entries(b, entries, func() {
			if result := auth(b.Context(), "openai", "gpt-5.4"); result["ok"] != true || result["apiKey"] != "benchmark-key" {
				b.Fatalf("auth=%+v", result)
			}
		})
	})
}

// BenchmarkCatalogModelAuthCustomProvider resolves the auth of a model of a models.json provider (a fixture entry of internal/catalogfixture under a stored key): a path with its own cost, measured per operation on a fixed input.
func BenchmarkCatalogModelAuthCustomProvider(b *testing.B) {
	services, providerID, modelID := modelAuthFixture(b)
	probe := &modelOperationBridgeProbe{actions: map[string]any{}}
	detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
	b.Cleanup(detach)
	auth := probe.actions["getModelAuth"].(func(context.Context, string, string) map[string]any)
	b.ReportAllocs()
	for b.Loop() {
		if result := auth(b.Context(), providerID, modelID); result["ok"] != true || result["apiKey"] != "benchmark-key" {
			b.Fatalf("auth=%+v", result)
		}
	}
}
