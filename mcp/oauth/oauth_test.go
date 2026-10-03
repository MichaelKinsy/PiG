package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/test/oauth.test.ts.

// testOAuthProvider is TestOAuthProvider of oauth.test.ts.
type testOAuthProvider struct {
	redirectURL      string
	client           *oauth.OAuthClientInformationMixed
	tokenSet         *oauth.OAuthTokens
	verifier         string
	discovery        *oauth.OAuthDiscoveryState
	authorizationURL *url.URL
}

func newTestOAuthProvider(redirectURL string) *testOAuthProvider {
	return &testOAuthProvider{redirectURL: redirectURL}
}

func (p *testOAuthProvider) RedirectURL() string { return p.redirectURL }
func (p *testOAuthProvider) ClientMetadata() oauth.OAuthClientMetadata {
	return oauth.OAuthClientMetadata{
		RedirectURIs: []string{p.redirectURL}, ClientName: "pi-mcp-test",
		GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}, TokenEndpointAuthMethod: "none",
	}
}
func (p *testOAuthProvider) State(context.Context) (string, error) { return "expected-state", nil }
func (p *testOAuthProvider) ClientInformation(context.Context) (*oauth.OAuthClientInformationMixed, error) {
	return p.client, nil
}
func (p *testOAuthProvider) SaveClientInformation(_ context.Context, information oauth.OAuthClientInformationMixed) error {
	p.client = &information
	return nil
}
func (p *testOAuthProvider) Tokens(context.Context) (*oauth.OAuthTokens, error) {
	return p.tokenSet, nil
}
func (p *testOAuthProvider) SaveTokens(_ context.Context, tokens oauth.OAuthTokens) error {
	p.tokenSet = &tokens
	return nil
}
func (p *testOAuthProvider) RedirectToAuthorization(_ context.Context, u *url.URL) error {
	p.authorizationURL = u
	return nil
}
func (p *testOAuthProvider) SaveCodeVerifier(_ context.Context, verifier string) error {
	p.verifier = verifier
	return nil
}
func (p *testOAuthProvider) CodeVerifier(context.Context) (string, error) {
	if p.verifier == "" {
		return "", errors.New("Missing code verifier")
	}
	return p.verifier, nil
}
func (p *testOAuthProvider) InvalidateCredentials(_ context.Context, kind string) error {
	if kind == "all" || kind == "client" {
		p.client = nil
	}
	if kind == "all" || kind == "tokens" {
		p.tokenSet = nil
	}
	if kind == "all" || kind == "verifier" {
		p.verifier = ""
	}
	if kind == "all" || kind == "discovery" {
		p.discovery = nil
	}
	return nil
}
func (p *testOAuthProvider) SaveDiscoveryState(_ context.Context, state oauth.OAuthDiscoveryState) error {
	p.discovery = &state
	return nil
}
func (p *testOAuthProvider) DiscoveryState(context.Context) (*oauth.OAuthDiscoveryState, error) {
	return p.discovery, nil
}

func listen(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, origin string)) string {
	t.Helper()
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(w, r, origin) }))
	origin = server.URL
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	return origin
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func readAll(r *http.Request) string {
	data, _ := io.ReadAll(r.Body)
	return string(data)
}

