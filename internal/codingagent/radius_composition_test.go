package codingagent

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// provider-composer applies custom model upserts before modelOverrides, and
// composes headers/auth for Radius just as for other built-in providers.
func TestRadiusModelsJSONComposition(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "")
	registry, _, store := radiusTestRegistry(t, `{"providers":{"radius-dev":{
		"oauth":"radius","baseUrl":"https://configured.example/v1","apiKey":"configured-key",
		"headers":{"X-Provider":"provider"},"modelOverrides":{"auto":{"name":"Override","contextWindow":4242,"headers":{"X-Model":"override"}}},
		"models":[{"id":"auto","name":"Defined","maxTokens":77},{"id":"custom","api":"pi-messages","name":"Custom"}]
	}}}`, nil)
	config, _ := ai.GetRadiusCredentialConfig(&ai.Credential{GatewayConfig: json.RawMessage(radiusTestConfigJSON("https://catalog.example/v1"))})
	models := ai.GetRadiusModelsFromConfig("radius-dev", config)
	raw, err := json.Marshal(models[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), "radius-dev", ai.ModelsStoreEntry{Models: []json.RawMessage{raw}}); err != nil {
		t.Fatal(err)
	}
	registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{})
	entry, ok := registry.Resolve("radius-dev", "auto")
	if !ok || entry.DisplayName != "Override" || entry.ContextWindow != 4242 || entry.MaxTokens != 77 || entry.BaseURL != "https://configured.example/v1" || entry.API != "pi-messages" || entry.Headers["X-Provider"] != "provider" || entry.Headers["X-Model"] != "override" {
		t.Errorf("composed auto = %+v, %t", entry, ok)
	}
	if custom, ok := registry.Resolve("radius-dev", "custom"); !ok || custom.DisplayName != "Custom" || custom.API != "pi-messages" || !registry.HasModelDefinition("radius-dev", "custom") {
		t.Errorf("custom definition = %+v, %t", custom, ok)
	}
	for name, entries := range map[string][]ModelEntry{"all": registry.GetAll(), "available": registry.GetAvailable()} {
		seen := map[string]int{}
		for _, entry := range entries {
			if entry.ProviderID == "radius-dev" {
				seen[entry.ModelID]++
				if entry.ModelID == "auto" && entry.DisplayName != "Override" {
					t.Errorf("%s returned uncomposed auto: %+v", name, entry)
				}
			}
		}
		if seen["auto"] != 1 || seen["custom"] != 1 || len(seen) != 2 {
			t.Errorf("%s identities = %v; want each composed model once", name, seen)
		}
	}
	if !registry.HasConfiguredAuth("radius-dev") {
		t.Error("configured API key did not make Radius available")
	}
	if key, err := registry.RadiusAPIKey(context.Background(), "radius-dev"); err != nil || key != "configured-key" {
		t.Errorf("request key = %q, %v", key, err)
	}
}

func TestRadiusCatalogRefreshUsesConfiguredAPIKey(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "environment-key")
	gateway := newRadiusTestGateway(t, func(w http.ResponseWriter, r *http.Request, origin string) {
		if got := r.Header.Get("Authorization"); got != "Bearer configured-key" {
			t.Errorf("catalog auth = %q", got)
		}
		serveRadiusConfig(w, r, origin)
	})
	registry, _, _ := radiusTestRegistry(t, `{"providers":{"radius-dev":{"oauth":"radius","baseUrl":"`+gateway.server.URL+`/v1","apiKey":"configured-key","modelOverrides":{"auto":{"name":"Configured override"}}}}}`, nil)
	result := registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{AllowNetwork: true, Providers: []string{"radius-dev"}})
	if len(result.Errors) != 0 || result.Aborted {
		t.Fatal(result)
	}
	entry, ok := registry.Resolve("radius-dev", "auto")
	if !ok || entry.DisplayName != "Configured override" || entry.BaseURL != gateway.server.URL+"/v1" {
		t.Fatalf("refreshed composed model = %+v, %t", entry, ok)
	}
}

func TestRadiusCompositionPreservesCatalogURLUnlessPlainOverride(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		config := `{"providers":{"radius":{"baseUrl":"https://configured.example/v1","modelOverrides":{"balanced":{"name":"Renamed"}}`
		if oauth {
			config += `,"oauth":"radius"`
		}
		config += `}}}`
		registry, _, store := radiusTestRegistry(t, config, nil)
		if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: []json.RawMessage{json.RawMessage(`{"id":"balanced","name":"Cached","provider":"radius","api":"pi-messages","baseUrl":"https://catalog.example/v1","input":["text"],"cost":{"input":1},"contextWindow":1000,"maxTokens":50}`)}}); err != nil {
			t.Fatal(err)
		}
		registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{})
		wantURL := "https://configured.example/v1"
		if oauth {
			wantURL = "https://catalog.example/v1"
		}
		entry, ok := registry.Resolve("radius", "balanced")
		if !ok || entry.BaseURL != wantURL || entry.DisplayName != "Renamed" || entry.InputCost != 1 || entry.MaxTokens != 50 {
			t.Errorf("oauth=%t entry=%+v found=%t", oauth, entry, ok)
		}
	}
}

func TestRadiusConfiguredCredentialPrecedence(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "environment-key")
	t.Setenv("RADIUS_TEST_MISSING", "")
	for _, tc := range []struct {
		name, configured string
		stored           *ai.Credential
		want             string
		wantError        bool
	}{
		{name: "configured", configured: "configured-key", want: "configured-key"},
		{name: "stored", configured: "configured-key", stored: &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"}, want: "stored-key"},
		{name: "empty-stored-falls-to-env", configured: "configured-key", stored: &ai.Credential{Type: ai.CredentialAPIKey}, want: "environment-key"},
		{name: "missing-configured-env-errors", configured: "$RADIUS_TEST_MISSING", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credentials := map[string]ai.Credential{}
			if tc.stored != nil {
				credentials["radius"] = *tc.stored
			}
			registry, _, _ := radiusTestRegistry(t, `{"providers":{"radius":{"apiKey":"`+tc.configured+`"}}}`, credentials)
			key, err := registry.RadiusAPIKey(t.Context(), "radius")
			if (err != nil) != tc.wantError || key != tc.want {
				t.Fatalf("key=%q err=%v; want %q error=%t", key, err, tc.want, tc.wantError)
			}
			if tc.wantError && registry.HasConfiguredAuth("radius") {
				t.Fatal("missing configured variable fell through to unrelated environment key")
			}
		})
	}
}
