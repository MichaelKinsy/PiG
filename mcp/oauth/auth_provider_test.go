package oauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/test/auth-provider.test.ts.

// rejectedRequest is what a connection's request reports after its token was rejected.
func rejectedRequest(t *testing.T, server *authorizationServer, token string, fetch mcp.McpFetch) mcp.UnauthorizedContext {
	t.Helper()
	serverURL, err := url.Parse(server.ServerURL)
	if err != nil {
		t.Fatal(err)
	}
	if fetch == nil {
		fetch = http.DefaultClient
	}
	return mcp.UnauthorizedContext{Response: &http.Response{StatusCode: 401, Header: http.Header{}}, ServerURL: serverURL, Fetch: fetch, Token: token}
}

func authProviderFor(options oauth.McpSignInOptions, settings oauth.McpOAuthSettings) oauth.McpAuthProvider {
	return oauth.CreateMcpAuthProvider(oauth.McpAuthProviderOptions{
		ServerURL: options.ServerURL, Store: options.Store, ClientName: options.ClientName, ClientMetadataDocuments: options.ClientMetadataDocuments,
		Settings: func() (oauth.McpOAuthSettings, error) { return settings, nil }, OnChallenge: func(oauth.OAuthChallenge) {},
	})
}

func expectAuthorizationRequired(t *testing.T, err error) {
	t.Helper()
	if _, ok := errors.AsType[*oauth.McpOAuthAuthorizationRequiredError](err); !ok {
		t.Fatalf("err = %v, want McpOAuthAuthorizationRequiredError", err)
	}
}

func expectToken(t *testing.T, provider oauth.McpAuthProvider, want string) {
	t.Helper()
	got, err := provider.Token(t.Context())
	if err != nil || got != want {
		t.Fatalf("token = %q, %v, want %q", got, err, want)
	}
}

func TestCreateMcpAuthProviderRefreshesARejectedToken(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	provider := authProviderFor(options, oauth.McpOAuthSettings{})

	if err := provider.OnUnauthorized(t.Context(), rejectedRequest(t, server, "access-1", nil)); err != nil {
		t.Fatal(err)
	}
	expectToken(t, provider, "access-2")
	if got := server.TokenRequests()[1].Get("client_id"); got != "client-1" {
		t.Fatalf("refresh client_id = %q, want client-1", got)
	}
}

// mutation-checked: a refresh that may prepare an authorization request (no RefreshOnly) fails it.
func TestCreateMcpAuthProviderLeavesAPendingSignInUsableWhenTheRefreshFails(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	// A step-up keeps the tokens while the user is in the browser.
	stepUp := options
	stepUp.Challenge = &oauth.OAuthChallenge{Error: "insufficient_scope", Scope: "more"}
	authorization := startRedirect(t, stepUp)
	pending := loadState(t, options.Store)

	server.RevokeRefreshTokens()
	provider := authProviderFor(options, oauth.McpOAuthSettings{})
	expectAuthorizationRequired(t, provider.OnUnauthorized(t.Context(), rejectedRequest(t, server, "access-1", nil)))

	state := loadState(t, options.Store)
	if state.OAuthState != pending.OAuthState || state.CodeVerifier != pending.CodeVerifier {
		t.Fatalf("pending sign-in = %q/%q, want %q/%q", state.OAuthState, state.CodeVerifier, pending.OAuthState, pending.CodeVerifier)
	}
	if got := len(server.Registrations()); got != 1 {
		t.Fatalf("registrations = %d, want 1", got)
	}
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	if err := complete(t.Context(), stepUp, authorization, response); err != nil {
		t.Fatal(err)
	}
	if got := loadState(t, options.Store).Tokens.AccessToken; got != "access-2" {
		t.Fatalf("access token = %q, want access-2", got)
	}
}

// mutation-checked: a refresh that may register a client (no RefreshOnly) fails it.
func TestCreateMcpAuthProviderDoesNotRegisterAClientToRefresh(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true, issParameter: true})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.RedirectURL = loopbackRedirect
	withDocuments := options
	withDocuments.ClientMetadataDocuments = &oauth.McpClientMetadataDocuments{BaseURL: "https://app.example/oauth", ListsRedirectURL: func(string) bool { return true }}
	signIn(t, withDocuments)
	if got := loadState(t, options.Store).ClientInformation; got != nil {
		t.Fatalf("stored client = %+v, want none", got)
	}

	// Without the documents, the tokens have no client to refresh them with.
	provider := authProviderFor(options, oauth.McpOAuthSettings{})
	expectAuthorizationRequired(t, provider.OnUnauthorized(t.Context(), rejectedRequest(t, server, "access-1", nil)))
	if got := server.Registrations(); len(got) != 0 {
		t.Fatalf("registrations = %v, want none", got)
	}
	if got := len(server.Authorizations()); got != 1 {
		t.Fatalf("authorizations = %d, want 1", got)
	}
}