func TestMCPOAuthDiscoversRegistersAuthorizesWithPKCEAndRefreshesOn401(t *testing.T) {
	ctx := t.Context()
	var expectedChallenge atomic.Value
	var refreshes atomic.Int32
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			jsonResponse(w, 200, map[string]any{"resource": serverOrigin + "/mcp", "authorization_servers": []string{serverOrigin}, "scopes_supported": []string{"org:read"}})
		case "/.well-known/oauth-authorization-server":
			jsonResponse(w, 200, map[string]any{
				"issuer": serverOrigin, "authorization_endpoint": serverOrigin + "/authorize", "token_endpoint": serverOrigin + "/token",
				"registration_endpoint": serverOrigin + "/register", "response_types_supported": []string{"code"},
				"grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"},
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/register":
			var metadata map[string]any
			_ = json.Unmarshal([]byte(readAll(r)), &metadata)
			// Empty and null optional fields count as absent (#10266).
			metadata["client_id"] = "test-client"
			metadata["client_secret"] = ""
			jsonResponse(w, 201, metadata)
		case "/authorize":
			expectedChallenge.Store(r.URL.Query().Get("code_challenge"))
			redirect, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
			query := redirect.Query()
			query.Set("code", "test-code")
			query.Set("state", r.URL.Query().Get("state"))
			redirect.RawQuery = query.Encode()
			w.Header().Set("Location", redirect.String())
			w.WriteHeader(302)
		case "/token":
			params, _ := url.ParseQuery(readAll(r))
			if params.Get("grant_type") == "refresh_token" {
				refreshes.Add(1)
				jsonResponse(w, 200, map[string]any{
					"access_token":  "refreshed-token",
					"token_type":    "Bearer",
					"refresh_token": "",
					"expires_in":    nil,
				})
				return
			}
			digest := sha256.Sum256([]byte(params.Get("code_verifier")))
			challenge := base64.RawURLEncoding.EncodeToString(digest[:])
			want, _ := expectedChallenge.Load().(string)
			if params.Get("code") != "test-code" || challenge != want {
				jsonResponse(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			jsonResponse(w, 200, map[string]any{
				"access_token":  "first-token",
				"refresh_token": "refresh-token",
				"token_type":    "Bearer",
				"scope":         "",
			})
		case "/mcp":
			switch r.Method {
			case http.MethodGet:
				w.WriteHeader(405)
				return
			case http.MethodDelete:
				w.WriteHeader(200)
				return
			}
			token := r.Header.Get("Authorization")
			if token != "Bearer first-token" && token != "Bearer refreshed-token" {
				_ = readAll(r)
				// An empty scope falls through to the resource metadata's scopes_supported.
				w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp", scope=""`, serverOrigin))
				w.WriteHeader(401)
				_, _ = io.WriteString(w, "Unauthorized")
				return
			}
			var message map[string]any
			_ = json.Unmarshal([]byte(readAll(r)), &message)
			id, hasID := message["id"]
			if !hasID {
				w.WriteHeader(202)
				return
			}
			var result any = map[string]any{"tools": []any{map[string]any{"name": "issues", "inputSchema": map[string]any{"type": "object"}}}}
			if message["method"] == "initialize" {
				result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "oauth-test", "version": "1.0.0"}}
			}
			jsonResponse(w, 200, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		default:
			w.WriteHeader(404)
		}
	})

	callback, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	provider := newTestOAuthProvider(callback.RedirectURL)
	newTransport := func() *mcp.StreamableHTTPTransport {
		transport, err := mcp.NewStreamableHTTPTransport(mcp.StreamableHTTPTransportOptions{
			URL: origin + "/mcp", Headers: map[string]string{"Authorization": "Bearer caller-supplied-stale-token"},
			AuthProvider: oauth.AdaptOAuthProvider(provider), OpenGetStream: new(false),
		})
		if err != nil {
			t.Fatal(err)
		}
		return transport
	}
	newClient := func() *mcp.Client {
		return mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "oauth-test", Version: "1.0.0"}})
	}
	firstClient := newClient()
	_, err = firstClient.Connect(ctx, newTransport())
	if _, ok := errors.AsType[*oauth.McpOAuthAuthorizationRequiredError](err); !ok {
		t.Fatalf("connect err = %v", err)
	}
	if provider.authorizationURL == nil || provider.authorizationURL.Query().Get("scope") != "org:read" || provider.authorizationURL.Query().Get("resource") != origin+"/mcp" {
		t.Fatalf("authorization url = %v", provider.authorizationURL)
	}

	wait, err := callback.WaitForCallback("expected-state")
	if err != nil {
		t.Fatal(err)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	authorizationResponse, err := noFollow.Get(provider.authorizationURL.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = authorizationResponse.Body.Close()
	followed, err := http.Get(authorizationResponse.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	_ = followed.Body.Close()
	received, err := wait.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := oauth.AuthorizeMcp(ctx, provider, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp", AuthorizationCode: received.Code})
	if err != nil || result != oauth.OAuthAuthorized {
		t.Fatalf("authorize = %q, %v", result, err)
	}

	client := newClient()
	if _, err := client.Connect(ctx, newTransport()); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(ctx, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "issues" {
		t.Fatalf("tools = %#v", tools)
	}
	_ = client.Close()

	tokens := *provider.tokenSet
	tokens.AccessToken = "stale-token"
	provider.tokenSet = &tokens
	refreshedClient := newClient()
	if _, err := refreshedClient.Connect(ctx, newTransport()); err != nil {
		t.Fatal(err)
	}
	// Neither token response names a scope, so the grant has the requested scope.
	if got, want := *provider.tokenSet, (oauth.OAuthTokens{
		AccessToken:  "refreshed-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Scope:        "org:read",
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("token set = %#v, want %#v", got, want)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d", refreshes.Load())
	}
	_ = refreshedClient.Close()
	_ = callback.Close()
}

func unauthorized(origin string, token string) mcp.UnauthorizedContext {
	response := &http.Response{StatusCode: 401, Header: http.Header{"Www-Authenticate": []string{"Bearer"}}, Body: http.NoBody}
	serverURL, _ := url.Parse(origin + "/mcp")
	return mcp.UnauthorizedContext{Response: response, ServerURL: serverURL, Fetch: http.DefaultClient, Token: token}
}

func TestMCPOAuthSharesOneRefreshBetweenConcurrent401sWhenRefreshTokensRotate(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var grants []string
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			// Invalid resource metadata falls back to the server origin instead of failing discovery.
			jsonResponse(w, 200, map[string]any{"resource": serverOrigin + "/mcp", "authorization_servers": []string{"not a url"}})
		case "/.well-known/oauth-authorization-server":
			// Issuer without the trailing slash that URL parsing adds to the fallback server URL.
			jsonResponse(w, 200, map[string]any{
				"issuer": serverOrigin, "authorization_endpoint": serverOrigin + "/authorize", "token_endpoint": serverOrigin + "/token",
				"response_types_supported": []string{"code"},
			})
		case "/token":
			params, _ := url.ParseQuery(readAll(r))
			refreshToken := params.Get("refresh_token")
			mu.Lock()
			grants = append(grants, refreshToken)
			mu.Unlock()
			if refreshToken != "r1" {
				jsonResponse(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			time.Sleep(20 * time.Millisecond)
			jsonResponse(w, 200, map[string]any{"access_token": "a2", "refresh_token": "r2", "token_type": "Bearer", "expires_in": 3600})
		default:
			w.WriteHeader(404)
		}
	})
	store := &oauth.MemoryOAuthStateStore{}
	provider, err := oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
		ServerURL: origin + "/mcp", RedirectURL: "http://127.0.0.1/callback", ClientMetadata: oauth.OAuthClientMetadata{ClientName: "test"},
		ClientID: "client", Store: store, OnRedirect: func(context.Context, *url.URL) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveTokens(ctx, oauth.OAuthTokens{AccessToken: "a1", RefreshToken: "r1", TokenType: "Bearer"}); err != nil {
		t.Fatal(err)
	}
	auth := oauth.AdaptOAuthProvider(provider)
	handler := auth.(mcp.UnauthorizedHandler)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { errs <- handler.OnUnauthorized(ctx, unauthorized(origin, "a1")) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// A late 401 for a request that still carried the old token must not refresh again.
	if err := handler.OnUnauthorized(ctx, unauthorized(origin, "a1")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := strings.Join(grants, ",")
	mu.Unlock()
	if got != "r1" {
		t.Fatalf("grants = %s", got)
	}
	token, err := auth.Token(ctx)
	if err != nil || token != "a2" {
		t.Fatalf("token = %q, %v", token, err)
	}
	state, _ := store.Load(ctx)
	if state == nil || state.Tokens == nil || state.Tokens.RefreshToken != "r2" {
		t.Fatalf("state = %#v", state)
	}
	if state.TokensExpireAt == nil || *state.TokensExpireAt <= time.Now().UnixMilli()+3_500_000 {
		t.Fatalf("tokensExpireAt = %v", state.TokensExpireAt)
	}
}

func TestMCPOAuthAsksForAuthorizationInsteadOfRefreshingWhenTheServerNeedsMoreScope(t *testing.T) {
	ctx := t.Context()
	provider := newTestOAuthProvider("http://127.0.0.1/callback")
	provider.client = &oauth.OAuthClientInformationMixed{ClientID: "client"}
	provider.tokenSet = &oauth.OAuthTokens{AccessToken: "a1", RefreshToken: "r1", TokenType: "Bearer", Scope: "repo read:org"}
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			jsonResponse(w, 200, map[string]any{
				"issuer": serverOrigin, "authorization_endpoint": serverOrigin + "/authorize", "token_endpoint": serverOrigin + "/token",
				"response_types_supported": []string{"code"},
			})
			return
		}
		if r.URL.Path == "/token" {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(404)
	})
	auth := oauth.AdaptOAuthProvider(provider).(mcp.UnauthorizedHandler)
	serverURL, _ := url.Parse(origin + "/mcp")
	err := auth.OnUnauthorized(ctx, mcp.UnauthorizedContext{
		Response:  &http.Response{StatusCode: 403, Body: http.NoBody, Header: http.Header{"Www-Authenticate": []string{`Bearer error="insufficient_scope", scope="repo admin"`}}},
		ServerURL: serverURL, Fetch: http.DefaultClient, Token: "a1",
	})
	if _, ok := errors.AsType[*oauth.McpOAuthAuthorizationRequiredError](err); !ok {
		t.Fatalf("err = %v", err)
	}
	// The challenge may list only the missing scopes; the new grant keeps the old ones too.
	if provider.authorizationURL == nil || provider.authorizationURL.Query().Get("scope") != "repo read:org admin" {
		t.Fatalf("authorization url = %v", provider.authorizationURL)
	}
	// The working grant is kept until the user authorizes the new scope.
	if provider.tokenSet.AccessToken != "a1" {
		t.Fatalf("access token = %s", provider.tokenSet.AccessToken)
	}
}

func TestMCPOAuthBindsPersistedCredentialsToTheExactMCPServerURL(t *testing.T) {
	ctx := t.Context()
	store := &oauth.MemoryOAuthStateStore{}
	redirect := func(context.Context, *url.URL) error { return nil }
	first, err := oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
		ServerURL: "https://one.example/mcp", RedirectURL: "http://127.0.0.1/callback", ClientMetadata: oauth.OAuthClientMetadata{ClientName: "test"},
		Store: store, OnRedirect: redirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SaveTokens(ctx, oauth.OAuthTokens{AccessToken: "secret", TokenType: "Bearer"}); err != nil {
		t.Fatal(err)
	}
	tokens, err := first.Tokens(ctx)
	if err != nil || tokens == nil || tokens.AccessToken != "secret" {
		t.Fatalf("tokens = %#v, %v", tokens, err)
	}
	second, err := oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
		ServerURL: "https://two.example/mcp", RedirectURL: "http://127.0.0.1/callback", ClientMetadata: oauth.OAuthClientMetadata{ClientName: "test"},
		Store: store, OnRedirect: redirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens, err := second.Tokens(ctx); err != nil || tokens != nil {
		t.Fatalf("second tokens = %#v, %v", tokens, err)
	}
}

func TestMCPOAuthRejectsAuthorizationMetadataWhoseIssuerDoesNotMatchDiscovery(t *testing.T) {
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			jsonResponse(w, 200, map[string]any{
				"issuer": "https://attacker.example", "authorization_endpoint": serverOrigin + "/authorize", "token_endpoint": serverOrigin + "/token",
				"response_types_supported": []string{"code"},
			})
			return
		}
		w.WriteHeader(404)
	})
	_, err := oauth.DiscoverAuthorizationServerMetadata(t.Context(), origin, oauth.DiscoveryOptions{})
	if _, ok := errors.AsType[*oauth.OAuthIssuerMismatchError](err); !ok {
		t.Fatalf("err = %v", err)
	}
}

// #10172
func TestMCPOAuthUsesAConfiguredAuthorizationServerMetadataDocumentAsIs(t *testing.T) {
	ctx := t.Context()
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, serverOrigin string) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			// Names the MCP server itself, which serves no authorization server metadata.
			jsonResponse(w, 200, map[string]any{"resource": serverOrigin + "/mcp", "authorization_servers": []string{serverOrigin}})
		case "/idp/metadata.json":
			jsonResponse(w, 200, map[string]any{
				// Not derivable from the document URL; a configured document is not checked.
				"issuer":                   "https://idp.example",
				"authorization_endpoint":   serverOrigin + "/idp/authorize",
				"token_endpoint":           serverOrigin + "/idp/token",
				"response_types_supported": []string{"code"},
			})
		default:
			w.WriteHeader(404)
		}
	})
	provider := newTestOAuthProvider("http://127.0.0.1/callback")
	provider.client = &oauth.OAuthClientInformationMixed{ClientID: "client"}
	metadataURL, _ := url.Parse(origin + "/idp/metadata.json")
	options := oauth.OAuthFlowOptions{
		ServerURL:                      origin + "/mcp",
		AuthorizationServerMetadataURL: metadataURL,
	}
	result, err := oauth.AuthorizeMcp(ctx, provider, options)
	if err != nil || result != oauth.OAuthRedirect {
		t.Fatalf("authorize = %q, %v", result, err)
	}
	authorizationURL := provider.authorizationURL
	if authorizationURL == nil {
		t.Fatal("no authorization url")
	}
	if got := authorizationURL.Scheme + "://" + authorizationURL.Host + authorizationURL.Path; got != origin+"/idp/authorize" {
		t.Fatalf("authorization endpoint = %s", got)
	}
	if got := authorizationURL.Query().Get("resource"); got != origin+"/mcp" {
		t.Fatalf("resource = %s", got)
	}

	insecure := options
	insecure.AuthorizationServerMetadataURL, _ = url.Parse("http://idp.example/metadata.json")
	_, err = oauth.AuthorizeMcp(ctx, provider, insecure)
	if _, ok := errors.AsType[*oauth.OAuthInsecureEndpointError](err); !ok {
		t.Fatalf("insecure err = %v", err)
	}
}

