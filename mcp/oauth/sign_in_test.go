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
	"time"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/test/sign-in.test.ts.
//
// pig mapping: a JavaScript `signal` is the context; `toMatchObject` reads the listed members of the recorded value.

// Callback routes of the application on two origins, like p2 through a relay and on its public URL.
const (
	route      = "https://app.example/mcp/callback"
	otherRoute = "https://relay.example/mcp/callback"
	// loopbackRedirect is a loopback redirect URI; tests read the redirect from the authorization server instead of
	// following it.
	loopbackRedirect = "http://127.0.0.1:1/callback"
)

// documents list `/callback` on 127.0.0.1, like pi's on pi.dev.
var documents = &oauth.McpClientMetadataDocuments{
	BaseURL: "https://app.example/oauth",
	ListsRedirectURL: func(redirectURL string) bool {
		u, err := url.Parse(redirectURL)
		return err == nil && u.Hostname() == "127.0.0.1" && u.Path == "/callback"
	},
}

func signInOptions(server *authorizationServer, settings oauth.McpOAuthSettings) oauth.McpSignInOptions {
	return oauth.McpSignInOptions{
		McpOAuthClientOptions: oauth.McpOAuthClientOptions{ServerURL: server.ServerURL, Store: memoryStore(), Settings: settings, ClientName: "app"},
		RedirectURL:           route,
	}
}

func startRedirect(t *testing.T, options oauth.McpSignInOptions) oauth.McpSignInRedirect {
	t.Helper()
	started, err := oauth.StartMcpSignIn(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != "redirect" {
		t.Fatalf("started = %+v, want the browser step", started)
	}
	return *started.Redirect
}

// routeResponse is the response a callback route reads from the query of the redirect.
func routeResponse(t *testing.T, location string) oauth.McpAuthorizationResponse {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	return oauth.McpAuthorizationResponse{Code: query.Get("code"), State: query.Get("state"), Iss: query.Get("iss")}
}

func complete(ctx context.Context, options oauth.McpSignInOptions, authorization oauth.McpSignInRedirect, response oauth.McpAuthorizationResponse) error {
	return oauth.CompleteMcpSignIn(ctx, oauth.McpSignInCompletion{McpSignInOptions: options, Authorization: authorization, Response: response})
}

// signIn starts, follows the browser step, and completes.
func signIn(t *testing.T, options oauth.McpSignInOptions) oauth.McpSignInRedirect {
	t.Helper()
	authorization := startRedirect(t, options)
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	if err := complete(t.Context(), options, authorization, response); err != nil {
		t.Fatal(err)
	}
	return authorization
}

func loadState(t *testing.T, store oauth.McpOAuthStateStore) *oauth.McpOAuthState {
	t.Helper()
	state, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func queryOf(t *testing.T, rawURL string) url.Values {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func expectError(t *testing.T, err error, substring string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substring) {
		t.Fatalf("err = %v, want one containing %q", err, substring)
	}
}

func expectCancelled(t *testing.T, err error) {
	t.Helper()
	if _, ok := errors.AsType[*oauth.McpSignInCancelledError](err); !ok {
		t.Fatalf("err = %v, want McpSignInCancelledError", err)
	}
}

func expectGrantTypes(t *testing.T, server *authorizationServer, want ...string) {
	t.Helper()
	if got := server.GrantTypes(); !slices.Equal(got, want) {
		t.Fatalf("token request grant types = %q, want %q", got, want)
	}
}

func serverCallbackID(serverURL string) string {
	sum := sha256.Sum256([]byte(serverURL))
	return base64.RawURLEncoding.EncodeToString(sum[:9])
}

// registration is the i-th client registration, with the listed members checked.
func expectRegistration(t *testing.T, server *authorizationServer, i int, want map[string]any) {
	t.Helper()
	registrations := server.Registrations()
	if i >= len(registrations) {
		t.Fatalf("registrations = %v, want at least %d", registrations, i+1)
	}
	for key, value := range want {
		if got := registrations[i][key]; !reflect.DeepEqual(got, value) {
			t.Fatalf("registrations[%d][%q] = %#v, want %#v", i, key, got, value)
		}
	}
}

// mutation-checked: dropping the scope passed to the code exchange (flow.Scope) fails it.
func TestSignInCompletesInASecondCallWithTheSameStoreAndAPersistedRedirect(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{Scope: "issues:read"})

	started := startRedirect(t, options)
	// Plain data: the application may store it and read it back in another request.
	data, err := json.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	var authorization oauth.McpSignInRedirect
	if err := json.Unmarshal(data, &authorization); err != nil {
		t.Fatal(err)
	}
	if authorization != started {
		t.Fatalf("authorization = %+v, want %+v", authorization, started)
	}
	if authorization.RedirectURL != route || authorization.Scope != "issues:read" {
		t.Fatalf("authorization = %+v, want redirect %s and scope issues:read", authorization, route)
	}
	if got := queryOf(t, authorization.AuthorizationURL).Get("state"); got != authorization.State {
		t.Fatalf("authorization URL state = %q, want %q", got, authorization.State)
	}
	expectRegistration(t, server, 0, map[string]any{"client_name": "app", "redirect_uris": []any{route}, "application_type": "web"})

	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	if err := complete(t.Context(), options, authorization, response); err != nil {
		t.Fatal(err)
	}

	tokens := loadState(t, options.Store).Tokens
	if tokens == nil || tokens.AccessToken != "access-1" || tokens.Scope != "issues:read" {
		t.Fatalf("tokens = %+v, want access-1 with scope issues:read", tokens)
	}
	request := server.TokenRequests()[0]
	if request.Get("redirect_uri") != route || request.Get("client_id") != "client-1" {
		t.Fatalf("token request = %v, want redirect_uri %s and client_id client-1", request, route)
	}
}

func TestSignInRefreshesTheStoredTokensInsteadOfRedirecting(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)

	started, err := oauth.StartMcpSignIn(t.Context(), options)
	if err != nil || started.Kind != "authorized" || started.Redirect != nil {
		t.Fatalf("started = %+v, %v, want authorized", started, err)
	}
	if got := len(server.Authorizations()); got != 1 {
		t.Fatalf("authorizations = %d, want 1", got)
	}
	if got := server.TokenRequests()[1].Get("grant_type"); got != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", got)
	}
	if got := loadState(t, options.Store).Tokens.AccessToken; got != "access-2" {
		t.Fatalf("access token = %q, want access-2", got)
	}
}

