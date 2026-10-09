package oauth_test

// Pins packages/mcp/src/oauth/errors.ts (messages and names), callback.ts handle()/reply()/waitForCallback() (state
// consumption, error and missing-code responses, response headers) and provider.ts invalidateCredentials(), which
// packages/mcp/test/oauth.test.ts exercises only through whole flows.

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

func TestOAuthErrorMessagesAreTheOnesErrorsTSBuilds(t *testing.T) {
	received := "https://evil.example"
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&oauth.OAuthError{Code: "invalid_grant", Message: "The grant expired"}, "The grant expired"},
		{&oauth.OAuthError{Code: "invalid_grant"}, "invalid_grant"},
		{&oauth.OAuthIssuerMismatchError{Expected: "https://auth.example", Received: &received}, `OAuth issuer mismatch: expected "https://auth.example", received "https://evil.example"`},
		{&oauth.OAuthInsecureEndpointError{Endpoint: "http://auth.example/token"}, "Refusing to send OAuth credentials to non-HTTPS endpoint http://auth.example/token"},
		{&oauth.OAuthRegistrationError{Status: 400, Body: "bad redirect"}, "OAuth dynamic client registration failed with status 400: bad redirect"},
		{&oauth.McpOAuthAuthorizationRequiredError{}, "MCP OAuth authorization requires user interaction"},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("%T message = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func callbackGet(t *testing.T, target string) (int, http.Header, string) {
	t.Helper()
	response, err := http.Get(target) //nolint:gosec,noctx // loopback test server
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, response.Header, string(body)
}

func TestOAuthCallbackServerAnswersEachAuthorizationResponseLikeCallbackTS(t *testing.T) {
	server, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	wait := func(state string) func() (oauth.OAuthCallback, error) {
		t.Helper()
		return startWait(t, server, state)
	}
	result := func(w func() (oauth.OAuthCallback, error)) (oauth.OAuthCallback, error) { return w() }

	// An unknown path is 404 and does not touch a pending state.
	pending := wait("s1")
	if status, _, body := callbackGet(t, strings.TrimSuffix(server.RedirectURL, "/callback")+"/other?state=s1&code=c"); status != 404 || body != "Not found" {
		t.Fatalf("other path = %d %q", status, body)
	}
	// An empty state is never a match, even for a wait registered under "" (callback.ts: `!state || !pending`).
	emptyWait := wait("")
	// No state, or a state nobody waits for: 400.
	for _, query := range []string{"", "?code=c", "?state=&code=c", "?state=unknown&code=c"} {
		if status, _, body := callbackGet(t, server.RedirectURL+query); status != 400 || body != "Invalid or expired OAuth state" {
			t.Fatalf("query %q = %d %q", query, status, body)
		}
	}
	_ = emptyWait
	if _, err := startWait(t, server, "s1")(); err == nil || err.Error() != "OAuth state is already pending" {
		t.Fatalf("duplicate wait = %v", err)
	}
	// A success carries the code, state and iss, and consumes the state.
	if status, _, body := callbackGet(t, server.RedirectURL+"?state=s1&code=abc&iss=https%3A%2F%2Fauth.example"); status != 200 || body != "Authorization complete. You may close this window." {
		t.Fatalf("success = %d %q", status, body)
	}
	got, err := result(pending)
	if err != nil || got != (oauth.OAuthCallback{Code: "abc", State: "s1", Iss: "https://auth.example"}) {
		t.Fatalf("callback = %+v, %v", got, err)
	}
	if status, _, _ := callbackGet(t, server.RedirectURL+"?state=s1&code=abc"); status != 400 {
		t.Fatalf("replayed state = %d, want 400", status)
	}

	// An error response is reported with its description, or its code when it has none.
	described := wait("s2")
	if status, _, body := callbackGet(t, server.RedirectURL+"?state=s2&error=access_denied&error_description=User+said+no"); status != 200 || body != "Authorization failed. You may close this window.\n\nUser said no" {
		t.Fatalf("error page = %d %q", status, body)
	}
	if _, err := result(described); err == nil || err.Error() != "User said no" {
		t.Fatalf("error = %v", err)
	}
	bare := wait("s3")
	callbackGet(t, server.RedirectURL+"?state=s3&error=access_denied")
	if _, err := result(bare); err == nil || err.Error() != "access_denied" {
		t.Fatalf("error without a description = %v", err)
	}
	// A response without a code is a 400 and rejects the wait.
	missing := wait("s4")
	if status, _, body := callbackGet(t, server.RedirectURL+"?state=s4"); status != 400 || body != "Missing authorization code" {
		t.Fatalf("missing code = %d %q", status, body)
	}
	if _, err := result(missing); err == nil || err.Error() != "OAuth callback did not include an authorization code" {
		t.Fatalf("missing code error = %v", err)
	}
}

