package codingagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func radiusTestConfigJSON(baseURL string) string {
	return `{"baseUrl":"` + baseURL + `","models":[{"id":"auto","name":"Radius Auto","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":0.2},"contextWindow":128000,"maxTokens":16384}]}`
}

// radiusTestRegistry builds a registry over agentDir with the given
// models.json and auth.json credentials and an in-memory models store.
func radiusTestRegistry(t *testing.T, modelsJSON string, credentials map[string]ai.Credential) (*ModelRegistry, *ai.AuthStorage, *ai.InMemoryModelsStore) {
	t.Helper()
	agentDir := t.TempDir()
	if modelsJSON != "" {
		if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	for providerID, credential := range credentials {
		if err := auth.Set(providerID, credential); err != nil {
			t.Fatal(err)
		}
	}
	registry := NewModelRegistry(agentDir)
	registry.SetAuthStorage(auth)
	store := ai.NewInMemoryModelsStore()
	registry.SetModelsStore(store)
	return registry, auth, store
}

type radiusTestGateway struct {
	mu       sync.Mutex
	requests []string
	auth     []string
	server   *httptest.Server
}

func newRadiusTestGateway(t *testing.T, handle func(http.ResponseWriter, *http.Request, string)) *radiusTestGateway {
	t.Helper()
	gateway := &radiusTestGateway{}
	gateway.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gateway.mu.Lock()
		gateway.requests = append(gateway.requests, request.URL.Path)
		gateway.auth = append(gateway.auth, request.Header.Get("authorization"))
		gateway.mu.Unlock()
		handle(writer, request, gateway.server.URL)
	}))
	t.Cleanup(gateway.server.Close)
	return gateway
}

func (gateway *radiusTestGateway) seen() ([]string, []string) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	return append([]string(nil), gateway.requests...), append([]string(nil), gateway.auth...)
}

func serveRadiusConfig(writer http.ResponseWriter, request *http.Request, origin string) {
	if request.URL.Path != "/v1/config" {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("content-type", "application/json")
	_, _ = writer.Write([]byte(radiusTestConfigJSON(origin + "/v1")))
}

func radiusOAuthCredential(gatewayConfig string) ai.Credential {
	return ai.Credential{Type: ai.CredentialOAuth, Access: "access-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli(), GatewayConfig: json.RawMessage(gatewayConfig)}
}

// Ported from coding-agent radius.test.ts "restores the legacy credential
// catalog without network access".
func TestRadiusRestoresLegacyCredentialCatalogOffline(t *testing.T) {
	registry, _, store := radiusTestRegistry(t, "", map[string]ai.Credential{
		"radius": radiusOAuthCredential(radiusTestConfigJSON("https://radius.example.com/v1")),
	})

	result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{})

	if len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
	entry, ok := registry.Resolve("radius", "auto")
	if !ok || entry.API != "pi-messages" || entry.BaseURL != "https://radius.example.com/v1" || entry.APIKey != "access-token" {
		t.Fatalf("radius/auto = %+v, %t", entry, ok)
	}
	if registry.GetProviderDisplayName("radius") != "Radius" || !registry.HasConfiguredAuth("radius") {
		t.Fatalf("display name %q, auth %t", registry.GetProviderDisplayName("radius"), registry.HasConfiguredAuth("radius"))
	}
	if stored, _ := store.Read(context.Background(), "radius"); stored == nil || len(stored.Models) != 1 {
		t.Fatalf("legacy catalog not persisted: %+v", stored)
	}
}

// Ported from radius.test.ts "fetches and stores the catalog for configured
// Radius auth". The gateway is the models.json "radius" entry, so the request
// stays on loopback; ai/radius_test.go asserts the default radius.pi.dev URL.
func TestRadiusFetchesAndStoresCatalogForConfiguredAuth(t *testing.T) {
	gateway := newRadiusTestGateway(t, serveRadiusConfig)
	registry, _, store := radiusTestRegistry(t, `{"providers":{"radius":{"oauth":"radius","baseUrl":"`+gateway.server.URL+`/v1"}}}`, map[string]ai.Credential{
		"radius": {Type: ai.CredentialOAuth, Access: "access-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})

	result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: true})

	if len(result.Errors) != 0 {
		t.Fatalf("errors = %v", result.Errors)
	}
	if !registry.HasGeneratedModel("radius", "auto") || !registry.HasModelDefinition("radius", "auto") {
		t.Fatal("refreshed radius/auto missing")
	}
	if stored, _ := store.Read(context.Background(), "radius"); stored == nil || len(stored.Models) != 1 {
		t.Fatalf("stored = %+v", stored)
	}
	paths, auth := gateway.seen()
	if len(paths) != 1 || paths[0] != "/v1/config" || auth[0] != "Bearer access-token" {
		t.Fatalf("requests = %v %v", paths, auth)
	}
}

// Ported from radius.test.ts "does not refresh catalogs over the network by default".
func TestRadiusCatalogRefreshStaysOfflineByDefault(t *testing.T) {
	gateway := newRadiusTestGateway(t, func(http.ResponseWriter, *http.Request, string) { t.Error("unexpected catalog fetch") })
	registry, _, _ := radiusTestRegistry(t, `{"providers":{"radius":{"oauth":"radius","baseUrl":"`+gateway.server.URL+`"}}}`, map[string]ai.Credential{
		"radius": radiusOAuthCredential(radiusTestConfigJSON("https://radius.example.com/v1")),
	})

	registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{})

	if !registry.HasModelDefinition("radius", "auto") {
		t.Fatal("stored radius/auto missing")
	}
}

// Ported from radius.test.ts "does not fetch or make Radius models available
// without configured auth".
func TestRadiusWithoutAuthIsUnavailableAndNotFetched(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "")
	gateway := newRadiusTestGateway(t, func(http.ResponseWriter, *http.Request, string) { t.Error("unauthenticated catalog fetch") })
	registry, _, _ := radiusTestRegistry(t, `{"providers":{"radius":{"oauth":"radius","baseUrl":"`+gateway.server.URL+`"}}}`, nil)

	registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: true})

	for _, entry := range registry.GetAvailable() {
		if entry.ProviderID == "radius" {
			t.Fatalf("unauthenticated radius model available: %+v", entry)
		}
	}
	if registry.HasConfiguredAuth("radius") {
		t.Fatal("radius reports configured auth without a credential")
	}
	withoutRadius := registry.AvailableProviderCount()
	t.Setenv("RADIUS_API_KEY", "env-key")
	if got := registry.AvailableProviderCount(); got != withoutRadius+1 {
		t.Fatalf("available providers with RADIUS_API_KEY = %d, want %d", got, withoutRadius+1)
	}
}