func TestSignInRefreshesUnderTheStoresRefreshLock(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	store := &recordingStore{}
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store = store
	signIn(t, options)

	started, err := oauth.StartMcpSignIn(t.Context(), options)
	if err != nil || started.Kind != "authorized" {
		t.Fatalf("started = %+v, %v, want authorized", started, err)
	}
	var saves []recordedSave
	for _, save := range store.Saves() {
		if save.accessToken == "access-2" {
			saves = append(saves, save)
		}
	}
	if want := []recordedSave{{locked: true, accessToken: "access-2"}}; !slices.Equal(saves, want) {
		t.Fatalf("saves of access-2 = %+v, want %+v", saves, want)
	}
}

// mutation-checked: failing the sign-in on McpRefreshLockBusyError fails it.
func TestSignInGoesToTheBrowserStepWhenAnotherProcessHoldsTheRefreshLockForTooLong(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	store := &recordingStore{}
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store = store
	signIn(t, options)
	store.lockError = oauth.NewMcpRefreshLockBusyError()

	started, err := oauth.StartMcpSignIn(t.Context(), options)
	if err != nil || started.Kind != "redirect" {
		t.Fatalf("started = %+v, %v, want the browser step", started, err)
	}
	expectGrantTypes(t, server, "authorization_code")
}

