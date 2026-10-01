package mcpext_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/coding-agent/test/mcp-oauth-refresh.test.ts.

type refreshHarness struct {
	server  *oauthMcpServer
	lockDir string
	process func() (mcpext.McpOAuthServerStore, mcpext.McpAuthProvider)
}

// followPrompt is the sign-in prompt of the upstream test: the browser follows
// the authorization redirect, and the paste prompt waits until the callback arrives.
type followPrompt struct{}

func (followPrompt) ShowAuthorizationURL(u *url.URL) {
	go func() {
		if response, err := http.Get(u.String()); err == nil {
			_ = response.Body.Close()
		}
	}()
}

func (followPrompt) PromptForRedirectURL(ctx context.Context) (string, error) {
	<-ctx.Done()
	return "", nil
}

func signedIn(t *testing.T) *refreshHarness {
	t.Helper()
	server := startOAuthMcpServer(t)
	lockDir, err := os.MkdirTemp("", "pi-mcp-refresh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(lockDir) })
	// Stores sharing the credential file and lock directory stand in for separate pi processes.
	backend := &mcpext.InMemoryAuthStorageBackend{}
	process := func() (mcpext.McpOAuthServerStore, mcpext.McpAuthProvider) {
		store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(backend, lockDir).ForServer(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		provider := mcpext.NewMcpAuthProvider(mcpext.McpAuthProviderOptions{
			ServerURL: server.URL, Store: store,
			Settings:    func() (mcpext.McpOAuthSettings, error) { return mcpext.McpOAuthSettings{}, nil },
			OnChallenge: func(oauth.OAuthChallenge) {},
		})
		return store, provider
	}
	store, _ := process()
	if err := mcpext.SignInMcpServer(t.Context(), mcpext.SignInOptions{ServerURL: server.URL, Store: store, Prompt: followPrompt{}}); err != nil {
		t.Fatal(err)
	}
	return &refreshHarness{server: server, lockDir: lockDir, process: process}
}

func unauthorized(serverURL, token string, fetch mcp.McpFetch) mcp.UnauthorizedContext {
	u, _ := url.Parse(serverURL)
	return mcp.UnauthorizedContext{Response: &http.Response{StatusCode: 401, Header: http.Header{}, Body: http.NoBody}, ServerURL: u, Fetch: fetch, Token: token}
}

func TestMCPOAuthRefreshRefreshesOnceWhenSeveralProcessesFindTheSameTokenRejected(t *testing.T) {
	h := signedIn(t)
	h.server.expireAccessTokens()
	providers := make([]mcpext.McpAuthProvider, 3)
	for i := range providers {
		_, providers[i] = h.process()
	}

	// The server rotates refresh tokens: a second refresh with refresh-1 would fail with invalid_grant.
	var wg sync.WaitGroup
	errs := make(chan error, len(providers))
	for _, provider := range providers {
		wg.Go(func() {
			errs <- provider.OnUnauthorized(t.Context(), unauthorized(h.server.URL, "access-1", http.DefaultClient))
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	refreshes := 0
	for _, entry := range h.server.logEntries() {
		if entry == "token refresh" {
			refreshes++
		}
	}
	if refreshes != 1 {
		t.Fatalf("token refreshes = %d, log = %v", refreshes, h.server.logEntries())
	}
	for i, provider := range providers {
		if token, err := provider.Token(t.Context()); err != nil || token != "access-2" {
			t.Errorf("provider %d token = %q, %v", i, token, err)
		}
	}
	// The lock is released.
	entries, err := os.ReadDir(h.lockDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("lock dir = %v", entries)
	}
}

// hookFetch reports the first request a refresh sends, which proves the refresh is running.
type hookFetch struct {
	once    sync.Once
	started chan struct{}
}

func (f *hookFetch) Do(request *http.Request) (*http.Response, error) {
	f.once.Do(func() { close(f.started) })
	return http.DefaultClient.Do(request) //nolint:gosec // the request goes to the loopback test server
}

func TestMCPOAuthRefreshWaitsForARunningRefreshToSaveTheNewTokens(t *testing.T) {
	h := signedIn(t)
	h.server.expireAccessTokens()
	store, provider := h.process()

	fetch := &hookFetch{started: make(chan struct{})}
	refreshed := make(chan error, 1)
	go func() {
		refreshed <- provider.OnUnauthorized(t.Context(), unauthorized(h.server.URL, "access-1", fetch))
	}()
	// The refresh starts before Settled is called, as `onUnauthorized` starts it synchronously upstream.
	<-fetch.started
	provider.Settled(t.Context())
	state, err := store.Load(t.Context())
	if err != nil || state == nil || state.Tokens == nil || state.Tokens.AccessToken != "access-2" {
		t.Fatalf("state = %#v, %v", state, err)
	}
	if err := <-refreshed; err != nil {
		t.Fatal(err)
	}
}
