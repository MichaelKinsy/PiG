package oauth_test

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// PiG-only: packages/mcp/test/oauth.test.ts drives McpOAuthProvider through the flow and tests its failure
// semantics, but never reads back the state, code verifier, discovery state, invalidation kinds, configured client,
// metadata defaults or redirect hook that packages/mcp/src/oauth/provider.ts defines.
func newStateProvider(t *testing.T, options oauth.McpOAuthProviderOptions) (*oauth.McpOAuthProvider, *oauth.MemoryOAuthStateStore) {
	t.Helper()
	store := &oauth.MemoryOAuthStateStore{}
	options.ServerURL = "https://server.example/mcp"
	options.RedirectURL = "http://127.0.0.1:9/callback"
	options.Store = store
	if options.OnRedirect == nil {
		options.OnRedirect = func(context.Context, *url.URL) error { return nil }
	}
	provider, err := oauth.NewMcpOAuthProvider(options)
	if err != nil {
		t.Fatal(err)
	}
	return provider, store
}

func TestMcpOAuthProviderStateIsStableRandomHexUntilAllCredentialsAreInvalidated(t *testing.T) {
	ctx := t.Context()
	provider, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{})
	first, err := provider.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("state = %q, want 32 random bytes as hex", first)
	}
	if again, _ := provider.State(ctx); again != first {
		t.Fatalf("second state = %q, want the stored %q", again, first)
	}
	if err := provider.InvalidateCredentials(ctx, "tokens"); err != nil {
		t.Fatal(err)
	}
	if kept, _ := provider.State(ctx); kept != first {
		t.Fatalf("state after invalidating tokens = %q, want %q", kept, first)
	}
	if err := provider.InvalidateCredentials(ctx, "all"); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := provider.State(ctx); fresh == first {
		t.Fatal("state survived invalidating all credentials")
	}
}

func TestMcpOAuthProviderCodeVerifierRoundTripsAndFailsWhenNoneIsStored(t *testing.T) {
	ctx := t.Context()
	provider, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{})
	if _, err := provider.CodeVerifier(ctx); err == nil || err.Error() != "No OAuth PKCE code verifier is stored" {
		t.Fatalf("empty verifier error = %v", err)
	}
	if err := provider.SaveCodeVerifier(ctx, "verifier-1"); err != nil {
		t.Fatal(err)
	}
	if got, err := provider.CodeVerifier(ctx); err != nil || got != "verifier-1" {
		t.Fatalf("verifier = %q, %v", got, err)
	}
	if err := provider.InvalidateCredentials(ctx, "verifier"); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.CodeVerifier(ctx); err == nil {
		t.Fatal("verifier survived invalidating verifier")
	}
}

func TestMcpOAuthProviderInvalidatesOnlyTheNamedCredentialKind(t *testing.T) {
	ctx := t.Context()
	seed := func(t *testing.T) *oauth.McpOAuthProvider {
		provider, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{})
		if err := provider.SaveClientInformation(ctx, oauth.OAuthClientInformationMixed{ClientID: "cid"}); err != nil {
			t.Fatal(err)
		}
		if err := provider.SaveTokens(ctx, oauth.OAuthTokens{AccessToken: "a", TokenType: "Bearer"}); err != nil {
			t.Fatal(err)
		}
		if err := provider.SaveCodeVerifier(ctx, "v"); err != nil {
			t.Fatal(err)
		}
		if err := provider.SaveDiscoveryState(ctx, oauth.OAuthDiscoveryState{AuthorizationServerURL: "https://as.example"}); err != nil {
			t.Fatal(err)
		}
		return provider
	}
	present := func(provider *oauth.McpOAuthProvider) map[string]bool {
		client, _ := provider.ClientInformation(ctx)
		tokens, _ := provider.Tokens(ctx)
		_, verifierErr := provider.CodeVerifier(ctx)
		discovery, _ := provider.DiscoveryState(ctx)
		return map[string]bool{"client": client != nil, "tokens": tokens != nil, "verifier": verifierErr == nil, "discovery": discovery != nil}
	}
	for _, kind := range []string{"client", "tokens", "verifier", "discovery", "all"} {
		provider := seed(t)
		if err := provider.InvalidateCredentials(ctx, kind); err != nil {
			t.Fatal(err)
		}
		got := present(provider)
		for name, has := range got {
			want := kind != "all" && kind != name
			if has != want {
				t.Errorf("after invalidating %q, %s present = %v, want %v", kind, name, has, want)
			}
		}
	}
}