// mutation-checked: treating every error of the lock as McpRefreshLockBusyError fails it.
func TestSignInFailsWhenTheRefreshLockFailsForAnotherReason(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	lockError := errors.New("EACCES: permission denied")
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store = &recordingStore{lockError: lockError}

	if _, err := oauth.StartMcpSignIn(t.Context(), options); !errors.Is(err, lockError) {
		t.Fatalf("err = %v, want %v", err, lockError)
	}
	if got := server.Registrations(); len(got) != 0 {
		t.Fatalf("registrations = %v, want none", got)
	}
	if got := server.Authorizations(); len(got) != 0 {
		t.Fatalf("authorizations = %v, want none", got)
	}
}

// mutation-checked: dropping the redirect URI comparison of the flow (AuthorizationRedirectURL) sends the code.
func TestSignInRefusesToCompleteWithAnotherRedirectURIThanTheAuthorizationRequest(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.RedirectURL, options.ClientMetadataDocuments = loopbackRedirect, documents
	authorization := startRedirect(t, options)
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	// Another loopback port than the one passed to start: the server-specific variant differs too.
	wrong := options
	wrong.RedirectURL = "http://127.0.0.1:2/callback"
	expectError(t, complete(t.Context(), wrong, authorization, response), "but the authorization request used "+authorization.RedirectURL)
	if got := server.TokenRequests(); len(got) != 0 {
		t.Fatalf("token requests = %v, want none", got)
	}
}

// mutation-checked: not comparing the stored state on completion, or not clearing it on a new sign-in, fails it.
func TestSignInGivesEverySignInAFreshStateAndRefusesToCompleteASupersededOne(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	first := startRedirect(t, options)
	firstResponse := routeResponse(t, authorizeInBrowser(t, first.AuthorizationURL))
	second := startRedirect(t, options)
	if second.State == first.State {
		t.Fatalf("both sign-ins have the state %q", first.State)
	}

	expectError(t, complete(t.Context(), options, first, firstResponse), "Another sign-in to this MCP server started since")
	if got := server.TokenRequests(); len(got) != 0 {
		t.Fatalf("token requests = %v, want none", got)
	}
}

// mutation-checked: dropping the response state comparison sends the code of another sign-in.
func TestSignInRefusesAResponseWithAnotherState(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	authorization := startRedirect(t, options)
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	response.State = "other"
	expectError(t, complete(t.Context(), options, authorization, response), "belongs to a different sign-in")
	if got := server.TokenRequests(); len(got) != 0 {
		t.Fatalf("token requests = %v, want none", got)
	}
}

func TestSignInRefreshesWithTheStoredClientFromAnotherRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)

	other := options
	other.RedirectURL = otherRoute
	started, err := oauth.StartMcpSignIn(t.Context(), other)
	if err != nil || started.Kind != "authorized" {
		t.Fatalf("started = %+v, %v, want authorized", started, err)
	}
	if got := len(server.Registrations()); got != 1 {
		t.Fatalf("registrations = %d, want 1", got)
	}
	if got := server.TokenRequests()[1].Get("client_id"); got != "client-1" {
		t.Fatalf("refresh client_id = %q, want client-1", got)
	}
	if got := loadState(t, options.Store).ClientInformation.ClientID; got != "client-1" {
		t.Fatalf("stored client = %q, want client-1", got)
	}
}

func TestSignInKeepsTheClientForABrowserStepOnItsRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	server.RevokeRefreshTokens()

	authorization := startRedirect(t, options)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "client-1" {
		t.Fatalf("client_id = %q, want client-1", got)
	}
	if got := len(server.Registrations()); got != 1 {
		t.Fatalf("registrations = %d, want 1", got)
	}
}

// mutation-checked: keeping the stored client for another redirect URI fails it.
func TestSignInDropsTheClientAndItsTokensForABrowserStepOnAnotherRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	signIn(t, options)
	server.RevokeRefreshTokens()

	other := options
	other.RedirectURL = otherRoute
	authorization := startRedirect(t, other)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "client-2" {
		t.Fatalf("client_id = %q, want client-2", got)
	}
	expectRegistration(t, server, 1, map[string]any{"redirect_uris": []any{otherRoute}})
	state := loadState(t, options.Store)
	if state.ClientInformation == nil || state.ClientInformation.ClientID != "client-2" || state.Tokens != nil {
		t.Fatalf("state = %+v, want client-2 and no tokens", state)
	}
}

