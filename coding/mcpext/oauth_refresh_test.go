package mcpext_test

// pi: packages/coding-agent/src/extensions/mcp/oauth.ts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
		store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(backend, lockDir).ForServer("test", server.URL)
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

// MCP OAuth sign-in

func signInWithSettings(t *testing.T, options oauthMcpServerOptions, settings func(serverURL string) mcpext.McpOAuthSettings) error {
	t.Helper()
	server := startOAuthMcpServer(t, options)
	store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "").ForServer("test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	s := mcpext.McpOAuthSettings{}
	if settings != nil {
		s = settings(server.URL)
	}
	return mcpext.SignInMcpServer(t.Context(), mcpext.SignInOptions{ServerURL: server.URL, Store: store, Settings: s, Prompt: followPrompt{}})
}

func TestMCPOAuthSignInRejectsAnAuthorizationResponseFromAnotherIssuer(t *testing.T) {
	err := signInWithSettings(t, oauthMcpServerOptions{Iss: "https://attacker.example"}, nil)
	if _, ok := errors.AsType[*oauth.OAuthIssuerMismatchError](err); !ok {
		t.Fatalf("err = %v", err)
	}
}

// recordingPrompt is followPrompt that records the authorization URLs it opens.
type recordingPrompt struct {
	followPrompt
	mu     sync.Mutex
	opened []*url.URL
}

func (p *recordingPrompt) ShowAuthorizationURL(u *url.URL) {
	p.mu.Lock()
	p.opened = append(p.opened, u)
	p.mu.Unlock()
	p.followPrompt.ShowAuthorizationURL(u)
}

func TestMCPOAuthSignInKeepsTheGrantedScopeWhenTheServerAsksForMore(t *testing.T) {
	ctx := t.Context()
	server := startOAuthMcpServer(t)
	store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "").ForServer("test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	prompt := &recordingPrompt{}
	signInWith := func(challenge oauth.OAuthChallenge) error {
		return mcpext.SignInMcpServer(ctx, mcpext.SignInOptions{
			ServerURL: server.URL, Store: store, Settings: mcpext.McpOAuthSettings{}, Challenge: &challenge, Prompt: prompt,
		})
	}
	grantedScope := func() string {
		state, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if state == nil || state.Tokens == nil {
			return "<no tokens>"
		}
		return state.Tokens.Scope
	}

	if err := signInWith(oauth.OAuthChallenge{Scope: "issues:read"}); err != nil {
		t.Fatal(err)
	}
	// The token response names no scope, so the grant has the requested one.
	if got := grantedScope(); got != "issues:read" {
		t.Fatalf("granted scope = %q, want %q", got, "issues:read")
	}
	// The step-up challenge lists only the missing scope. Requesting just that would lose
	// issues:read, so the next request would ask for sign-in again.
	if err := signInWith(oauth.OAuthChallenge{Error: "insufficient_scope", Scope: "issues:write"}); err != nil {
		t.Fatal(err)
	}
	prompt.mu.Lock()
	scopes := make([]string, 0, len(prompt.opened))
	for _, u := range prompt.opened {
		scopes = append(scopes, u.Query().Get("scope"))
	}
	prompt.mu.Unlock()
	if want := []string{"issues:read", "issues:read issues:write"}; !slices.Equal(scopes, want) {
		t.Fatalf("requested scopes = %q, want %q", scopes, want)
	}
	if got := grantedScope(); got != "issues:read issues:write" {
		t.Fatalf("granted scope = %q, want %q", got, "issues:read issues:write")
	}
}

// #10172
func TestMCPOAuthSignInUsesTheConfiguredAuthorizationServerMetadataURL(t *testing.T) {
	err := signInWithSettings(t, oauthMcpServerOptions{}, func(serverURL string) mcpext.McpOAuthSettings {
		base, err := url.Parse(serverURL)
		if err != nil {
			t.Fatal(err)
		}
		return mcpext.McpOAuthSettings{AuthServerMetadataURL: base.ResolveReference(&url.URL{Path: "/missing"})}
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404 loading authorization server metadata") {
		t.Fatalf("err = %v", err)
	}
}

// #10565 (oauth.ts signInMcpServer): ending the context stops the sign-in at any step with McpSignInCancelledError, also while the authorization server hangs, and a context that already ended never starts it.
func TestMCPOAuthSignInEndsWithACancellationWhenItsContextEnds(t *testing.T) {
	server := startOAuthMcpServer(t)
	server.stallPath("/.well-known/oauth-authorization-server")
	store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, t.TempDir()).ForServer("test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	options := mcpext.SignInOptions{ServerURL: server.URL, Store: store, Prompt: followPrompt{}}

	ended, end := context.WithCancel(t.Context())
	end()
	if err := mcpext.SignInMcpServer(ended, options); !isSignInCancelled(err) {
		t.Fatalf("sign-in with an ended context = %v", err)
	}
	if server.stalledCount() != 0 {
		t.Fatal("a sign-in with an ended context made a request")
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- mcpext.SignInMcpServer(ctx, options) }()
	deadline := time.Now().Add(10 * time.Second)
	for server.stalledCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the sign-in never reached the stalled request")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !isSignInCancelled(err) {
			t.Fatalf("sign-in error = %v, want McpSignInCancelledError", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the sign-in ignored its cancelled context")
	}
}