func TestMCPOAuthExchangesACodeOnlyWhenItsIssParameterNamesTheAuthorizationServer(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var codes []string
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		params, _ := url.ParseQuery(readAll(r))
		mu.Lock()
		codes = append(codes, params.Get("code"))
		mu.Unlock()
		jsonResponse(w, 200, map[string]any{"access_token": "token", "token_type": "Bearer"})
	})
	exchange := func(code string, iss *string, issParameterSupported bool) (oauth.OAuthFlowResult, error) {
		provider := newTestOAuthProvider("http://127.0.0.1/callback")
		provider.client = &oauth.OAuthClientInformationMixed{ClientID: "client"}
		provider.verifier = "verifier"
		provider.discovery = &oauth.OAuthDiscoveryState{
			AuthorizationServerURL: origin,
			AuthorizationServerMetadata: &oauth.AuthorizationServerMetadata{
				Issuer:                 origin,
				AuthorizationEndpoint:  origin + "/authorize",
				TokenEndpoint:          origin + "/token",
				ResponseTypesSupported: []string{"code"},
				AuthorizationResponseIssParameterSupported: new(issParameterSupported),
			},
		}
		return oauth.AuthorizeMcp(ctx, provider, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp", AuthorizationCode: code, Iss: iss})
	}
	if _, err := exchange("other", new("https://attacker.example"), false); !isIssuerMismatch(err) {
		t.Fatalf("other: err = %v", err)
	}
	if _, err := exchange("missing", nil, true); !isIssuerMismatch(err) {
		t.Fatalf("missing: err = %v", err)
	}
	if result, err := exchange("matching", new(origin), true); err != nil || result != oauth.OAuthAuthorized {
		t.Fatalf("matching = %q, %v", result, err)
	}
	// Servers that do not promise the parameter may omit it.
	if result, err := exchange("omitted", nil, false); err != nil || result != oauth.OAuthAuthorized {
		t.Fatalf("omitted = %q, %v", result, err)
	}
	mu.Lock()
	got := strings.Join(codes, ",")
	mu.Unlock()
	if got != "matching,omitted" {
		t.Fatalf("codes = %s", got)
	}
}

