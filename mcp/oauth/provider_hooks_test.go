package oauth_test

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// PiG-only: packages/mcp/test/oauth.test.ts never gives its provider the optional addClientAuthentication or
// clientMetadataDocument members of OAuthClientProvider, so the flow paths that call them have no upstream test.

type authenticatingProvider struct {
	*testOAuthProvider
	add oauth.AddClientAuthentication
}

func (p authenticatingProvider) AddClientAuthentication() oauth.AddClientAuthentication { return p.add }

type documentProvider struct {
	*testOAuthProvider
	document *oauth.OAuthClientMetadataDocument
	seen     *oauth.AuthorizationServerMetadata
}

func (p *documentProvider) ClientMetadataDocument(metadata *oauth.AuthorizationServerMetadata) (*oauth.OAuthClientMetadataDocument, error) {
	p.seen = metadata
	return p.document, nil
}

// Pi packages/mcp/src/oauth/flow.ts:34-39 AddClientAuthentication(headers, params, url: string | URL, metadata) and flow.ts:210-211: the provider hook replaces the default
// client authentication and receives the token endpoint URL (here its string form), so it can sign for it.
// mutation-checked: passing an empty endpoint fails this test.
func TestMCPOAuthTokenRequestUsesTheProvidersAddClientAuthentication(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var header, assertion, grant string
	var secret bool
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		params, _ := url.ParseQuery(readAll(r))
		mu.Lock()
		header, assertion, grant = r.Header.Get("X-Client-Assertion"), params.Get("client_assertion"), params.Get("grant_type")
		secret = params.Has("client_secret") || r.Header.Get("Authorization") != ""
		mu.Unlock()
		jsonResponse(w, 200, map[string]any{"access_token": "token", "token_type": "Bearer"})
	})
	base := newTestOAuthProvider("http://127.0.0.1/callback")
	base.client = &oauth.OAuthClientInformationMixed{ClientID: "client", ClientSecret: "ignored-secret"}
	base.verifier = "verifier"
	base.discovery = &oauth.OAuthDiscoveryState{
		AuthorizationServerURL: origin,
		AuthorizationServerMetadata: &oauth.AuthorizationServerMetadata{
			Issuer: origin, AuthorizationEndpoint: origin + "/authorize", TokenEndpoint: origin + "/token", ResponseTypesSupported: []string{"code"},
			TokenEndpointAuthMethodsSupported: []string{"client_secret_basic"},
		},
	}
	var add oauth.AddClientAuthentication = func(_ context.Context, headers http.Header, params *oauth.URLSearchParams, endpoint string, _ *oauth.AuthorizationServerMetadata) error {
		endpointURL, err := url.Parse(endpoint)
		if err != nil {
			return err
		}
		headers.Set("X-Client-Assertion", "signed-for-"+endpointURL.Path)
		params.Set("client_assertion", "jwt")
		return nil
	}
	provider := authenticatingProvider{testOAuthProvider: base, add: add}
	result, err := oauth.AuthorizeMcp(ctx, provider, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp", AuthorizationCode: "code"})
	if err != nil || result != oauth.OAuthAuthorized {
		t.Fatalf("authorize = %q, %v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if header != "signed-for-/token" || assertion != "jwt" || grant != "authorization_code" {
		t.Fatalf("token request header=%q assertion=%q grant=%q", header, assertion, grant)
	}
	if secret {
		t.Fatal("the default client authentication ran although the provider replaced it")
	}
}

func TestMCPOAuthUsesTheProvidersClientMetadataDocumentInsteadOfRegistering(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var registrations int
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			jsonResponse(w, 200, map[string]any{"resource": serverOrigin + "/mcp", "authorization_servers": []string{serverOrigin}})
		case "/.well-known/oauth-authorization-server":
			jsonResponse(w, 200, map[string]any{
				"issuer": serverOrigin, "authorization_endpoint": serverOrigin + "/authorize", "token_endpoint": serverOrigin + "/token",
				"registration_endpoint": serverOrigin + "/register", "response_types_supported": []string{"code"},
				"client_id_metadata_document_supported": true,
			})
		case "/register":
			mu.Lock()
			registrations++
			mu.Unlock()
			jsonResponse(w, 201, map[string]any{"client_id": "registered"})
		default:
			w.WriteHeader(404)
		}
	})
	provider := &documentProvider{
		testOAuthProvider: newTestOAuthProvider("http://127.0.0.1/callback"),
		document:          &oauth.OAuthClientMetadataDocument{URL: "https://client.example/metadata.json", RedirectURL: "http://127.0.0.1/document-callback"},
	}
	result, err := oauth.AuthorizeMcp(ctx, provider, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp"})
	if err != nil || result != oauth.OAuthRedirect {
		t.Fatalf("authorize = %q, %v", result, err)
	}
	if provider.seen == nil || provider.seen.Issuer != origin {
		t.Fatalf("callback saw metadata %+v, want the discovered server metadata", provider.seen)
	}
	query := provider.authorizationURL.Query()
	if query.Get("client_id") != "https://client.example/metadata.json" || query.Get("redirect_uri") != "http://127.0.0.1/document-callback" {
		t.Fatalf("authorization query = %v", query)
	}
	mu.Lock()
	defer mu.Unlock()
	if registrations != 0 {
		t.Fatalf("registered %d times although the provider supplied a metadata document", registrations)
	}

	insecure := &documentProvider{testOAuthProvider: newTestOAuthProvider("http://127.0.0.1/callback"), document: &oauth.OAuthClientMetadataDocument{URL: "http://client.example/metadata.json", RedirectURL: "http://127.0.0.1/cb"}}
	if _, err := oauth.AuthorizeMcp(ctx, insecure, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp"}); err == nil || err.Error() != "Invalid OAuth client metadata URL" {
		t.Fatalf("insecure document err = %v", err)
	}
}
