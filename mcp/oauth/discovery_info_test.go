package oauth_test

import (
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// PiG-only: packages/mcp/test/oauth.test.ts reaches discoverProtectedResourceMetadata and discoverOAuthServerInfo only
// through the authorization flow, so the path-aware lookup, the root fallback, the explicit metadata URL, the
// protocol-version header and the configured authorization-server metadata document have no direct test.
func TestDiscoverProtectedResourceMetadataTriesThePathAwareURLThenTheRoot(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var paths []string
	var version, accept string
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		version, accept = r.Header.Get("MCP-Protocol-Version"), r.Header.Get("Accept")
		mu.Unlock()
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			jsonResponse(w, 200, map[string]any{"resource": serverOrigin + "/mcp", "authorization_servers": []string{"https://as.example"}})
			return
		}
		w.WriteHeader(404)
	})
	metadata, err := oauth.DiscoverProtectedResourceMetadata(ctx, origin+"/mcp", oauth.DiscoveryOptions{ProtocolVersion: "2025-03-26"})
	if err != nil || metadata == nil || len(metadata.AuthorizationServers) != 1 || metadata.AuthorizationServers[0] != "https://as.example" {
		t.Fatalf("metadata = %+v, %v", metadata, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/.well-known/oauth-protected-resource/mcp" || paths[1] != "/.well-known/oauth-protected-resource" {
		t.Fatalf("lookup order = %v", paths)
	}
	if version != "2025-03-26" || accept != "application/json" {
		t.Fatalf("headers version=%q accept=%q", version, accept)
	}
}

func TestDiscoverProtectedResourceMetadataUsesAnExplicitURLWithoutFallingBack(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var paths []string
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(500)
	})
	_, err := oauth.DiscoverProtectedResourceMetadata(ctx, origin+"/mcp", oauth.DiscoveryOptions{ResourceMetadataURL: origin + "/custom/prm.json"})
	if err == nil || err.Error() != "HTTP 500 loading OAuth protected resource metadata" {
		t.Fatalf("err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/custom/prm.json" {
		t.Fatalf("requests = %v, want only the explicit URL", paths)
	}
}

func TestDiscoverOAuthServerInfoFollowsTheFirstAuthorizationServerOfTheResourceMetadata(t *testing.T) {
	ctx := t.Context()
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			jsonResponse(w, 200, map[string]any{"resource": serverOrigin + "/mcp", "authorization_servers": []string{serverOrigin + "/tenant", "https://ignored.example"}})
		case "/.well-known/oauth-authorization-server/tenant":
			jsonResponse(w, 200, map[string]any{"issuer": serverOrigin + "/tenant", "authorization_endpoint": serverOrigin + "/a", "token_endpoint": serverOrigin + "/t", "response_types_supported": []string{"code"}})
		default:
			w.WriteHeader(404)
		}
	})
	info, err := oauth.DiscoverOAuthServerInfo(ctx, origin+"/mcp", oauth.DiscoveryOptions{})
	if err != nil || info == nil {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if info.AuthorizationServerURL != origin+"/tenant" || info.AuthorizationServerMetadata == nil || info.AuthorizationServerMetadata.TokenEndpoint != origin+"/t" || info.ResourceMetadata == nil {
		t.Fatalf("info = %+v", info)
	}
}

func TestDiscoverOAuthServerInfoFallsBackToTheServerOriginWithoutResourceMetadata(t *testing.T) {
	ctx := t.Context()
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			jsonResponse(w, 200, map[string]any{"issuer": serverOrigin + "/", "authorization_endpoint": serverOrigin + "/a", "token_endpoint": serverOrigin + "/t", "response_types_supported": []string{"code"}})
			return
		}
		w.WriteHeader(404)
	})
	info, err := oauth.DiscoverOAuthServerInfo(ctx, origin+"/mcp", oauth.DiscoveryOptions{})
	if err != nil || info == nil || info.ResourceMetadata != nil || info.AuthorizationServerURL != origin+"/" || info.AuthorizationServerMetadata == nil {
		t.Fatalf("info = %+v, %v", info, err)
	}
}

func TestDiscoverOAuthServerInfoUsesAConfiguredMetadataDocumentAndSkipsIssuerValidationOnRequest(t *testing.T) {
	ctx := t.Context()
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/idp.json":
			jsonResponse(w, 200, map[string]any{"issuer": "https://idp.example", "authorization_endpoint": serverOrigin + "/a", "token_endpoint": serverOrigin + "/t", "response_types_supported": []string{"code"}})
		case "/broken.json":
			w.WriteHeader(503)
		case "/.well-known/oauth-authorization-server":
			jsonResponse(w, 200, map[string]any{"issuer": "https://other.example", "authorization_endpoint": serverOrigin + "/a", "token_endpoint": serverOrigin + "/t", "response_types_supported": []string{"code"}})
		default:
			w.WriteHeader(404)
		}
	})
	document, _ := url.Parse(origin + "/idp.json")
	info, err := oauth.DiscoverOAuthServerInfo(ctx, origin+"/mcp", oauth.DiscoveryOptions{AuthorizationServerMetadataURL: document})
	if err != nil || info == nil || info.AuthorizationServerURL != "https://idp.example" || info.AuthorizationServerMetadata == nil {
		t.Fatalf("configured info = %+v, %v", info, err)
	}
	broken, _ := url.Parse(origin + "/broken.json")
	if _, err := oauth.DiscoverOAuthServerInfo(ctx, origin+"/mcp", oauth.DiscoveryOptions{AuthorizationServerMetadataURL: broken}); err == nil || err.Error() != "HTTP 503 loading authorization server metadata from "+broken.String() {
		t.Fatalf("broken err = %v", err)
	}
	if _, err := oauth.DiscoverOAuthServerInfo(ctx, origin+"/mcp", oauth.DiscoveryOptions{}); err == nil {
		t.Fatal("an issuer that differs from the discovery URL must be rejected")
	}
	skipped, err := oauth.DiscoverOAuthServerInfo(ctx, origin+"/mcp", oauth.DiscoveryOptions{SkipIssuerValidation: true})
	if err != nil || skipped == nil || skipped.AuthorizationServerMetadata == nil || skipped.AuthorizationServerMetadata.Issuer != "https://other.example" {
		t.Fatalf("skipped = %+v, %v", skipped, err)
	}
}
