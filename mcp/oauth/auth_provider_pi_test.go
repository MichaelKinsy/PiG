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

// Ports packages/mcp/test/auth-provider.test.ts (Pi bb266bf35).

// rejectedToken is what a connection's request reports after its token was rejected.
func rejectedToken(server *authServer, token string, fetch mcp.McpFetch) mcp.UnauthorizedContext {
	serverURL, _ := url.Parse(server.serverURL)
	return mcp.UnauthorizedContext{Response: &http.Response{StatusCode: 401, Header: http.Header{}, Body: http.NoBody}, ServerURL: serverURL, Fetch: fetch, Token: token}
}

func newProvider(options oauth.McpSignInOptions) oauth.McpAuthProvider {
	return oauth.CreateMcpAuthProvider(oauth.McpAuthProviderOptions{
		ServerURL: options.ServerURL, Store: options.Store, ClientName: options.ClientName, ClientMetadataDocuments: options.ClientMetadataDocuments,
		Settings: func() (oauth.McpOAuthSettings, error) { return options.Settings, nil },
	})
}

func expectAuthorizationRequired(t *testing.T, err error) {
	t.Helper()
	if _, ok := errors.AsType[*oauth.McpOAuthAuthorizationRequiredError](err); !ok {
		t.Fatalf("error = %v, want McpOAuthAuthorizationRequiredError", err)
	}
}

func expectToken(t *testing.T, provider oauth.McpAuthProvider, want string) {
	t.Helper()
	got, err := provider.Token(t.Context())
	if err != nil || got != want {
		t.Fatalf("token = %q, %v, want %q", got, err, want)
	}
}

func TestAuthProviderRefreshesARejectedToken(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	provider := newProvider(options)
	if err := provider.OnUnauthorized(t.Context(), rejectedToken(server, "access-1", nil)); err != nil {
		t.Fatal(err)
	}
	expectToken(t, provider, "access-2")
	if got := server.tokens()[1].Get("client_id"); got != "client-1" {
		t.Fatalf("client_id = %q", got)
	}
}

func TestAuthProviderLeavesAPendingSignInUsableWhenTheRefreshFails(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	// A step-up keeps the tokens while the user is in the browser.
	stepUp := options
	stepUp.Challenge = &oauth.OAuthChallenge{Error: "insufficient_scope", Scope: "more"}
	authorization := startRedirect(t, stepUp)
	pending := loadState(t, options)

	server.revokeRefreshTokens()
	expectAuthorizationRequired(t, newProvider(options).OnUnauthorized(t.Context(), rejectedToken(server, "access-1", nil)))
	state := loadState(t, options)
	if state.OAuthState != pending.OAuthState || state.CodeVerifier != pending.CodeVerifier || len(server.registered()) != 1 {
		t.Fatalf("state = %+v, pending = %+v, registrations = %d", state, pending, len(server.registered()))
	}
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	if err := oauth.CompleteMcpSignIn(t.Context(), oauth.McpCompleteSignInOptions{McpSignInOptions: stepUp, Authorization: authorization, Response: response}); err != nil {
		t.Fatal(err)
	}
	if got := loadState(t, options).Tokens.AccessToken; got != "access-2" {
		t.Fatalf("access token = %q", got)
	}
}

func TestAuthProviderDoesNotRegisterAClientToRefresh(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true, issParameter: true})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.RedirectURL = loopbackURL
	options.ClientMetadataDocuments = &oauth.McpClientMetadataDocuments{BaseURL: "https://app.example/oauth", ListsRedirectURL: func(string) bool { return true }}
	signIn(t, options)
	if loadState(t, options).ClientInformation != nil {
		t.Fatal("a document client was stored")
	}
	// Without the documents, the tokens have no client to refresh them with.
	withoutDocuments := options
	withoutDocuments.ClientMetadataDocuments = nil
	expectAuthorizationRequired(t, newProvider(withoutDocuments).OnUnauthorized(t.Context(), rejectedToken(server, "access-1", nil)))
	if len(server.registered()) != 0 || len(server.authorized()) != 1 {
		t.Fatalf("registrations = %v, authorizations = %d", server.registered(), len(server.authorized()))
	}
}

// pendingSignIn is signed in, with a step-up sign-in waiting for the browser, which keeps the tokens meanwhile.
func pendingSignIn(t *testing.T) (*authServer, oauth.McpSignInOptions, *oauth.McpOAuthState, oauth.McpAuthProvider) {
	t.Helper()
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	stepUp := options
	stepUp.Challenge = &oauth.OAuthChallenge{Error: "insufficient_scope", Scope: "more"}
	startRedirect(t, stepUp)
	return server, options, loadState(t, options), newProvider(options)
}

