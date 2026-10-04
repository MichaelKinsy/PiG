package mcpext_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Ports packages/coding-agent/test/mcp-oauth-refresh.test.ts ("MCP OAuth client ID metadata documents", #10302).

var cimdSettings = mcpext.McpOAuthSettings{ClientRegistration: "cimd"}

type cimdHarness struct {
	server *oauthMcpServer
	store  mcpext.McpOAuthServerStore
}

func startCimdServer(t *testing.T, options oauthMcpServerOptions) *cimdHarness {
	t.Helper()
	server := startOAuthMcpServer(t, options)
	store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "").ForServer("test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &cimdHarness{server: server, store: store}
}

func (h *cimdHarness) signIn(t *testing.T, settings mcpext.McpOAuthSettings, prompt mcpext.McpSignInPrompt) error {
	t.Helper()
	if prompt == nil {
		prompt = followPrompt{}
	}
	return mcpext.SignInMcpServer(t.Context(), mcpext.SignInOptions{ServerURL: h.server.URL, Store: h.store, Settings: settings, Prompt: prompt})
}

func (h *cimdHarness) storedClientID(t *testing.T) (string, bool) {
	t.Helper()
	state, err := h.store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.ClientInformation == nil {
		return "", false
	}
	return state.ClientInformation.ClientID, true
}

func TestMCPOAuthCIMDRegistersDynamicallyByDefaultEvenWhenTheServerSupportsDocuments(t *testing.T) {
	h := startCimdServer(t, oauthMcpServerOptions{Cimd: true, IssParameter: true})
	if err := h.signIn(t, mcpext.McpOAuthSettings{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := h.server.registrationCount(); got != 1 {
		t.Fatalf("registrations = %d", got)
	}
	if got := h.server.recordedAuthorizations()[0].Get("client_id"); got != "client-1" {
		t.Fatalf("client_id = %q", got)
	}
}

func TestMCPOAuthCIMDUsesPisDocumentWhenAuthorizationResponsesNameTheirIssuer(t *testing.T) {
	h := startCimdServer(t, oauthMcpServerOptions{Cimd: true, IssParameter: true})
	if err := h.signIn(t, cimdSettings, nil); err != nil {
		t.Fatal(err)
	}
	authorization := h.server.recordedAuthorizations()[0]
	if got := authorization.Get("client_id"); got != "https://pi.dev/oauth/client.json" {
		t.Fatalf("client_id = %q", got)
	}
	redirect, err := url.Parse(authorization.Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	if got := redirect.Hostname() + redirect.Path; got != "127.0.0.1/callback" || redirect.Port() == "" {
		t.Fatalf("redirect = %s", redirect)
	}
	if got := h.server.registrationCount(); got != 0 {
		t.Fatalf("registrations = %d", got)
	}
	tokens := h.server.recordedTokenRequests()
	if tokens[0].Get("client_id") != "https://pi.dev/oauth/client.json" || tokens[0].Get("redirect_uri") != redirect.String() {
		t.Fatalf("token request = %v", tokens[0])
	}
	// The document is not stored, so signing in again refreshes the tokens instead of discarding them.
	if id, ok := h.storedClientID(t); ok {
		t.Fatalf("stored client = %q", id)
	}
	if err := h.signIn(t, cimdSettings, nil); err != nil {
		t.Fatal(err)
	}
	if got := len(h.server.recordedAuthorizations()); got != 1 {
		t.Fatalf("authorizations = %d", got)
	}
	tokens = h.server.recordedTokenRequests()
	if tokens[1].Get("grant_type") != "refresh_token" || tokens[1].Get("client_id") != "https://pi.dev/oauth/client.json" {
		t.Fatalf("second token request = %v", tokens[1])
	}
}

func TestMCPOAuthCIMDUsesADocumentAndCallbackPathSpecificToTheMCPServerWithoutTheIssParameter(t *testing.T) {
	h := startCimdServer(t, oauthMcpServerOptions{Cimd: true})
	if err := h.signIn(t, cimdSettings, nil); err != nil {
		t.Fatal(err)
	}
	// Computed like Codex: the first 9 bytes of the SHA-256 of the MCP server URL.
	sum := sha256.Sum256([]byte(h.server.URL))
	id := base64.RawURLEncoding.EncodeToString(sum[:9])
	authorization := h.server.recordedAuthorizations()[0]
	if got := authorization.Get("client_id"); got != "https://pi.dev/oauth/"+id+"/client.json" {
		t.Fatalf("client_id = %q", got)
	}
	redirect, err := url.Parse(authorization.Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	if redirect.Path != "/callback/"+id {
		t.Fatalf("redirect path = %q", redirect.Path)
	}
	if got := h.server.registrationCount(); got != 0 {
		t.Fatalf("registrations = %d", got)
	}
	if got := h.server.recordedTokenRequests()[0].Get("redirect_uri"); got != redirect.String() {
		t.Fatalf("token redirect_uri = %q", got)
	}
}

func TestMCPOAuthCIMDReplacesARegisteredClientWhenSwitchingToTheDocument(t *testing.T) {
	h := startCimdServer(t, oauthMcpServerOptions{Cimd: true, IssParameter: true})
	if err := h.signIn(t, mcpext.McpOAuthSettings{}, nil); err != nil {
		t.Fatal(err)
	}
	if id, _ := h.storedClientID(t); id != "client-1" {
		t.Fatalf("stored client = %q", id)
	}
	if err := h.signIn(t, cimdSettings, nil); err != nil {
		t.Fatal(err)
	}
	// The registered client's tokens are not refreshed with another client.
	var clients []string
	for _, authorization := range h.server.recordedAuthorizations() {
		clients = append(clients, authorization.Get("client_id"))
	}
	if want := []string{"client-1", "https://pi.dev/oauth/client.json"}; !reflect.DeepEqual(clients, want) {
		t.Fatalf("authorization clients = %q, want %q", clients, want)
	}
	if id, ok := h.storedClientID(t); ok {
		t.Fatalf("stored client = %q", id)
	}
}

// pastePrompt pastes the redirect URL of the shown authorization URL with its path replaced by /callback.
type pastePrompt struct {
	mu    sync.Mutex
	shown *url.URL
	ready chan struct{}
}

func (p *pastePrompt) ShowAuthorizationURL(u *url.URL) {
	p.mu.Lock()
	p.shown = u
	p.mu.Unlock()
	close(p.ready)
}

func (p *pastePrompt) PromptForRedirectURL(ctx context.Context) (string, error) {
	select {
	case <-p.ready:
	case <-ctx.Done():
		return "", nil
	}
	p.mu.Lock()
	shown := p.shown
	p.mu.Unlock()
	redirect, err := url.Parse(shown.Query().Get("redirect_uri"))
	if err != nil {
		return "", err
	}
	redirect.Path = "/callback"
	redirect.RawQuery = "code=code-1&state=" + shown.Query().Get("state")
	return redirect.String(), nil
}

func TestMCPOAuthCIMDAcceptsTheAuthorizationResponseOnlyOnTheServerSpecificRedirectURI(t *testing.T) {
	// A mixed-up authorization server redirects to the shared callback path.
	mixedUp := startCimdServer(t, oauthMcpServerOptions{Cimd: true, RedirectPath: "/callback"})
	if err := mixedUp.signIn(t, cimdSettings, nil); err == nil || !strings.Contains(err.Error(), "arrived on another redirect URI") {
		t.Fatalf("mixed-up sign-in = %v", err)
	}

	// The same for a redirect URL pasted from the browser.
	byPaste := startCimdServer(t, oauthMcpServerOptions{Cimd: true})
	if err := byPaste.signIn(t, cimdSettings, &pastePrompt{ready: make(chan struct{})}); err == nil || !strings.Contains(err.Error(), "does not match this sign-in's redirect URI") {
		t.Fatalf("pasted sign-in = %v", err)
	}
}

func TestMCPOAuthCIMDFailsInsteadOfRegisteringWhenTheServerDoesNotSupportDocuments(t *testing.T) {
	h := startCimdServer(t, oauthMcpServerOptions{IssParameter: true})
	if err := h.signIn(t, cimdSettings, nil); err == nil || !strings.Contains(err.Error(), "does not support Client ID Metadata Documents") {
		t.Fatalf("sign-in = %v", err)
	}
	if got := h.server.registrationCount(); got != 0 {
		t.Fatalf("registrations = %d", got)
	}
}
