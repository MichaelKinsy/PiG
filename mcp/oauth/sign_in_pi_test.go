package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/test/sign-in.test.ts (Pi bb266bf35). Go mechanic: Pi's `signal` is the context of each call.

// Callback routes of the application on two origins, like p2 through a relay and on its public URL.
const (
	routeURL      = "https://app.example/mcp/callback"
	otherRouteURL = "https://relay.example/mcp/callback"
	// loopbackURL is a loopback redirect URI; tests read the redirect from the authorization server instead of following it.
	loopbackURL = "http://127.0.0.1:1/callback"
)

// loopbackDocuments are documents that list `/callback` on 127.0.0.1, like pi's on pi.dev.
func loopbackDocuments() *oauth.McpClientMetadataDocuments {
	return &oauth.McpClientMetadataDocuments{BaseURL: "https://app.example/oauth", ListsRedirectURL: func(redirectURL string) bool {
		u, err := url.Parse(redirectURL)
		return err == nil && u.Hostname() == "127.0.0.1" && u.Path == "/callback"
	}}
}

func signInOptions(server *authServer, settings oauth.McpOAuthSettings) oauth.McpSignInOptions {
	return oauth.McpSignInOptions{
		McpOAuthClientOptions: oauth.McpOAuthClientOptions{ServerURL: server.serverURL, Store: &memoryStore{}, Settings: settings, ClientName: "app"},
		RedirectURL:           routeURL,
	}
}