// mutation-checked: keeping a registered client that cimd replaces, or a document URL other than client.json, fails it.
func TestSignInDoesNotRefreshWithARegisteredClientThatCimdReplaces(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true, issParameter: true})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.RedirectURL, options.ClientMetadataDocuments = loopbackRedirect, documents
	dcr := options
	dcr.Settings = oauth.McpOAuthSettings{ClientRegistration: "dcr"}
	signIn(t, dcr)
	if got := loadState(t, options.Store).ClientInformation.ClientID; got != "client-1" {
		t.Fatalf("stored client = %q, want client-1", got)
	}

	cimd := options
	cimd.Settings = oauth.McpOAuthSettings{ClientRegistration: "cimd"}
	authorization := startRedirect(t, cimd)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "https://app.example/oauth/client.json" {
		t.Fatalf("client_id = %q, want the document", got)
	}
	expectGrantTypes(t, server, "authorization_code")
	state := loadState(t, options.Store)
	if state.ClientInformation != nil || state.Tokens != nil {
		t.Fatalf("state = %+v, want no client and no tokens", state)
	}
}

// mutation-checked: refreshing on a step-up, requesting the challenged scope alone, or not skipping the flow's refresh fails it.
func TestSignInAsksForTheGrantedScopePlusTheChallengedOneOnAStepUpWithoutRefreshing(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Challenge = &oauth.OAuthChallenge{Scope: "issues:read"}
	signIn(t, options)
	if got := loadState(t, options.Store).Tokens.Scope; got != "issues:read" {
		t.Fatalf("scope = %q, want issues:read", got)
	}

	// The challenge lists only the missing scope; requesting just that would lose issues:read.
	stepUp := options
	stepUp.Challenge = &oauth.OAuthChallenge{Error: "insufficient_scope", Scope: "issues:write"}
	authorization := signIn(t, stepUp)
	if authorization.Scope != "issues:read issues:write" {
		t.Fatalf("scope = %q, want issues:read issues:write", authorization.Scope)
	}
	expectGrantTypes(t, server, "authorization_code", "authorization_code")
	if got := loadState(t, options.Store).Tokens.Scope; got != "issues:read issues:write" {
		t.Fatalf("scope = %q, want issues:read issues:write", got)
	}
}

func loopbackSignIn(server *authorizationServer, settings oauth.McpOAuthSettings) oauth.McpSignInOptions {
	options := signInOptions(server, settings)
	options.RedirectURL, options.ClientMetadataDocuments = loopbackRedirect, documents
	return options
}

// mutation-checked: a document URL other than client.json fails it.
func TestSignInUsesADocumentWhenTheServerSupportsThemAndTheDocumentsListTheRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true, issParameter: true})
	options := loopbackSignIn(server, oauth.McpOAuthSettings{})
	authorization := signIn(t, options)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "https://app.example/oauth/client.json" {
		t.Fatalf("client_id = %q, want the document", got)
	}
	if authorization.RedirectURL != loopbackRedirect {
		t.Fatalf("redirect URL = %q, want %q", authorization.RedirectURL, loopbackRedirect)
	}
	if got := server.Registrations(); len(got) != 0 {
		t.Fatalf("registrations = %v, want none", got)
	}
	if got := server.TokenRequests()[0].Get("client_id"); got != "https://app.example/oauth/client.json" {
		t.Fatalf("token request client_id = %q, want the document", got)
	}
	if got := loadState(t, options.Store).ClientInformation; got != nil {
		t.Fatalf("stored client = %+v, want none", got)
	}
}

