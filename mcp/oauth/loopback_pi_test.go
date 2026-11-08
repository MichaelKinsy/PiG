package oauth_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/mcp/test/loopback.test.ts (Pi bb266bf35). Go mechanic: Pi's `signal` is the context; OnRegistered tells the test when the
// callback server waits, where Pi's test only had to call fetch after the call returned.

func listenAt(t *testing.T, serverURL string, documents bool) oauth.McpLoopbackCallback {
	t.Helper()
	options := oauth.ListenForLoopbackSignInOptions{ServerURL: serverURL, Store: &memoryStore{}}
	if documents {
		options.ClientMetadataDocuments = &oauth.McpClientMetadataDocuments{BaseURL: "https://app.example/oauth", ListsRedirectURL: func(string) bool { return true }}
	}
	loopback, err := oauth.ListenForLoopbackSignIn(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loopback.Callback.Close() })
	return loopback
}

func pendingRedirect(redirectURL string) oauth.McpSignInRedirect {
	return oauth.McpSignInRedirect{AuthorizationURL: "https://auth.example/authorize", RedirectURL: redirectURL, State: "state-1"}
}

type waitResult struct {
	response oauth.McpAuthorizationResponse
	err      error
}

// startWaiting runs WaitForLoopbackSignIn on its own goroutine and returns once the callback server waits for the response.
func startWaiting(t *testing.T, ctx context.Context, options oauth.WaitForLoopbackSignInOptions) <-chan waitResult {
	t.Helper()
	registered := make(chan struct{})
	options.OnRegistered = func() { close(registered) }
	done := make(chan waitResult, 1)
	go func() {
		response, err := oauth.WaitForLoopbackSignIn(ctx, options)
		done <- waitResult{response, err}
	}()
	select {
	case <-registered:
	case r := <-done:
		done <- r
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not register")
	}
	return done
}

func TestLoopbackSignInCompletesThroughTheLoopbackCallbackServer(t *testing.T) {
	server := startAuthorizationServer(t, authServerOptions{})
	store := &memoryStore{}
	loopback, err := oauth.ListenForLoopbackSignIn(t.Context(), oauth.ListenForLoopbackSignInOptions{ServerURL: server.serverURL, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loopback.Callback.Close() })
	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/callback$`).MatchString(loopback.RedirectURL) {
		t.Fatalf("redirect = %q", loopback.RedirectURL)
	}
	options := oauth.McpSignInOptions{
		McpOAuthClientOptions: oauth.McpOAuthClientOptions{ServerURL: server.serverURL, Store: store, ClientName: "app"},
		RedirectURL:           loopback.RedirectURL,
	}
	authorization := startRedirect(t, options)
	waiting := startWaiting(t, t.Context(), oauth.WaitForLoopbackSignInOptions{Callback: loopback.Callback, Authorization: authorization})
	response, err := http.Get(authorizeInBrowser(t, authorization.AuthorizationURL))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	got := <-waiting
	if got.err != nil {
		t.Fatal(got.err)
	}
	if err := oauth.CompleteMcpSignIn(t.Context(), oauth.McpCompleteSignInOptions{McpSignInOptions: options, Authorization: authorization, Response: got.response}); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(t.Context())
	if state.Tokens.AccessToken != "access-1" {
		t.Fatalf("tokens = %+v", state.Tokens)
	}
	registrations := server.registered()
	if len(registrations) != 1 || registrations[0]["application_type"] != "native" {
		t.Fatalf("registrations = %v", registrations)
	}
}

func TestLoopbackSignInRejectsAResponseOnAnotherRedirectPath(t *testing.T) {
	serverURL := "https://mcp.example/mcp"
	loopback := listenAt(t, serverURL, true)
	waiting := startWaiting(t, t.Context(), oauth.WaitForLoopbackSignInOptions{Callback: loopback.Callback, Authorization: pendingRedirect(loopback.RedirectURL + "/" + callbackIDOf(serverURL))})
	// A mixed-up authorization server redirects to the shared callback path.
	response, err := http.Get(loopback.RedirectURL + "?code=c&state=state-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	got := <-waiting
	if got.err == nil || !strings.Contains(got.err.Error(), "arrived on another redirect URI") {
		t.Fatalf("error = %v", got.err)
	}
}

func TestLoopbackSignInTakesAPastedRedirectURLWhileWaitingForTheCallback(t *testing.T) {
	loopback := listenAt(t, "https://mcp.example/mcp", false)
	waiting := startWaiting(t, t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: pendingRedirect(loopback.RedirectURL),
		PromptForRedirectURL: func(context.Context) (string, error) { return loopback.RedirectURL + "?code=pasted&state=state-1", nil },
	})
	got := <-waiting
	if got.err != nil || got.response != (oauth.McpAuthorizationResponse{Code: "pasted", State: "state-1"}) {
		t.Fatalf("response = %+v, %v", got.response, got.err)
	}
}

func TestLoopbackSignInCancelsTheWaitAndAnEmptyPasteCancelsToo(t *testing.T) {
	loopback := listenAt(t, "https://mcp.example/mcp", false)
	ctx, cancel := context.WithCancel(t.Context())
	waiting := startWaiting(t, ctx, oauth.WaitForLoopbackSignInOptions{Callback: loopback.Callback, Authorization: pendingRedirect(loopback.RedirectURL)})
	cancel()
	expectCancelled(t, (<-waiting).err)

	second := pendingRedirect(loopback.RedirectURL)
	second.State = "state-2"
	pasted := startWaiting(t, t.Context(), oauth.WaitForLoopbackSignInOptions{
		Callback: loopback.Callback, Authorization: second,
		PromptForRedirectURL: func(context.Context) (string, error) { return "", nil },
	})
	expectCancelled(t, (<-pasted).err)
}