func isIssuerMismatch(err error) bool {
	_, ok := errors.AsType[*oauth.OAuthIssuerMismatchError](err)
	return ok
}

func TestOAuthCallbackServerPagesRendersPlainTextByDefault(t *testing.T) {
	callback, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = callback.Close() }()
	pending, err := callback.WaitForCallback("s1")
	if err != nil {
		t.Fatal(err)
	}
	//nolint:bodyclose // readAllBody closes the body
	response, err := http.Get(callback.RedirectURL + "?code=abc&state=s1")
	if err != nil {
		t.Fatal(err)
	}
	body := readAllBody(response)
	if got := response.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if body != "Authorization complete. You may close this window." {
		t.Fatalf("body = %q", body)
	}
	received, err := pending.Wait(t.Context())
	if err != nil || received.Code != "abc" {
		t.Fatalf("callback = %#v, %v", received, err)
	}
}

func readAllBody(response *http.Response) string {
	defer func() { _ = response.Body.Close() }()
	data, _ := io.ReadAll(response.Body)
	return string(data)
}

func TestOAuthCallbackServerPagesRendersPagesThroughRenderPage(t *testing.T) {
	var mu sync.Mutex
	var pages []oauth.OAuthCallbackPage
	callback, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{RenderPage: func(page oauth.OAuthCallbackPage) string {
		mu.Lock()
		pages = append(pages, page)
		mu.Unlock()
		if page.OK {
			return "<p>ok</p>"
		}
		return "<p>" + page.Message + "</p>"
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = callback.Close() }()
	denied, err := callback.WaitForCallback("s1")
	if err != nil {
		t.Fatal(err)
	}
	//nolint:bodyclose // readAllBody closes the body
	failure, err := http.Get(callback.RedirectURL + "?error=access_denied&error_description=Denied&state=s1")
	if err != nil {
		t.Fatal(err)
	}
	readAllBody(failure)
	if got := failure.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if _, err := denied.Wait(t.Context()); err == nil || err.Error() != "Denied" {
		t.Fatalf("denied err = %v", err)
	}
	mu.Lock()
	last := pages[len(pages)-1]
	mu.Unlock()
	if last != (oauth.OAuthCallbackPage{OK: false, Message: "Authorization failed. You may close this window.", Details: "Denied"}) {
		t.Fatalf("page = %#v", last)
	}

	pending, err := callback.WaitForCallback("s2")
	if err != nil {
		t.Fatal(err)
	}
	//nolint:bodyclose // readAllBody closes the body
	success, err := http.Get(callback.RedirectURL + "?code=abc&state=s2")
	if err != nil {
		t.Fatal(err)
	}
	if body := readAllBody(success); body != "<p>ok</p>" {
		t.Fatalf("body = %q", body)
	}
	received, err := pending.Wait(t.Context())
	if err != nil || received.Code != "abc" {
		t.Fatalf("callback = %#v, %v", received, err)
	}
}

// Regression: refreshAuthorization keeps the old refresh token when the server
// does not rotate it, as `{ refresh_token: options.refreshToken, ...tokens }` does.
func TestRefreshAuthorizationKeepsTheOldRefreshTokenWhenTheServerDoesNotRotateIt(t *testing.T) {
	origin := listen(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		params, _ := url.ParseQuery(readAll(r))
		if params.Get("grant_type") != "refresh_token" || params.Get("refresh_token") != "old" {
			jsonResponse(w, 400, map[string]any{"error": "invalid_grant"})
			return
		}
		jsonResponse(w, 200, map[string]any{"access_token": "fresh", "token_type": "Bearer"})
	})
	tokens, err := oauth.RefreshAuthorization(t.Context(), origin, oauth.RefreshAuthorizationOptions{
		TokenRequestOptions: oauth.TokenRequestOptions{ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "client"}},
		RefreshToken:        "old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "fresh" || tokens.RefreshToken != "old" {
		t.Fatalf("tokens = %#v", tokens)
	}
}