func startRedirect(t *testing.T, options oauth.McpSignInOptions) oauth.McpSignInRedirect {
	t.Helper()
	started, err := oauth.StartMcpSignIn(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != oauth.McpSignInKindRedirect || started.Redirect().Kind != oauth.McpSignInKindRedirect {
		t.Fatalf("Expected the browser step, got kind %q", started.Kind)
	}
	return started.Redirect()
}

// The browser step is plain data an application persists: Pi's JSON names, with `kind`.
func TestMcpSignInStartIsPlainDataWithAKind(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	started, err := oauth.StartMcpSignIn(t.Context(), signInOptions(server, oauth.McpOAuthSettings{}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["kind"] != "redirect" || got["authorizationUrl"] != started.AuthorizationURL || got["redirectUrl"] != started.RedirectURL || got["state"] != started.State {
		t.Fatalf("json = %s", data)
	}
	authorized, err := json.Marshal(oauth.McpSignInStart{Kind: oauth.McpSignInKindAuthorized})
	if err != nil || string(authorized) != `{"kind":"authorized"}` {
		t.Fatalf("authorized json = %s, %v", authorized, err)
	}
}

// signIn starts, follows the browser step, and completes.
func signIn(t *testing.T, options oauth.McpSignInOptions) oauth.McpSignInRedirect {
	t.Helper()
	authorization := startRedirect(t, options)
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	if err := oauth.CompleteMcpSignIn(t.Context(), oauth.McpCompleteSignInOptions{McpSignInOptions: options, Authorization: authorization, Response: response}); err != nil {
		t.Fatal(err)
	}
	return authorization
}

func loadState(t *testing.T, options oauth.McpSignInOptions) *oauth.McpOAuthState {
	t.Helper()
	state, err := options.Store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func queryOf(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func expectError(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %v, want one containing %q", err, contains)
	}
}

func expectCancelled(t *testing.T, err error) {
	t.Helper()
	if _, ok := errors.AsType[*oauth.McpSignInCancelledError](err); !ok {
		t.Fatalf("error = %v, want McpSignInCancelledError", err)
	}
}

func expectGrants(t *testing.T, server *authServer, want ...string) {
	t.Helper()
	if got := server.grantTypes(); !slices.Equal(got, want) {
		t.Fatalf("grant types = %q, want %q", got, want)
	}
}

func callbackIDOf(serverURL string) string {
	sum := sha256.Sum256([]byte(serverURL))
	return base64.RawURLEncoding.EncodeToString(sum[:9])
}

func TestSignInCompletesInASecondCallWithTheSameStoreAndAPersistedRedirect(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{Scope: "issues:read"})
	authorization := startRedirect(t, options)
	if authorization.RedirectURL != routeURL || authorization.Scope != "issues:read" {
		t.Fatalf("authorization = %+v", authorization)
	}
	if got := queryOf(t, authorization.AuthorizationURL).Get("state"); got != authorization.State {
		t.Fatalf("state = %q, want %q", got, authorization.State)
	}
	registrations := server.registered()
	if len(registrations) != 1 || registrations[0]["client_name"] != "app" || !reflect.DeepEqual(registrations[0]["redirect_uris"], []any{routeURL}) || registrations[0]["application_type"] != "web" {
		t.Fatalf("registrations = %v", registrations)
	}

	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	if err := oauth.CompleteMcpSignIn(t.Context(), oauth.McpCompleteSignInOptions{McpSignInOptions: options, Authorization: authorization, Response: response}); err != nil {
		t.Fatal(err)
	}
	tokens := loadState(t, options).Tokens
	if tokens == nil || tokens.AccessToken != "access-1" || tokens.Scope != "issues:read" {
		t.Fatalf("tokens = %+v", tokens)
	}
	requests := server.tokens()
	if requests[0].Get("redirect_uri") != routeURL || requests[0].Get("client_id") != "client-1" {
		t.Fatalf("token request = %v", requests[0])
	}
}

func TestSignInRefreshesTheStoredTokensInsteadOfRedirecting(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)

	started, err := oauth.StartMcpSignIn(t.Context(), options)
	if err != nil || started.Kind != oauth.McpSignInKindAuthorized {
		t.Fatalf("start = %+v, %v", started, err)
	}
	if len(server.authorized()) != 1 || server.tokens()[1].Get("grant_type") != "refresh_token" {
		t.Fatalf("authorizations = %d, token requests = %v", len(server.authorized()), server.tokens())
	}
	if got := loadState(t, options).Tokens.AccessToken; got != "access-2" {
		t.Fatalf("access token = %q", got)
	}
}

func TestSignInRefreshesUnderTheStoresRefreshLock(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	store := &recordingStore{}
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store = store
	signIn(t, options)

	started, err := oauth.StartMcpSignIn(t.Context(), options)
	if err != nil || started.Kind != oauth.McpSignInKindAuthorized {
		t.Fatalf("start = %+v, %v", started, err)
	}
	if got := store.accessTokenSaves("access-2"); !reflect.DeepEqual(got, []recordedSave{{locked: true, accessToken: "access-2"}}) {
		t.Fatalf("saves = %+v", got)
	}
}

func TestSignInRefusesToCompleteWithAnotherRedirectURIThanTheAuthorizationRequest(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.RedirectURL, options.ClientMetadataDocuments = loopbackURL, loopbackDocuments()
	authorization := startRedirect(t, options)
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	// Another loopback port than the one passed to start: the server-specific variant differs too.
	wrong := options
	wrong.RedirectURL = "http://127.0.0.1:2/callback"
	err := oauth.CompleteMcpSignIn(t.Context(), oauth.McpCompleteSignInOptions{McpSignInOptions: wrong, Authorization: authorization, Response: response})
	expectError(t, err, "but the authorization request used "+authorization.RedirectURL)
	if len(server.tokens()) != 0 {
		t.Fatalf("token requests = %v", server.tokens())
	}
}

func TestSignInGivesEverySignInAFreshStateAndRefusesToCompleteASupersededOne(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	first := startRedirect(t, options)
	firstResponse := routeResponse(t, authorizeInBrowser(t, first.AuthorizationURL))
	second := startRedirect(t, options)
	if second.State == first.State {
		t.Fatal("the second sign-in reused the state")
	}
	expectError(t, oauth.CompleteMcpSignIn(t.Context(), oauth.McpCompleteSignInOptions{McpSignInOptions: options, Authorization: first, Response: firstResponse}), "Another sign-in to this MCP server started since")
	if len(server.tokens()) != 0 {
		t.Fatalf("token requests = %v", server.tokens())
	}
}

func TestSignInRefusesAResponseWithAnotherState(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	authorization := startRedirect(t, options)
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	response.State = "other"
	expectError(t, oauth.CompleteMcpSignIn(t.Context(), oauth.McpCompleteSignInOptions{McpSignInOptions: options, Authorization: authorization, Response: response}), "belongs to a different sign-in")
	if len(server.tokens()) != 0 {
		t.Fatalf("token requests = %v", server.tokens())
	}
}

func TestStoredClientRefreshesFromAnotherRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	other := options
	other.RedirectURL = otherRouteURL
	started, err := oauth.StartMcpSignIn(t.Context(), other)
	if err != nil || started.Kind != oauth.McpSignInKindAuthorized {
		t.Fatalf("start = %+v, %v", started, err)
	}
	if len(server.registered()) != 1 || server.tokens()[1].Get("client_id") != "client-1" || loadState(t, options).ClientInformation.ClientID != "client-1" {
		t.Fatal("the refresh did not use the stored client")
	}
}

func TestStoredClientKeepsTheClientForABrowserStepOnItsRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	server.revokeRefreshTokens()
	authorization := startRedirect(t, options)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "client-1" || len(server.registered()) != 1 {
		t.Fatalf("client_id = %q, registrations = %d", got, len(server.registered()))
	}
}

