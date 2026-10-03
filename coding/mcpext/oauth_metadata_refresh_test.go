package mcpext_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// oauth.ts createMcpAuthProvider: a refresh after a 401 loads the configured authorization server metadata document
// instead of discovering one, and the stored discovery stays as it was.
func TestMcpAuthProviderRefreshUsesTheConfiguredAuthServerMetadataURL(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(404)
	}))
	defer server.Close()
	serverURL := server.URL + "/mcp"
	store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "").ForServer("test", serverURL)
	if err != nil {
		t.Fatal(err)
	}
	stored := oauth.McpOAuthState{
		ServerURL:         serverURL,
		ClientInformation: &oauth.OAuthClientInformation{ClientID: "client"},
		Tokens:            &oauth.OAuthTokens{AccessToken: "old", TokenType: "Bearer", RefreshToken: "refresh"},
	}
	if err := store.Save(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	metadataURL, _ := url.Parse(server.URL + "/idp/metadata.json")
	provider := mcpext.NewMcpAuthProvider(mcpext.McpAuthProviderOptions{
		ServerURL: serverURL,
		Store:     store,
		Settings: func() (mcpext.McpOAuthSettings, error) {
			return mcpext.McpOAuthSettings{AuthServerMetadataURL: metadataURL}, nil
		},
	})
	handler, ok := provider.(mcp.UnauthorizedHandler)
	if !ok {
		t.Fatal("the provider does not handle 401s")
	}
	mcpURL, _ := url.Parse(serverURL)
	err = handler.OnUnauthorized(t.Context(), mcp.UnauthorizedContext{
		Response: &http.Response{StatusCode: 401, Header: http.Header{}}, ServerURL: mcpURL, Fetch: http.DefaultClient, Token: "old",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "HTTP 404 loading authorization server metadata from "+metadataURL.String()) {
		t.Fatalf("err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 || paths[len(paths)-1] != "/idp/metadata.json" {
		t.Fatalf("requests = %q", paths)
	}
	for _, path := range paths {
		if strings.HasPrefix(path, "/.well-known/oauth-authorization-server") || strings.HasPrefix(path, "/.well-known/openid-configuration") {
			t.Fatalf("discovered authorization server metadata: %q", paths)
		}
	}
	state, err := store.Load(t.Context())
	if err != nil || state == nil || state.Discovery != nil || state.Tokens == nil || state.Tokens.AccessToken != "old" {
		t.Fatalf("state = %#v, %v", state, err)
	}
}

// runtime.ts oauthSettings: the configured `oauth.authServerMetadataUrl` reaches the OAuth settings as a URL.
func TestMCPConnectionOAuthSettingsCarryTheAuthServerMetadataURL(t *testing.T) {
	servers := &serverSet{}
	connection, _ := connect(t, mcpext.McpServerEntry{
		Name: "fake", Source: "test",
		Config: extension.McpServerConfig{URL: "http://unused.invalid", OAuth: &extension.McpOAuthConfig{AuthServerMetadataURL: "https://idp.example/m"}},
	}, []transportSource{func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) }}, nil)
	settings, err := connection.OAuthSettings()
	if err != nil || settings.AuthServerMetadataURL == nil || settings.AuthServerMetadataURL.String() != "https://idp.example/m" {
		t.Fatalf("settings = %#v, %v", settings, err)
	}
	// `new URL()` strips surrounding whitespace and drops tabs and newlines, so config validation accepts this URL.
	connection, _ = connect(t, mcpext.McpServerEntry{
		Name: "spaced", Source: "test",
		Config: extension.McpServerConfig{URL: "http://unused.invalid", OAuth: &extension.McpOAuthConfig{AuthServerMetadataURL: " https://idp.example/\tm\n"}},
	}, []transportSource{func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) }}, nil)
	settings, err = connection.OAuthSettings()
	if err != nil || settings.AuthServerMetadataURL == nil || settings.AuthServerMetadataURL.String() != "https://idp.example/m" {
		t.Fatalf("spaced settings = %#v, %v", settings, err)
	}
	connection, _ = connect(t, mcpext.McpServerEntry{
		Name: "plain", Source: "test", Config: extension.McpServerConfig{URL: "http://unused.invalid", OAuth: &extension.McpOAuthConfig{}},
	}, []transportSource{func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) }}, nil)
	if settings, err := connection.OAuthSettings(); err != nil || settings.AuthServerMetadataURL != nil {
		t.Fatalf("unset settings = %#v, %v", settings, err)
	}
}

// oauth.ts forServer load: `states[key] || !states[legacyKey]` lets legacy state replace a falsy (`null`) entry
// under the new key, and tokens() falls back to legacy state for a `null` entry (`??`).
func TestMcpOAuthCredentialStoreTakesLegacyStateOverANullEntry(t *testing.T) {
	const serverURL = "https://mcp.example.com/mcp"
	backend := &mcpext.InMemoryAuthStorageBackend{}
	content := `{"mcp__work|` + serverURL + `":null,"` + serverURL + `":{"serverUrl":"` + serverURL + `","tokens":{"access_token":"legacy","token_type":"Bearer"}}}`
	if err := backend.WithLock(func(string, bool) (*string, error) { return &content, nil }); err != nil {
		t.Fatal(err)
	}
	credentials := mcpext.NewMcpOAuthCredentialStoreWithBackend(backend, "")
	if tokens := credentials.Tokens("work", serverURL); tokens == nil || tokens.AccessToken != "legacy" {
		t.Fatalf("tokens = %#v", tokens)
	}
	store, err := credentials.ForServer("work", serverURL)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(t.Context())
	if err != nil || state == nil || state.Tokens == nil || state.Tokens.AccessToken != "legacy" {
		t.Fatalf("state = %#v, %v", state, err)
	}
	var stored string
	_ = backend.WithLock(func(current string, _ bool) (*string, error) { stored = current; return nil, nil })
	if strings.Contains(stored, `"`+serverURL+`": {`) || !strings.HasPrefix(stored, "{\n  \"mcp__work|"+serverURL+"\": {\n") {
		t.Fatalf("stored = %s", stored)
	}
}