// Regression: credentials must not go to a plain HTTP endpoint that is not loopback.
func TestTokenRequestsRefuseNonLoopbackHTTPEndpoints(t *testing.T) {
	_, err := oauth.RefreshAuthorization(t.Context(), "http://auth.example.com", oauth.RefreshAuthorizationOptions{
		TokenRequestOptions: oauth.TokenRequestOptions{ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "client"}},
		RefreshToken:        "old",
	})
	if _, ok := errors.AsType[*oauth.OAuthInsecureEndpointError](err); !ok {
		t.Fatalf("err = %v", err)
	}
}

// flakyStateStore is the probe store of McpOAuthProvider's write chain: load and save fail on demand and count calls.
type flakyStateStore struct {
	mu       sync.Mutex
	value    *oauth.McpOAuthState
	loadErr  error
	saveErr  error
	saveRuns int
}

func (s *flakyStateStore) Load(context.Context) (*oauth.McpOAuthState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.value == nil {
		return nil, nil
	}
	clone := s.value.Clone()
	return &clone, nil
}

func (s *flakyStateStore) Save(_ context.Context, state oauth.McpOAuthState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveRuns++
	if s.saveErr != nil {
		return s.saveErr
	}
	clone := state.Clone()
	s.value = &clone
	return nil
}