func TestStoredClientIsDroppedWithItsTokensForABrowserStepOnAnotherRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	server.revokeRefreshTokens()
	other := options
	other.RedirectURL = otherRouteURL
	authorization := startRedirect(t, other)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "client-2" {
		t.Fatalf("client_id = %q", got)
	}
	if got := server.registered()[1]["redirect_uris"]; !reflect.DeepEqual(got, []any{otherRouteURL}) {
		t.Fatalf("redirect_uris = %v", got)
	}
	state := loadState(t, options)
	if state.ClientInformation.ClientID != "client-2" || state.Tokens != nil {
		t.Fatalf("state = %+v", state)
	}
}

func TestStoredClientRegisteredByDCRIsNotRefreshedWhenCimdReplacesIt(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true, issParameter: true})
	options := signInOptions(server, oauth.McpOAuthSettings{ClientRegistration: "dcr"})
	options.RedirectURL, options.ClientMetadataDocuments = loopbackURL, loopbackDocuments()
	signIn(t, options)
	if got := loadState(t, options).ClientInformation.ClientID; got != "client-1" {
		t.Fatalf("client_id = %q", got)
	}
	cimd := options
	cimd.Settings = oauth.McpOAuthSettings{ClientRegistration: "cimd"}
	authorization := startRedirect(t, cimd)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "https://app.example/oauth/client.json" {
		t.Fatalf("client_id = %q", got)
	}
	expectGrants(t, server, "authorization_code")
	state := loadState(t, options)
	if state.ClientInformation != nil || state.Tokens != nil {
		t.Fatalf("state = %+v", state)
	}
}

func TestStoredClientAsksForTheGrantedScopePlusTheChallengedOneOnAStepUpWithoutRefreshing(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Challenge = &oauth.OAuthChallenge{Scope: "issues:read"}
	signIn(t, options)
	if got := loadState(t, options).Tokens.Scope; got != "issues:read" {
		t.Fatalf("scope = %q", got)
	}
	// The challenge lists only the missing scope; requesting just that would lose issues:read.
	stepUp := options
	stepUp.Challenge = &oauth.OAuthChallenge{Error: "insufficient_scope", Scope: "issues:write"}
	authorization := signIn(t, stepUp)
	if authorization.Scope != "issues:read issues:write" {
		t.Fatalf("scope = %q", authorization.Scope)
	}
	expectGrants(t, server, "authorization_code", "authorization_code")
	if got := loadState(t, options).Tokens.Scope; got != "issues:read issues:write" {
		t.Fatalf("stored scope = %q", got)
	}
}

func loopbackOptions(server *authServer, settings oauth.McpOAuthSettings) oauth.McpSignInOptions {
	options := signInOptions(server, settings)
	options.RedirectURL, options.ClientMetadataDocuments = loopbackURL, loopbackDocuments()
	return options
}

func TestMetadataDocumentsUsesADocumentWhenTheServerSupportsThemAndTheDocumentsListTheRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true, issParameter: true})
	options := loopbackOptions(server, oauth.McpOAuthSettings{})
	authorization := signIn(t, options)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "https://app.example/oauth/client.json" {
		t.Fatalf("client_id = %q", got)
	}
	if authorization.RedirectURL != loopbackURL || len(server.registered()) != 0 {
		t.Fatalf("redirect = %q, registrations = %v", authorization.RedirectURL, server.registered())
	}
	if got := server.tokens()[0].Get("client_id"); got != "https://app.example/oauth/client.json" {
		t.Fatalf("token client_id = %q", got)
	}
	if loadState(t, options).ClientInformation != nil {
		t.Fatal("a document client was stored")
	}
}

