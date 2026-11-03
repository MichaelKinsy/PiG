package oauth_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/test/loopback.test.ts.
//
// pig mapping: awaiting the wait before the browser follows the redirect is `OnPending`: the wait is registered
// when it runs.

func listenAt(t *testing.T, serverURL string, withDocuments bool) *oauth.McpLoopbackCallback {
	t.Helper()
	options := oauth.ListenForLoopbackSignInOptions{ServerURL: serverURL, Store: memoryStore(), Settings: oauth.McpOAuthSettings{}}
	if withDocuments {
		options.ClientMetadataDocuments = &oauth.McpClientMetadataDocuments{BaseURL: "https://app.example/oauth", ListsRedirectURL: func(string) bool { return true }}
	}
	loopback, err := oauth.ListenForLoopbackSignIn(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loopback.Callback.Close() })
	return loopback
}

func pending(redirectURL string) oauth.McpSignInRedirect {
	return oauth.McpSignInRedirect{Kind: "redirect", AuthorizationURL: "https://auth.example/authorize", RedirectURL: redirectURL, State: "state-1"}
}

type waitResult struct {
	response oauth.McpAuthorizationResponse
	err      error
}

// waitInBackground runs WaitForLoopbackSignIn and returns once the wait is registered.
func waitInBackground(ctx context.Context, t *testing.T, options oauth.WaitForLoopbackSignInOptions) <-chan waitResult {
	t.Helper()
	registered := make(chan struct{})
	options.OnPending = func() { close(registered) }
	done := make(chan waitResult, 1)
	go func() {
		response, err := oauth.WaitForLoopbackSignIn(ctx, options)
		done <- waitResult{response, err}
	}()
	select {
	case <-registered:
	case r := <-done:
		done <- r
	case <-time.After(10 * time.Second):
		t.Fatal("the wait never registered")
	}
	return done
}

func receive(t *testing.T, done <-chan waitResult) waitResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("the wait never ended")
		return waitResult{}
	}
}

func get(t *testing.T, target string) {
	t.Helper()
	response, err := http.Get(target) //nolint:gosec // loopback test server
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestLoopbackSignInCompletesThroughTheLoopbackCallbackServer(t *testing.T) {
	server := startAuthorizationServer(t, authorizationServerOptions{})
	store := memoryStore()
	loopback, err := oauth.ListenForLoopbackSignIn(t.Context(), oauth.ListenForLoopbackSignInOptions{ServerURL: server.ServerURL, Store: store, Settings: oauth.McpOAuthSettings{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loopback.Callback.Close() })
	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/callback$`).MatchString(loopback.RedirectURL) {
		t.Fatalf("redirect URL = %q", loopback.RedirectURL)
	}

	options := signInOptions(server, oauth.McpOAuthSettings{})
	options.Store, options.RedirectURL = store, loopback.RedirectURL
	authorization := startRedirect(t, options)
	done := waitInBackground(t.Context(), t, oauth.WaitForLoopbackSignInOptions{Callback: loopback.Callback, Authorization: authorization})
	get(t, authorizeInBrowser(t, authorization.AuthorizationURL))
	result := receive(t, done)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := complete(t.Context(), options, authorization, result.response); err != nil {
		t.Fatal(err)
	}

	if got := loadState(t, store).Tokens.AccessToken; got != "access-1" {
		t.Fatalf("access token = %q, want access-1", got)
	}
	expectRegistration(t, server, 0, map[string]any{"redirect_uris": []any{loopback.RedirectURL}, "application_type": "native"})
}

// mutation-checked: not passing the redirect path to WaitForCallback fails it.
func TestLoopbackSignInRejectsAResponseOnAnotherRedirectPath(t *testing.T) {
	const serverURL = "https://mcp.example/mcp"
	loopback := listenAt(t, serverURL, true)
	done := waitInBackground(t.Context(), t, oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: pending(loopback.RedirectURL + "/" + serverCallbackID(serverURL)),
	})
	// A mixed-up authorization server redirects to the shared callback path.
	get(t, loopback.RedirectURL+"?code=c&state=state-1")
	expectError(t, receive(t, done).err, "arrived on another redirect URI")
}

// mutation-checked: not serving the server-specific path fails it.
func TestLoopbackSignInServesTheServerSpecificRedirectPathOfTheDocuments(t *testing.T) {
	const serverURL = "https://mcp.example/mcp"
	loopback := listenAt(t, serverURL, true)
	specific := loopback.RedirectURL + "/" + serverCallbackID(serverURL)
	done := waitInBackground(t.Context(), t, oauth.WaitForLoopbackSignInOptions{Callback: loopback.Callback, Authorization: pending(specific)})
	get(t, specific+"?code=c&state=state-1")
	result := receive(t, done)
	if want := (oauth.McpAuthorizationResponse{Code: "c", State: "state-1"}); result.err != nil || result.response != want {
		t.Fatalf("response = %+v, %v, want %+v", result.response, result.err, want)
	}
}

// mutation-checked: never asking for the redirect URL deadlocks it.
func TestLoopbackSignInTakesAPastedRedirectURLWhileWaitingForTheCallback(t *testing.T) {
	loopback := listenAt(t, "https://mcp.example/mcp", false)
	response, err := oauth.WaitForLoopbackSignIn(t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: pending(loopback.RedirectURL),
		PromptForRedirectURL: func(context.Context) (string, error) { return loopback.RedirectURL + "?code=pasted&state=state-1", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (oauth.McpAuthorizationResponse{Code: "pasted", State: "state-1"}); response != want {
		t.Fatalf("response = %+v, want %+v", response, want)
	}
}

// mutation-checked: not reporting an ended context as McpSignInCancelledError fails it.
func TestLoopbackSignInCancelsTheWaitAndAnEmptyPasteCancelsToo(t *testing.T) {
	loopback := listenAt(t, "https://mcp.example/mcp", false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := waitInBackground(ctx, t, oauth.WaitForLoopbackSignInOptions{Callback: loopback.Callback, Authorization: pending(loopback.RedirectURL)})
	cancel()
	expectCancelled(t, receive(t, done).err)

	second := pending(loopback.RedirectURL)
	second.State = "state-2"
	_, err := oauth.WaitForLoopbackSignIn(t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: second,
		PromptForRedirectURL: func(context.Context) (string, error) { return "", nil },
	})
	expectCancelled(t, err)
}

var pastedAuthorization = pending("http://127.0.0.1:1234/callback/server-id")

// mutation-checked: dropping the iss parameter fails it.
func TestPastedRedirectURLsReadTheCodeStateAndIssuer(t *testing.T) {
	response, err := oauth.ResponseFromRedirectURL(" "+pastedAuthorization.RedirectURL+"?code=c&state=state-1&iss=x ", pastedAuthorization)
	if err != nil {
		t.Fatal(err)
	}
	if want := (oauth.McpAuthorizationResponse{Code: "c", State: "state-1", Iss: "x"}); response != want {
		t.Fatalf("response = %+v, want %+v", response, want)
	}
}

func TestPastedRedirectURLsRejectAnotherStateAnotherRedirectPathAndErrors(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{pastedAuthorization.RedirectURL + "?code=c&state=other", "belongs to a different sign-in"},
		{"http://127.0.0.1:1234/callback?code=c&state=state-1", "does not match this sign-in's redirect URI"},
		{pastedAuthorization.RedirectURL + "?error=access_denied", "access_denied"},
		{"code=c", "Expected the full redirect URL"},
	} {
		_, err := oauth.ResponseFromRedirectURL(tc.input, pastedAuthorization)
		expectError(t, err, tc.want)
	}
}