// mutation-checked: a redirect URI without the callback ID fails it.
func TestSignInUsesADocumentAndRedirectURISpecificToTheMCPServerWithoutTheIssParameter(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true})
	authorization := signIn(t, loopbackSignIn(server, oauth.McpOAuthSettings{}))
	id := serverCallbackID(server.ServerURL)
	if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "https://app.example/oauth/"+id+"/client.json" {
		t.Fatalf("client_id = %q, want the server-specific document", got)
	}
	if authorization.RedirectURL != loopbackRedirect+"/"+id {
		t.Fatalf("redirect URL = %q, want %q", authorization.RedirectURL, loopbackRedirect+"/"+id)
	}
	if got := server.TokenRequests()[0].Get("redirect_uri"); got != loopbackRedirect+"/"+id {
		t.Fatalf("token request redirect_uri = %q, want %q", got, loopbackRedirect+"/"+id)
	}
}

// mutation-checked: appending the callback ID after a trailing slash fails it.
func TestSignInAppendsTheServerSpecificPathWithoutDoublingATrailingSlash(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true})
	options := loopbackSignIn(server, oauth.McpOAuthSettings{})
	options.RedirectURL = loopbackRedirect + "/"
	options.ClientMetadataDocuments = &oauth.McpClientMetadataDocuments{BaseURL: documents.BaseURL, ListsRedirectURL: func(string) bool { return true }}
	authorization := startRedirect(t, options)
	if want := loopbackRedirect + "/" + serverCallbackID(server.ServerURL); authorization.RedirectURL != want {
		t.Fatalf("redirect URL = %q, want %q", authorization.RedirectURL, want)
	}
}

// routeDocuments list only the application's https callback route.
var routeDocuments = &oauth.McpClientMetadataDocuments{
	BaseURL:          "https://app.example/oauth",
	ListsRedirectURL: func(redirectURL string) bool { return redirectURL == route },
}

// mutation-checked: refreshing with the loopback redirect URI of the settings, which the documents do not list, sends the user to the browser.
func TestSignInRefreshesTheTokensOfADocumentThatListsOnlyAnHTTPSRoute(t *testing.T) {
	for _, registration := range []string{"auto", "cimd"} {
		t.Run(registration, func(t *testing.T) {
			server := startAuthorizationServer(t, authorizationServerOptions{cimd: true, issParameter: true})
			options := signInOptions(server, oauth.McpOAuthSettings{ClientRegistration: registration})
			options.ClientMetadataDocuments = routeDocuments
			authorization := signIn(t, options)
			if got := queryOf(t, authorization.AuthorizationURL).Get("client_id"); got != "https://app.example/oauth/client.json" {
				t.Fatalf("client_id = %q, want the document", got)
			}
			if got := loadState(t, options.Store).ClientInformation; got != nil {
				t.Fatalf("stored client = %+v, want none", got)
			}

			started, err := oauth.StartMcpSignIn(t.Context(), options)
			if err != nil || started.Kind != "authorized" {
				t.Fatalf("started = %+v, %v, want authorized", started, err)
			}
			if got := len(server.Authorizations()); got != 1 {
				t.Fatalf("authorizations = %d, want 1", got)
			}
			refresh := server.TokenRequests()[1]
			if refresh.Get("grant_type") != "refresh_token" || refresh.Get("client_id") != "https://app.example/oauth/client.json" {
				t.Fatalf("refresh request = %v, want a refresh_token grant by the document", refresh)
			}
			if got := loadState(t, options.Store).Tokens.AccessToken; got != "access-2" {
				t.Fatalf("access token = %q, want access-2", got)
			}
		})
	}
}

func TestSignInRegistersDynamicallyWhenTheServerDoesNotSupportDocuments(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{issParameter: true})
	signIn(t, loopbackSignIn(server, oauth.McpOAuthSettings{}))
	if got := len(server.Registrations()); got != 1 {
		t.Fatalf("registrations = %d, want 1", got)
	}
	if got := server.Authorizations()[0].Get("client_id"); got != "client-1" {
		t.Fatalf("client_id = %q, want client-1", got)
	}
}