func TestMetadataDocumentsUsesADocumentAndRedirectURISpecificToTheMCPServerWithoutTheIssParameter(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true})
	authorization := signIn(t, loopbackOptions(server, oauth.McpOAuthSettings{}))
	id := callbackIDOf(server.serverURL)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "https://app.example/oauth/"+id+"/client.json" {
		t.Fatalf("client_id = %q", got)
	}
	if authorization.RedirectURL != loopbackURL+"/"+id || server.tokens()[0].Get("redirect_uri") != loopbackURL+"/"+id {
		t.Fatalf("redirect = %q, token redirect = %q", authorization.RedirectURL, server.tokens()[0].Get("redirect_uri"))
	}
}

func TestMetadataDocumentsAppendsTheServerSpecificPathWithoutDoublingATrailingSlash(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true})
	options := loopbackOptions(server, oauth.McpOAuthSettings{})
	options.RedirectURL = loopbackURL + "/"
	options.ClientMetadataDocuments = &oauth.McpClientMetadataDocuments{BaseURL: "https://app.example/oauth", ListsRedirectURL: func(string) bool { return true }}
	authorization := startRedirect(t, options)
	if want := loopbackURL + "/" + callbackIDOf(server.serverURL); authorization.RedirectURL != want {
		t.Fatalf("redirect = %q, want %q", authorization.RedirectURL, want)
	}
}

func TestMetadataDocumentsRegistersDynamicallyWhenTheServerDoesNotSupportDocuments(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{issParameter: true})
	signIn(t, loopbackOptions(server, oauth.McpOAuthSettings{}))
	if len(server.registered()) != 1 || server.authorized()[0].Get("client_id") != "client-1" {
		t.Fatalf("registrations = %v", server.registered())
	}
}

func TestMetadataDocumentsRegistersDynamicallyWhenTheDocumentsDoNotListTheRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true, issParameter: true})
	options := loopbackOptions(server, oauth.McpOAuthSettings{})
	options.RedirectURL = routeURL
	signIn(t, options)
	if len(server.registered()) != 1 {
		t.Fatalf("registrations = %v", server.registered())
	}
}

func TestMetadataDocumentsRegistersDynamicallyWithoutDocumentsOrWithAClientName(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true, issParameter: true})
	without := loopbackOptions(server, oauth.McpOAuthSettings{})
	without.ClientMetadataDocuments = nil
	signIn(t, without)
	signIn(t, loopbackOptions(server, oauth.McpOAuthSettings{ClientName: "Other"}))
	registrations := server.registered()
	if len(registrations) != 2 || registrations[0]["client_name"] != "app" || registrations[1]["client_name"] != "Other" {
		t.Fatalf("registrations = %v", registrations)
	}
}

func TestMetadataDocumentsFailWithCimdWhenTheServerDoesNotSupportDocuments(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{issParameter: true})
	_, err := oauth.StartMcpSignIn(t.Context(), loopbackOptions(server, oauth.McpOAuthSettings{ClientRegistration: "cimd"}))
	expectError(t, err, "does not support Client ID Metadata Documents")
	if len(server.registered()) != 0 {
		t.Fatalf("registrations = %v", server.registered())
	}
}

func TestMetadataDocumentsFailWithCimdWhenTheDocumentsDoNotListTheRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{cimd: true, issParameter: true})
	options := loopbackOptions(server, oauth.McpOAuthSettings{ClientRegistration: "cimd"})
	options.RedirectURL = routeURL
	_, err := oauth.StartMcpSignIn(t.Context(), options)
	expectError(t, err, "needs Client ID Metadata Documents that list the redirect URI")
	if len(server.registered()) != 0 {
		t.Fatalf("registrations = %v", server.registered())
	}
}

func TestCancellationCancelsAStartWhoseRequestDoesNotAnswer(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	server.stallPath("/register")
	ctx, cancel := context.WithTimeout(t.Context(), 100_000_000)
	defer cancel()
	_, err := oauth.StartMcpSignIn(ctx, signInOptions(server, oauth.McpOAuthSettings{}))
	expectCancelled(t, err)
}