// pendingSignIn is signed in, with a step-up sign-in waiting for the browser, which keeps the tokens meanwhile.
type pendingSignInFixture struct {
	server   *authorizationServer
	store    oauth.McpOAuthServerStore
	pending  *oauth.McpOAuthState
	provider oauth.McpAuthProvider
}

func pendingSignIn(t *testing.T) pendingSignInFixture {
	t.Helper()
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	stepUp := options
	stepUp.Challenge = &oauth.OAuthChallenge{Error: "insufficient_scope", Scope: "more"}
	startRedirect(t, stepUp)
	return pendingSignInFixture{server: server, store: options.Store, pending: loadState(t, options.Store), provider: authProviderFor(options, oauth.McpOAuthSettings{})}
}

func (f pendingSignInFixture) expectPendingKept(t *testing.T) *oauth.McpOAuthState {
	t.Helper()
	state := loadState(t, f.store)
	if state.OAuthState != f.pending.OAuthState || state.CodeVerifier != f.pending.CodeVerifier {
		t.Fatalf("pending sign-in = %q/%q, want %q/%q", state.OAuthState, state.CodeVerifier, f.pending.OAuthState, f.pending.CodeVerifier)
	}
	return state
}

// mutation-checked: invalidating all credentials after a rejected refresh, or not invalidating the client, fails it.
func TestCreateMcpAuthProviderDropsARejectedClientAndItsTokensButKeepsThePendingSignIn(t *testing.T) {
	f := pendingSignIn(t)
	f.server.FailNextRefresh("invalid_client", nil)
	expectAuthorizationRequired(t, f.provider.OnUnauthorized(t.Context(), rejectedRequest(t, f.server, "access-1", nil)))
	state := f.expectPendingKept(t)
	if state.ClientInformation != nil || state.Tokens != nil {
		t.Fatalf("state = %+v, want no client and no tokens", state)
	}
	if got := len(f.server.Registrations()); got != 1 {
		t.Fatalf("registrations = %d, want 1", got)
	}
}

// mutation-checked: invalidating the client without comparing it with the one the failed flow used fails it.
func TestCreateMcpAuthProviderKeepsAClientStoredWhileTheRejectedRefreshRan(t *testing.T) {
	f := pendingSignIn(t)
	// Another sign-in completes with a new client while the server rejects the old one.
	f.server.FailNextRefresh("invalid_client", func() {
		state, err := f.store.Load(context.Background())
		if err != nil || state == nil {
			t.Errorf("load = %v, %v", state, err)
			return
		}
		next := state.Clone()
		next.ClientInformation = &oauth.OAuthClientInformation{ClientID: "client-new", OAuthClientMetadata: oauth.OAuthClientMetadata{RedirectURIs: []string{route}}}
		if err := f.store.Save(context.Background(), next); err != nil {
			t.Errorf("save: %v", err)
		}
	})
	if err := f.provider.OnUnauthorized(t.Context(), rejectedRequest(t, f.server, "access-1", nil)); err != nil {
		t.Fatal(err)
	}
	if got := loadState(t, f.store).ClientInformation.ClientID; got != "client-new" {
		t.Fatalf("stored client = %q, want client-new", got)
	}
	requests := f.server.TokenRequests()
	if got := requests[len(requests)-1].Get("client_id"); got != "client-new" {
		t.Fatalf("last token request client_id = %q, want client-new", got)
	}
	expectToken(t, f.provider, "access-2")
}

// mutation-checked: a refresh that may prepare an authorization request (no RefreshOnly) fails it.
func TestCreateMcpAuthProviderKeepsTheTokensAndThePendingSignInAfterAServerError(t *testing.T) {
	f := pendingSignIn(t)
	f.server.FailNextRefresh("server_error", nil)
	expectAuthorizationRequired(t, f.provider.OnUnauthorized(t.Context(), rejectedRequest(t, f.server, "access-1", nil)))
	state := f.expectPendingKept(t)
	if state.Tokens == nil || state.Tokens.AccessToken != "access-1" {
		t.Fatalf("tokens = %+v, want access-1", state.Tokens)
	}
}

type offlineFetch struct{}

func (offlineFetch) Do(request *http.Request) (*http.Response, error) {
	if strings.HasSuffix(request.URL.Path, "/token") {
		return nil, errors.New("fetch failed")
	}
	return http.DefaultClient.Do(request) //nolint:gosec // the request goes to the loopback test server
}

// mutation-checked: a refresh that may prepare an authorization request (no RefreshOnly) fails it.
func TestCreateMcpAuthProviderKeepsTheTokensAndThePendingSignInAfterANetworkError(t *testing.T) {
	f := pendingSignIn(t)
	expectAuthorizationRequired(t, f.provider.OnUnauthorized(t.Context(), rejectedRequest(t, f.server, "access-1", offlineFetch{})))
	state := f.expectPendingKept(t)
	if state.Tokens == nil || state.Tokens.AccessToken != "access-1" {
		t.Fatalf("tokens = %+v, want access-1", state.Tokens)
	}
}