func TestSignInRegistersDynamicallyWhenTheDocumentsDoNotListTheRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true, issParameter: true})
	options := loopbackSignIn(server, oauth.McpOAuthSettings{})
	options.RedirectURL = route
	signIn(t, options)
	if got := len(server.Registrations()); got != 1 {
		t.Fatalf("registrations = %d, want 1", got)
	}
}

func TestSignInRegistersDynamicallyWithoutDocumentsOrWithAClientName(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true, issParameter: true})
	options := loopbackSignIn(server, oauth.McpOAuthSettings{})
	options.ClientMetadataDocuments = nil
	signIn(t, options)
	signIn(t, loopbackSignIn(server, oauth.McpOAuthSettings{ClientName: "Other"}))
	expectRegistration(t, server, 0, map[string]any{"client_name": "app"})
	expectRegistration(t, server, 1, map[string]any{"client_name": "Other"})
	if got := len(server.Registrations()); got != 2 {
		t.Fatalf("registrations = %d, want 2", got)
	}
}

func TestSignInFailsWithCimdWhenTheServerDoesNotSupportDocuments(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{issParameter: true})
	_, err := oauth.StartMcpSignIn(t.Context(), loopbackSignIn(server, oauth.McpOAuthSettings{ClientRegistration: "cimd"}))
	expectError(t, err, "does not support Client ID Metadata Documents")
	if got := server.Registrations(); len(got) != 0 {
		t.Fatalf("registrations = %v, want none", got)
	}
}

func TestSignInFailsWithCimdWhenTheDocumentsDoNotListTheRedirectURI(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{cimd: true, issParameter: true})
	options := loopbackSignIn(server, oauth.McpOAuthSettings{ClientRegistration: "cimd"})
	options.RedirectURL = route
	_, err := oauth.StartMcpSignIn(t.Context(), options)
	expectError(t, err, "needs Client ID Metadata Documents that list the redirect URI")
	if got := server.Registrations(); len(got) != 0 {
		t.Fatalf("registrations = %v, want none", got)
	}
}

// mutation-checked: not reporting an ended context as McpSignInCancelledError fails it.
func TestSignInCancelsAStartWhoseRequestDoesNotAnswer(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	server.Stall("/register")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := oauth.StartMcpSignIn(ctx, signInOptions(server, oauth.McpOAuthSettings{}))
	expectCancelled(t, err)
}

// mutation-checked: not reporting an ended context as McpSignInCancelledError fails it.
func TestSignInCancelsACompletionWhoseTokenRequestDoesNotAnswer(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	options := signInOptions(server, oauth.McpOAuthSettings{})
	authorization := startRedirect(t, options)
	response := routeResponse(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	server.Stall("/token")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	expectCancelled(t, complete(ctx, options, authorization, response))
}

// mutation-checked: waiting for the lock without regard to the context, or running the refresh after the cancellation, fails it.
func TestSignInCancelsAtOnceWhileAnotherProcessHoldsTheRefreshLockAndRefreshesNothing(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	store := &recordingStore{}
	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store = store
	signIn(t, options)

	release := store.Hold()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := store.Entered()
	go func() {
		// The sign-in waits for the lock.
		<-entered
		cancel()
	}()
	_, err := oauth.StartMcpSignIn(ctx, options)
	expectCancelled(t, err)

	// The lock arrives after the cancellation: nothing runs under it, and it is released.
	release()
	store.WaitForLockRuns()
	if store.IsLocked() {
		t.Fatal("the refresh lock is still held")
	}
	expectGrantTypes(t, server, "authorization_code")
	if got := len(server.Authorizations()); got != 1 {
		t.Fatalf("authorizations = %d, want 1", got)
	}
}

func TestSignInFailsAtOnceWithAnEndedContext(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := oauth.StartMcpSignIn(ctx, signInOptions(server, oauth.McpOAuthSettings{}))
	expectCancelled(t, err)
	if got := server.Registrations(); len(got) != 0 {
		t.Fatalf("registrations = %v, want none", got)
	}
}