func TestRadiusEnvironmentKeyMakesPublicCatalogAvailable(t *testing.T) {
	t.Setenv("RADIUS_API_KEY", "env-key")
	registry, _, _ := radiusTestRegistry(t, "", nil)

	available := 0
	for _, entry := range registry.GetAvailable() {
		if entry.ProviderID == "radius" && entry.API == "pi-messages" {
			available++
		}
	}
	if available != len(ai.ListModels("radius")) {
		t.Fatalf("available radius models = %d, want the public catalog %d", available, len(ai.ListModels("radius")))
	}
	if entry, ok := registry.Resolve("radius", "balanced"); !ok || entry.APIKey != "env-key" {
		t.Fatalf("radius/balanced = %+v, %t", entry, ok)
	}
}

// Ported from radius.test.ts "supports custom Radius gateways from models.json".
func TestRadiusSupportsCustomGatewaysFromModelsJSON(t *testing.T) {
	gateway := newRadiusTestGateway(t, serveRadiusConfig)
	registry, _, _ := radiusTestRegistry(t, `{"providers":{"radius-dev":{"name":"Radius (dev)","baseUrl":"`+gateway.server.URL+`","oauth":"radius"}}}`, map[string]ai.Credential{
		"radius-dev": {Type: ai.CredentialOAuth, Access: "access-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})

	registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: true})

	entry, ok := registry.Resolve("radius-dev", "auto")
	if !ok || entry.API != "pi-messages" || entry.BaseURL != gateway.server.URL+"/v1" {
		t.Fatalf("radius-dev/auto = %+v, %t", entry, ok)
	}
	if name := registry.GetProviderDisplayName("radius-dev"); name != "Radius (dev)" {
		t.Fatalf("display name = %q", name)
	}
	flow, ok := registry.RadiusOAuth("radius-dev")
	if !ok || flow.Name() != "Radius (dev)" || flow.Gateway() != gateway.server.URL {
		t.Fatalf("radius-dev OAuth = %+v, %t", flow, ok)
	}
}

// Ported from radius.test.ts "requires baseUrl for custom Radius gateways".
func TestRadiusCustomGatewayRequiresBaseURL(t *testing.T) {
	registry, _, _ := radiusTestRegistry(t, `{"providers":{"radius-dev":{"oauth":"radius"}}}`, nil)

	if !strings.Contains(registry.LoadError(), `"baseUrl" is required when "oauth" is set`) {
		t.Fatalf("load error = %q", registry.LoadError())
	}
	if _, ok := registry.RadiusOAuth("radius-dev"); ok {
		t.Fatal("radius-dev was configured without a baseUrl")
	}
}

func TestRadiusAPIKeyRefreshesExpiredOAuthToken(t *testing.T) {
	gateway := newRadiusTestGateway(t, func(writer http.ResponseWriter, request *http.Request, _ string) {
		_ = request.ParseForm()
		if request.URL.Path != "/v1/oauth/token" || request.PostForm.Get("refresh_token") != "old-refresh" {
			http.Error(writer, "unexpected", http.StatusBadRequest)
			return
		}
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"scope":"gateway offline_access"}`))
	})
	registry, auth, _ := radiusTestRegistry(t, `{"providers":{"radius-dev":{"baseUrl":"`+gateway.server.URL+`","oauth":"radius"}}}`, map[string]ai.Credential{
		"radius-dev": {Type: ai.CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: 1},
	})

	key, err := registry.RadiusAPIKey(context.Background(), "radius-dev")

	if err != nil || key != "new-access" {
		t.Fatalf("key = %q, %v", key, err)
	}
	stored, _, _ := auth.GetRaw("radius-dev")
	if stored.Access != "new-access" || stored.Refresh != "new-refresh" || stored.Scope != "gateway offline_access" {
		t.Fatalf("stored credential = %+v", stored)
	}
}

func TestModelNetworkEnabledHonorsOfflineVariables(t *testing.T) {
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	if ModelNetworkEnabled() {
		t.Fatal("PI_OFFLINE set (even empty) must disable model network access, as in Pi")
	}
	if err := os.Unsetenv("PI_OFFLINE"); err != nil { // t.Setenv above restores it
		t.Fatal(err)
	}
	if !ModelNetworkEnabled() {
		t.Fatal("network disabled without offline variables")
	}
	t.Setenv("PIG_OFFLINE", "true")
	if ModelNetworkEnabled() {
		t.Fatal("PIG_OFFLINE=true must disable model network access")
	}
}