func TestOAuthCallbackServerRenderedPagesAreHTMLAndNotCached(t *testing.T) {
	server, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{RenderPage: func(page oauth.OAuthCallbackPage) string {
		return "<p>" + page.Message + "</p>"
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	status, header, body := callbackGet(t, server.RedirectURL+"?state=none")
	if status != 400 || body != "<p>Invalid or expired OAuth state</p>" {
		t.Fatalf("rendered page = %d %q", status, body)
	}
	if header.Get("Content-Type") != "text/html; charset=utf-8" || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", header)
	}

	plain, err := oauth.ListenOAuthCallbackServer(oauth.OAuthCallbackServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	_, header, _ = callbackGet(t, plain.RedirectURL+"?state=none")
	if header.Get("Content-Type") != "text/plain; charset=utf-8" || header.Get("Cache-Control") != "" {
		t.Fatalf("plain headers = %v", header)
	}
}

func TestMcpOAuthProviderInvalidateCredentialsDropsOnlyTheNamedState(t *testing.T) {
	ctx := t.Context()
	fill := func() *oauth.McpOAuthProvider {
		store := &oauth.MemoryOAuthStateStore{}
		provider, err := oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
			ServerURL: "https://s.example/mcp", RedirectURL: "http://127.0.0.1/cb", ClientMetadata: oauth.OAuthClientMetadata{ClientName: "t"},
			Store: store, OnRedirect: func(context.Context, *url.URL) error { return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		expires := 3600.0
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(provider.SaveClientInformation(ctx, oauth.OAuthClientInformationMixed{ClientID: "registered"}))
		must(provider.SaveTokens(ctx, oauth.OAuthTokens{AccessToken: "a", TokenType: "Bearer", ExpiresIn: &expires}))
		must(provider.SaveCodeVerifier(ctx, "verifier"))
		must(provider.SaveDiscoveryState(ctx, oauth.OAuthDiscoveryState{AuthorizationServerURL: "https://auth.example"}))
		if _, err := provider.State(ctx); err != nil {
			t.Fatal(err)
		}
		return provider
	}
	type remaining struct{ client, tokens, verifier, discovery, state bool }
	read := func(p *oauth.McpOAuthProvider) remaining {
		t.Helper()
		client, err := p.ClientInformation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tokens, _ := p.Tokens(ctx)
		_, verifierErr := p.CodeVerifier(ctx)
		discovery, _ := p.DiscoveryState(ctx)
		// A fresh State call creates a value when none is stored; a stored one is returned unchanged.
		return remaining{client != nil, tokens != nil, verifierErr == nil, discovery != nil, false}
	}
	for _, tc := range []struct {
		kind string
		want remaining
	}{
		{"client", remaining{false, true, true, true, false}},
		{"tokens", remaining{true, false, true, true, false}},
		{"verifier", remaining{true, true, false, true, false}},
		{"discovery", remaining{true, true, true, false, false}},
		{"all", remaining{false, false, false, false, false}},
	} {
		provider := fill()
		before, _ := provider.State(ctx)
		if err := provider.InvalidateCredentials(ctx, tc.kind); err != nil {
			t.Fatal(err)
		}
		if got := read(provider); got != tc.want {
			t.Errorf("after %q: remaining = %+v, want %+v", tc.kind, got, tc.want)
		}
		after, _ := provider.State(ctx)
		if kept := after == before; kept != (tc.kind != "all") {
			t.Errorf("after %q: OAuth state kept = %v", tc.kind, kept)
		}
	}
}

// startWait calls WaitForCallback on its own goroutine, as a Go caller of upstream's waitForCallback does, and returns once the state is
// pending (or the call failed). The returned function delivers the call's result.
func startWait(t *testing.T, server *oauth.OAuthCallbackServer, state string, path ...string) func() (oauth.OAuthCallback, error) {
	t.Helper()
	type outcome struct {
		callback oauth.OAuthCallback
		err      error
	}
	registered := make(chan struct{})
	done := make(chan outcome, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	go func() {
		callback, err := server.WaitForCallback(oauth.WithCallbackRegistered(ctx, func() { close(registered) }), state, path...)
		done <- outcome{callback, err}
	}()
	var early *outcome
	select {
	case <-registered:
	case result := <-done:
		early = &result
	}
	return func() (oauth.OAuthCallback, error) {
		if early != nil {
			return early.callback, early.err
		}
		result := <-done
		return result.callback, result.err
	}
}
