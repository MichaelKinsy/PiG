package mcpext_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Sign-in cancellation (1.1.0, #10565; packages/coding-agent/src/extensions/mcp/oauth.ts signInMcpServer): ending the
// context stops the sign-in at any step with McpSignInCancelledError. The step-by-step session cases are in
// coding/mcp_oauth_session_upstream_test.go.

func signInStore(t *testing.T, serverURL string) mcpext.McpOAuthServerStore {
	t.Helper()
	store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, t.TempDir()).ForServer("test", serverURL)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func isSignInCancelled(err error) bool {
	_, ok := errors.AsType[*mcpext.McpSignInCancelledError](err)
	return ok
}

// blockingPrompt shows nothing and waits for the redirect URL until its context ends; shown is closed once the
// authorization URL was shown.
type blockingPrompt struct{ shown chan struct{} }

func (p blockingPrompt) ShowAuthorizationURL(*url.URL) { close(p.shown) }

func (blockingPrompt) PromptForRedirectURL(ctx context.Context) (string, error) {
	<-ctx.Done()
	return "", nil
}

func TestSignInMcpServerStopsWithoutRequestsWhenItsContextAlreadyEnded(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := mcpext.SignInMcpServer(ctx, mcpext.SignInOptions{ServerURL: server.URL + "/mcp", Store: signInStore(t, server.URL+"/mcp"), Prompt: blockingPrompt{make(chan struct{})}})

	if !isSignInCancelled(err) || requests != 0 {
		t.Fatalf("err = %v with %d requests, want McpSignInCancelledError without requests", err, requests)
	}
}

func TestSignInMcpServerReportsAnAbortedAuthorizationServerRequestAsACancellation(t *testing.T) {
	arrived := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- mcpext.SignInMcpServer(ctx, mcpext.SignInOptions{ServerURL: server.URL + "/mcp", Store: signInStore(t, server.URL+"/mcp"), Prompt: blockingPrompt{make(chan struct{})}})
	}()
	<-arrived
	cancel()
	if err := <-done; !isSignInCancelled(err) {
		t.Fatalf("err = %v, want McpSignInCancelledError", err)
	}
}

func TestSignInMcpServerStopsWhileWaitingForTheBrowser(t *testing.T) {
	server := startOAuthMcpServer(t)
	prompt := blockingPrompt{make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- mcpext.SignInMcpServer(ctx, mcpext.SignInOptions{ServerURL: server.URL, Store: signInStore(t, server.URL), Prompt: prompt})
	}()
	<-prompt.shown
	cancel()
	if err := <-done; !isSignInCancelled(err) {
		t.Fatalf("err = %v, want McpSignInCancelledError", err)
	}
}

// A sign-in that fails for another reason stays a failure, not a cancellation.
func TestSignInMcpServerKeepsOtherFailuresAsFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(server.Close)
	err := mcpext.SignInMcpServer(t.Context(), mcpext.SignInOptions{ServerURL: server.URL + "/mcp", Store: signInStore(t, server.URL+"/mcp"), Prompt: blockingPrompt{make(chan struct{})}})
	if err == nil || isSignInCancelled(err) {
		t.Fatalf("err = %v, want a failure that is not a cancellation", err)
	}
}