func TestAuthProviderDropsARejectedClientAndItsTokensButKeepsThePendingSignIn(t *testing.T) {
	server, options, pending, provider := pendingSignIn(t)
	server.failNextRefresh("invalid_client", nil)
	expectAuthorizationRequired(t, provider.OnUnauthorized(t.Context(), rejectedToken(server, "access-1", nil)))
	state := loadState(t, options)
	if state.OAuthState != pending.OAuthState || state.CodeVerifier != pending.CodeVerifier || state.ClientInformation != nil || state.Tokens != nil || len(server.registered()) != 1 {
		t.Fatalf("state = %+v, registrations = %d", state, len(server.registered()))
	}
}

func TestAuthProviderKeepsAClientStoredWhileTheRejectedRefreshRan(t *testing.T) {
	server, options, _, provider := pendingSignIn(t)
	// Another sign-in completes with a new client while the server rejects the old one.
	server.failNextRefresh("invalid_client", func() {
		state, _ := options.Store.Load(context.Background())
		if state != nil {
			next := state.Clone()
			replacement := oauth.OAuthClientInformationMixed{ClientID: "client-new"}
			replacement.RedirectURIs = []string{routeURL}
			next.ClientInformation = &replacement
			_ = options.Store.Save(context.Background(), next)
		}
	})
	if err := provider.OnUnauthorized(t.Context(), rejectedToken(server, "access-1", nil)); err != nil {
		t.Fatal(err)
	}
	if got := loadState(t, options).ClientInformation.ClientID; got != "client-new" {
		t.Fatalf("client_id = %q", got)
	}
	requests := server.tokens()
	if got := requests[len(requests)-1].Get("client_id"); got != "client-new" {
		t.Fatalf("last token request client_id = %q", got)
	}
	expectToken(t, provider, "access-2")
}

func TestAuthProviderKeepsTheTokensAndThePendingSignInAfterAServerError(t *testing.T) {
	server, options, pending, provider := pendingSignIn(t)
	server.failNextRefresh("server_error", nil)
	expectAuthorizationRequired(t, provider.OnUnauthorized(t.Context(), rejectedToken(server, "access-1", nil)))
	state := loadState(t, options)
	if state.OAuthState != pending.OAuthState || state.CodeVerifier != pending.CodeVerifier || state.Tokens.AccessToken != "access-1" {
		t.Fatalf("state = %+v", state)
	}
}

type offlineFetch struct{}

func (offlineFetch) Do(request *http.Request) (*http.Response, error) {
	if strings.HasSuffix(request.URL.Path, "/token") {
		return nil, errors.New("fetch failed")
	}
	return http.DefaultClient.Do(request)
}

func TestAuthProviderKeepsTheTokensAndThePendingSignInAfterANetworkError(t *testing.T) {
	server, options, pending, provider := pendingSignIn(t)
	expectAuthorizationRequired(t, provider.OnUnauthorized(t.Context(), rejectedToken(server, "access-1", offlineFetch{})))
	state := loadState(t, options)
	if state.OAuthState != pending.OAuthState || state.CodeVerifier != pending.CodeVerifier || state.Tokens.AccessToken != "access-1" {
		t.Fatalf("state = %+v", state)
	}
}

func TestAuthProviderRefreshesTheTokensOfADocumentThatListsOnlyTheRedirectURIPassed(t *testing.T) {
	for _, registration := range []string{"auto", "cimd"} {
		t.Run(registration, func(t *testing.T) {
			server := startAuthorizationServer(t, authServerOptions{cimd: true, issParameter: true})
			options := signInOptions(server, oauth.McpOAuthSettings{ClientRegistration: registration})
			options.ClientMetadataDocuments = &oauth.McpClientMetadataDocuments{BaseURL: "https://app.example/oauth", ListsRedirectURL: func(redirectURL string) bool { return redirectURL == routeURL }}
			signIn(t, options)
			if loadState(t, options).ClientInformation != nil {
				t.Fatal("a document client was stored")
			}
			provider := oauth.CreateMcpAuthProvider(oauth.McpAuthProviderOptions{
				ServerURL: options.ServerURL, Store: options.Store, ClientName: options.ClientName, ClientMetadataDocuments: options.ClientMetadataDocuments,
				Settings: func() (oauth.McpOAuthSettings, error) { return options.Settings, nil }, RedirectURL: routeURL,
			})
			if err := provider.OnUnauthorized(t.Context(), rejectedToken(server, "access-1", nil)); err != nil {
				t.Fatal(err)
			}
			expectToken(t, provider, "access-2")
			if got := server.tokens()[1].Get("client_id"); got != "https://app.example/oauth/client.json" || len(server.authorized()) != 1 {
				t.Fatalf("client_id = %q, authorizations = %d", got, len(server.authorized()))
			}
		})
	}
}