func TestCancellationCancelsACompletionWhoseTokenRequestDoesNotAnswer(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	authorization := startRedirect(t, options)
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	server.stallPath("/token")
	ctx, cancel := context.WithTimeout(t.Context(), 100_000_000)
	defer cancel()
	expectCancelled(t, oauth.CompleteMcpSignIn(ctx, oauth.McpCompleteSignInOptions{McpSignInOptions: options, Authorization: authorization, Response: response}))
}

func TestCancellationFailsAtOnceWithAnEndedContext(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := oauth.StartMcpSignIn(ctx, signInOptions(server, oauth.McpOAuthSettings{}))
	expectCancelled(t, err)
	if len(server.registered()) != 0 {
		t.Fatalf("registrations = %v", server.registered())
	}
}
func TestMetadataDocumentsListingOnlyAnHTTPSRouteRefreshTheTokensOfADocument(t *testing.T) {
	for _, registration := range []string{"auto", "cimd"} {
		t.Run(registration, func(t *testing.T) {
			server := startAuthorizationServer(t, authServerOptions{cimd: true, issParameter: true})
			options := signInOptions(server, oauth.McpOAuthSettings{ClientRegistration: registration})
			options.ClientMetadataDocuments = &oauth.McpClientMetadataDocuments{BaseURL: "https://app.example/oauth", ListsRedirectURL: func(redirectURL string) bool { return redirectURL == routeURL }}
			authorization := signIn(t, options)
			if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "https://app.example/oauth/client.json" {
				t.Fatalf("client_id = %q", got)
			}
			if loadState(t, options).ClientInformation != nil {
				t.Fatal("a document client was stored")
			}
			started, err := oauth.StartMcpSignIn(t.Context(), options)
			if err != nil || started.Kind != oauth.McpSignInKindAuthorized {
				t.Fatalf("start = %+v, %v", started, err)
			}
			refresh := server.tokens()[1]
			if len(server.authorized()) != 1 || refresh.Get("grant_type") != "refresh_token" || refresh.Get("client_id") != "https://app.example/oauth/client.json" {
				t.Fatalf("refresh = %v", refresh)
			}
			if got := loadState(t, options).Tokens.AccessToken; got != "access-2" {
				t.Fatalf("access token = %q", got)
			}
		})
	}
}

// Pi 797e122cb: the lock failures and the cancellation while waiting for the refresh lock.

func TestSignInGoesToTheBrowserStepWhenAnotherProcessHoldsTheRefreshLockForTooLong(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	store := &recordingStore{}
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store = store
	signIn(t, options)
	store.lockError = oauth.NewMcpRefreshLockBusyError()
	started, err := oauth.StartMcpSignIn(t.Context(), options)
	if err != nil || started.Kind == oauth.McpSignInKindAuthorized {
		t.Fatalf("start = %+v, %v", started, err)
	}
	expectGrants(t, server, "authorization_code")
}

func TestSignInFailsWhenTheRefreshLockFailsForAnotherReason(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	lockError := errors.New("EACCES: permission denied")
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store = &recordingStore{lockError: lockError}
	if _, err := oauth.StartMcpSignIn(t.Context(), options); !errors.Is(err, lockError) {
		t.Fatalf("start error = %v, want %v", err, lockError)
	}
	if len(server.registered()) != 0 || len(server.authorized()) != 0 {
		t.Fatalf("registrations = %v, authorizations = %d", server.registered(), len(server.authorized()))
	}
}

func TestCancellationCancelsAtOnceWhileAnotherProcessHoldsTheRefreshLockAndRefreshesNothing(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	store := &recordingStore{}
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store = store
	signIn(t, options)

	release := store.hold()
	ctx, cancel := context.WithTimeout(t.Context(), 100_000_000)
	defer cancel()
	_, err := oauth.StartMcpSignIn(ctx, options)
	expectCancelled(t, err)

	// The lock arrives after the cancellation: nothing runs under it, and it is released.
	release()
	store.waitRuns(t)
	if store.isLocked() {
		t.Fatal("the lock was not released")
	}
	expectGrants(t, server, "authorization_code")
	if len(server.authorized()) != 1 {
		t.Fatalf("authorizations = %d", len(server.authorized()))
	}
}