func (s *flakyStateStore) set(loadErr, saveErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadErr, s.saveErr = loadErr, saveErr
}

func (s *flakyStateStore) saves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveRuns
}

// McpOAuthProvider (.upstream/v0.99.2/packages/mcp/src/oauth/provider.ts:50,70-138) chains every write on one promise,
// `this.writes = this.writes.then(...)`, and load() awaits it. A failed store.save or a store.load inside update leaves
// that promise rejected for good: every later load and update rejects with the same error and the store is not touched
// again. A store.load that fails outside the chain (load) fails only that call. Expected values are a Node 24 run of
// provider.ts with a store that fails on demand.
func TestMcpOAuthProviderKeepsFailingAfterAFailedWrite(t *testing.T) {
	ctx := t.Context()
	newProvider := func(store oauth.McpOAuthStateStore, clientID string) *oauth.McpOAuthProvider {
		t.Helper()
		provider, err := oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
			ServerURL: "https://s.example/mcp", RedirectURL: "http://127.0.0.1/cb", ClientMetadata: oauth.OAuthClientMetadata{ClientName: "t"},
			ClientID: clientID, Store: store, OnRedirect: func(context.Context, *url.URL) error { return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		return provider
	}
	tokens := func(access string) oauth.OAuthTokens {
		return oauth.OAuthTokens{AccessToken: access, TokenType: "Bearer"}
	}
	errSave, errLoad := errors.New("save-disk"), errors.New("load-disk")

	t.Run("a failed save fails every later load and update with the same error", func(t *testing.T) {
		store := &flakyStateStore{}
		provider := newProvider(store, "")
		if err := provider.SaveTokens(ctx, tokens("a")); err != nil {
			t.Fatal(err)
		}
		store.set(nil, errSave)
		if err := provider.SaveTokens(ctx, tokens("b")); !errors.Is(err, errSave) {
			t.Fatalf("failing save error = %v, want %v", err, errSave)
		}
		store.set(nil, nil)
		saves := store.saves()
		calls := map[string]error{
			"Tokens":                func() error { _, err := provider.Tokens(ctx); return err }(),
			"SaveTokens":            provider.SaveTokens(ctx, tokens("c")),
			"State":                 func() error { _, err := provider.State(ctx); return err }(),
			"CodeVerifier":          func() error { _, err := provider.CodeVerifier(ctx); return err }(),
			"InvalidateCredentials": provider.InvalidateCredentials(ctx, "all"),
			"ClientInformation":     func() error { _, err := provider.ClientInformation(ctx); return err }(),
			"SaveClientInformation": provider.SaveClientInformation(ctx, oauth.OAuthClientInformationMixed{ClientID: "x"}),
		}
		for name, err := range calls {
			if !errors.Is(err, errSave) {
				t.Errorf("%s after a failed save: error = %v, want %v", name, err, errSave)
			}
		}
		if got := store.saves(); got != saves {
			t.Errorf("store.Save ran %d more times after the failure, want 0", got-saves)
		}
		if err := provider.RedirectToAuthorization(ctx, &url.URL{Scheme: "https", Host: "a.example", Path: "/"}); err != nil {
			t.Errorf("RedirectToAuthorization after a failed save: %v", err)
		}
	})

	t.Run("a configured client never touches the chain", func(t *testing.T) {
		store := &flakyStateStore{}
		provider := newProvider(store, "cid")
		store.set(nil, errSave)
		if err := provider.SaveTokens(ctx, tokens("b")); !errors.Is(err, errSave) {
			t.Fatalf("failing save error = %v, want %v", err, errSave)
		}
		store.set(nil, nil)
		if client, err := provider.ClientInformation(ctx); err != nil || client == nil || client.ClientID != "cid" {
			t.Errorf("ClientInformation = %#v, %v, want the configured client", client, err)
		}
		if err := provider.SaveClientInformation(ctx, oauth.OAuthClientInformationMixed{ClientID: "x"}); err != nil {
			t.Errorf("SaveClientInformation with a configured client: %v", err)
		}
	})

	t.Run("a failed load outside an update fails only that call", func(t *testing.T) {
		store := &flakyStateStore{}
		provider := newProvider(store, "")
		store.set(errLoad, nil)
		if _, err := provider.Tokens(ctx); !errors.Is(err, errLoad) {
			t.Fatalf("failing load error = %v, want %v", err, errLoad)
		}
		store.set(nil, nil)
		if _, err := provider.Tokens(ctx); err != nil {
			t.Fatalf("Tokens after a failed load: %v", err)
		}
	})

	t.Run("a failed load inside an update fails every later call", func(t *testing.T) {
		store := &flakyStateStore{}
		provider := newProvider(store, "")
		store.set(errLoad, nil)
		if err := provider.SaveTokens(ctx, tokens("b")); !errors.Is(err, errLoad) {
			t.Fatalf("failing update error = %v, want %v", err, errLoad)
		}
		store.set(nil, nil)
		if _, err := provider.Tokens(ctx); !errors.Is(err, errLoad) {
			t.Fatalf("Tokens after a failed update load: error = %v, want %v", err, errLoad)
		}
	})
}

// A cancelled context has no upstream counterpart (store calls take no signal), so a save that fails because the
// caller cancelled is not a rejected write: the provider keeps working for the next call.
func TestMcpOAuthProviderIsNotPoisonedByACancelledCall(t *testing.T) {
	store := &flakyStateStore{}
	provider, err := oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
		ServerURL: "https://s.example/mcp", RedirectURL: "http://127.0.0.1/cb", ClientMetadata: oauth.OAuthClientMetadata{ClientName: "t"},
		Store: store, OnRedirect: func(context.Context, *url.URL) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	store.set(nil, context.Canceled)
	cancel()
	if err := provider.SaveTokens(cancelled, oauth.OAuthTokens{AccessToken: "a", TokenType: "Bearer"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save error = %v", err)
	}
	store.set(nil, nil)
	if err := provider.SaveTokens(t.Context(), oauth.OAuthTokens{AccessToken: "b", TokenType: "Bearer"}); err != nil {
		t.Fatalf("SaveTokens after a cancelled call: %v", err)
	}
}

// packages/mcp/src/oauth/flow.ts:162-172 set the authorize parameters in this order after any query the endpoint already has.
func TestStartAuthorizationKeepsUpstreamParameterOrder(t *testing.T) {
	authorizationURL, _, err := oauth.StartAuthorization("https://auth.example.com", oauth.StartAuthorizationOptions{
		Metadata: &oauth.AuthorizationServerMetadata{
			AuthorizationEndpoint:         "https://auth.example.com/authorize?tenant=a",
			ResponseTypesSupported:        []string{"code"},
			CodeChallengeMethodsSupported: []string{"S256"},
		},
		ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "client"},
		RedirectURL:       "http://localhost/callback",
		Scope:             "read offline_access",
		State:             "state",
		Resource:          "https://mcp.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for pair := range strings.SplitSeq(authorizationURL.RawQuery, "&") {
		key, _, _ := strings.Cut(pair, "=")
		got = append(got, key)
	}
	want := []string{"tenant", "response_type", "client_id", "code_challenge", "code_challenge_method", "redirect_uri", "state", "scope", "prompt", "resource"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("authorization URL parameter order = %v, want %v\nURL: %s", got, want, authorizationURL)
	}
}