func TestMcpOAuthProviderDiscoveryStateRoundTrips(t *testing.T) {
	ctx := t.Context()
	provider, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{})
	if got, err := provider.DiscoveryState(ctx); err != nil || got != nil {
		t.Fatalf("empty discovery = %+v, %v", got, err)
	}
	want := oauth.OAuthDiscoveryState{AuthorizationServerURL: "https://as.example", ResourceMetadataURL: "https://server.example/.well-known/oauth-protected-resource"}
	if err := provider.SaveDiscoveryState(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := provider.DiscoveryState(ctx)
	if err != nil || got == nil || got.AuthorizationServerURL != want.AuthorizationServerURL || got.ResourceMetadataURL != want.ResourceMetadataURL {
		t.Fatalf("discovery = %+v, %v", got, err)
	}
}

func TestMcpOAuthProviderConfiguredClientWinsAndIgnoresSavedRegistrations(t *testing.T) {
	ctx := t.Context()
	provider, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{ClientID: "configured", ClientSecret: "s3cret"})
	if err := provider.SaveClientInformation(ctx, oauth.OAuthClientInformationMixed{ClientID: "registered"}); err != nil {
		t.Fatal(err)
	}
	got, err := provider.ClientInformation(ctx)
	if err != nil || got == nil || got.ClientID != "configured" || got.ClientSecret != "s3cret" {
		t.Fatalf("client = %+v, %v", got, err)
	}
	if method := provider.ClientMetadata().TokenEndpointAuthMethod; method != "client_secret_post" {
		t.Fatalf("auth method = %q, want client_secret_post when a secret is configured", method)
	}
}

func TestMcpOAuthProviderDefaultsClientMetadataFromTheRedirectURL(t *testing.T) {
	provider, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{ClientMetadata: oauth.OAuthClientMetadata{ClientName: "name"}})
	metadata := provider.ClientMetadata()
	if provider.RedirectURL() != "http://127.0.0.1:9/callback" || !slices.Equal(metadata.RedirectURIs, []string{provider.RedirectURL()}) {
		t.Fatalf("redirect = %q, uris = %v", provider.RedirectURL(), metadata.RedirectURIs)
	}
	if !slices.Equal(metadata.GrantTypes, []string{"authorization_code", "refresh_token"}) || !slices.Equal(metadata.ResponseTypes, []string{"code"}) || metadata.TokenEndpointAuthMethod != "none" || metadata.ClientName != "name" {
		t.Fatalf("metadata = %+v", metadata)
	}
	explicit, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{ClientMetadata: oauth.OAuthClientMetadata{
		RedirectURIs: []string{"http://x/cb"}, GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"token"}, TokenEndpointAuthMethod: "client_secret_basic",
	}})
	got := explicit.ClientMetadata()
	if !slices.Equal(got.RedirectURIs, []string{"http://x/cb"}) || !slices.Equal(got.GrantTypes, []string{"authorization_code"}) || !slices.Equal(got.ResponseTypes, []string{"token"}) || got.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Fatalf("explicit metadata overridden: %+v", got)
	}
}

func TestMcpOAuthProviderRedirectsThroughTheConfiguredHook(t *testing.T) {
	var got *url.URL
	provider, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{OnRedirect: func(_ context.Context, u *url.URL) error { got = u; return nil }})
	target := &url.URL{Scheme: "https", Host: "as.example", Path: "/authorize", RawQuery: "a=1"}
	if err := provider.RedirectToAuthorization(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("hook received %v, want %v", got, target)
	}
}

func TestMcpOAuthProviderForwardsTheClientMetadataDocumentCallback(t *testing.T) {
	var seen *oauth.AuthorizationServerMetadata
	provider, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{ClientMetadataDocument: func(m *oauth.AuthorizationServerMetadata) (*oauth.OAuthClientMetadataDocument, error) {
		seen = m
		return &oauth.OAuthClientMetadataDocument{URL: "https://client.example/meta.json"}, nil
	}})
	metadata := &oauth.AuthorizationServerMetadata{Issuer: "https://as.example"}
	doc, err := provider.ClientMetadataDocument(metadata)
	if err != nil || doc == nil || doc.URL != "https://client.example/meta.json" || seen != metadata {
		t.Fatalf("document = %+v, %v, seen %v", doc, err, seen)
	}
	plain, _ := newStateProvider(t, oauth.McpOAuthProviderOptions{})
	if doc, err := plain.ClientMetadataDocument(metadata); doc != nil || err != nil {
		t.Fatalf("no callback should yield no document, got %+v, %v", doc, err)
	}
}
