//go:build !pig_strip_mcp

package coding

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Ports packages/coding-agent/test/suite/agent-session-mcp-oauth.test.ts (Pi 0.99.2): the OAuth sign-in through `/mcp`
// commands run by the Session.

type mcpOAuthSession struct {
	*mcpSession
	server  *mcpOAuthServer
	backend *mcpext.InMemoryAuthStorageBackend
	openedN []*url.URL
	mu      sync.Mutex
}

func (s *mcpOAuthSession) openedURLs() []*url.URL {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*url.URL(nil), s.openedN...)
}

// setupMCPOAuth is setup(browser, oauth) of the test: a browser that "follow"s the authorization redirect to the loopback
// callback, or cannot reach it, so the user "paste"s the redirect URL.
func setupMCPOAuth(t *testing.T, browser string, oauth *extension.McpOAuthConfig) *mcpOAuthSession {
	t.Helper()
	server := startMcpOAuthServer(t)
	backend := &mcpext.InMemoryAuthStorageBackend{}
	s := &mcpOAuthSession{server: server, backend: backend}
	redirectLocation := make(chan string, 1)
	entry := mcpext.McpServerEntry{Name: "issues", Config: extension.McpServerConfig{URL: server.URL, Exposure: extension.McpExposureDirect, OAuth: oauth}, Source: "test"}
	entries := []mcpext.McpServerEntry{entry}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(target string, c *http.Client) *http.Response {
		// A browser outlives the sign-in it completes: t.Context is cancelled before the cleanup that waits for this
		// request, so the request must not use it.
		request, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodGet, target, nil)
		if err != nil {
			t.Error(err)
			return nil
		}
		response, err := c.Do(request)
		if err != nil {
			t.Error(err)
			return nil
		}
		return response
	}
	var browsers sync.WaitGroup
	t.Cleanup(browsers.Wait)
	opts := mcpSessionOptions{
		onlyMCP: true, realTransport: true, config: &entries,
		credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(backend, ""),
		openURL: func(target string) {
			parsed, _ := url.Parse(target)
			s.mu.Lock()
			s.openedN = append(s.openedN, parsed)
			s.mu.Unlock()
			browsers.Go(func() {
				if browser == "follow" {
					// The browser follows the authorization redirect to the loopback callback.
					if response := get(target, http.DefaultClient); response != nil {
						_, _ = io.Copy(io.Discard, response.Body)
						_ = response.Body.Close()
					}
					return
				}
				// The browser cannot reach the callback; the user pastes the redirect URL.
				if response := get(target, client); response != nil {
					_ = response.Body.Close()
					redirectLocation <- response.Header.Get("Location")
				}
			})
		},
		// The paste prompt waits until sign-in completes unless the user pastes the redirect URL.
		uiInput: func(ctx context.Context, _, _ string) string {
			if browser == "paste" {
				select {
				case location := <-redirectLocation:
					return location
				case <-ctx.Done():
					return ""
				}
			}
			<-ctx.Done()
			return ""
		},
	}
	s.mcpSession = newMCPSession(t, extension.McpExposureDirect, nil, opts)
	return s
}

// callWhoami scripts a model call of mcp__issues__whoami and returns its tool result.
func (s *mcpOAuthSession) callWhoami(t *testing.T) agent.ToolResultMessage {
	t.Helper()
	s.respond(mcpToolCalls(mcpToolCall{"mcp__issues__whoami", ai.JsonObject{}}), mcpDone())
	before := len(s.session.Messages())
	s.prompt(t, "who am i")
	for _, message := range s.session.Messages()[before:] {
		if message.ToolResult != nil {
			return *message.ToolResult
		}
	}
	t.Fatal("no tool result")
	return agent.ToolResultMessage{}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestAgentSessionMCPOAuthSignsInThroughTheBrowserRefreshesExpiredTokensAndSignsOut(t *testing.T) {
	h := setupMCPOAuth(t, "follow", nil)

	h.prompt(t, "/mcp")
	// Startup problems are reported once, pointing to /mcp.
	if !slices.Contains(h.notes.all(), "MCP servers need attention:\n  issues: needs sign-in\nRun /mcp to fix.") {
		t.Errorf("notifications = %q", h.notes.all())
	}
	requireEqual(t, "last notification", h.notes.last(), "issues: needs sign-in, run /mcp login issues (direct)")

	h.prompt(t, "/mcp login issues")
	requireEqual(t, "last notification", h.notes.last(), `Signed in to MCP server "issues" (1 tools).`)
	requireEqual(t, "server log", h.server.logEntries(), []string{"401 none", "register", "token code"})
	var stored string
	_ = h.backend.WithLock(func(current string, _ bool) (*string, error) { stored = current; return nil, nil })
	if !strings.Contains(stored, `"access_token": "access-1"`) {
		t.Errorf("stored credentials = %s", stored)
	}

	requireEqual(t, "whoami", mcpText(h.callWhoami(t)), "token access-1")

	// An expired access token is refreshed without user interaction.
	h.server.expireAccessTokens()
	requireEqual(t, "whoami", mcpText(h.callWhoami(t)), "token access-2")
	log := h.server.logEntries()
	requireEqual(t, "server log tail", log[len(log)-3:], []string{"401 access-1", "token refresh", "call access-2"})

	// A token past its expiry is refreshed before the request, without a 401 round trip.
	expireStoredTokens(t, h.backend)
	requireEqual(t, "whoami", mcpText(h.callWhoami(t)), "token access-3")
	log = h.server.logEntries()
	requireEqual(t, "server log tail", log[len(log)-2:], []string{"token refresh", "call access-3"})

	h.prompt(t, "/mcp logout issues")
	requireEqual(t, "last notification", h.notes.last(), `Signed out of MCP server "issues".`)
	result := h.callWhoami(t)
	if !result.IsError {
		t.Error("whoami after sign-out is not an error")
	}
	requireEqual(t, "whoami", mcpText(result), `MCP server "issues" requires sign-in. Run /mcp to sign in.`)
}

var tokensExpireAt = regexp.MustCompile(`"tokensExpireAt":\s*\d+`)

// expireStoredTokens sets tokensExpireAt of every stored server state to one second ago.
func expireStoredTokens(t *testing.T, backend *mcpext.InMemoryAuthStorageBackend) {
	t.Helper()
	err := backend.WithLock(func(current string, _ bool) (*string, error) {
		next := tokensExpireAt.ReplaceAllString(current, `"tokensExpireAt": 1`)
		return &next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// shutdown emits session_shutdown like quitting does; it returns how long the handlers took.
func (s *mcpOAuthSession) shutdown() time.Duration {
	start := time.Now()
	s.session.EmitSessionShutdown("quit")
	return time.Since(start)
}

// Ports "cancels a sign-in waiting on <path> when the session shuts down" (1.1.0, #10565;
// packages/coding-agent/test/suite/agent-session-mcp-oauth.test.ts).
func TestAgentSessionMCPOAuthCancelsASignInWaitingOnTheAuthorizationServerWhenTheSessionShutsDown(t *testing.T) {
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/token"} {
		t.Run(path, func(t *testing.T) {
			h := setupMCPOAuth(t, "follow", nil)
			h.server.stallPath(path)
			login := make(chan error, 1)
			go func() { login <- h.session.Prompt(t.Context(), "/mcp login issues", nil) }()
			mcpWaitFor(t, "the sign-in to reach "+path, func() bool { return len(h.server.stalledRequests()) == 1 })

			// Shutdown waits for the sign-in to clean up, which aborting makes immediate.
			if took := h.shutdown(); took >= 2*time.Second {
				t.Fatalf("shutdown took %s, want under 2s", took)
			}
			if err := <-login; err != nil {
				t.Fatal(err)
			}
			// The request is aborted instead of left open, and the ended session reports nothing.
			select {
			case <-h.server.stalledRequests()[0].closed:
			case <-time.After(5 * time.Second):
				t.Fatal("the stalled request stayed open after the session ended")
			}
			for _, note := range h.notes.all() {
				if strings.HasPrefix(note, "Sign-in") {
					t.Errorf("the ended session reported %q", note)
				}
			}
		})
	}
}

// Ports "closes the session without refreshing an expiring token" (1.1.0, #10565;
// packages/coding-agent/test/suite/agent-session-mcp-oauth.test.ts).
func TestAgentSessionMCPOAuthClosesTheSessionWithoutRefreshingAnExpiringToken(t *testing.T) {
	h := setupMCPOAuth(t, "follow", nil)
	h.prompt(t, "/mcp login issues")
	requireEqual(t, "last notification", h.notes.last(), `Signed in to MCP server "issues" (1 tools).`)
	// Still accepted, but close enough to expiry that the next request would refresh it first.
	err := h.backend.WithLock(func(current string, _ bool) (*string, error) {
		next := tokensExpireAt.ReplaceAllString(current, fmt.Sprintf(`"tokensExpireAt": %d`, time.Now().UnixMilli()+10_000))
		return &next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server.stallPath("/token")

	if took := h.shutdown(); took >= 2*time.Second {
		t.Fatalf("shutdown took %s, want under 2s", took)
	}
	requireEqual(t, "stalled requests", len(h.server.stalledRequests()), 0)
	requireEqual(t, "DELETE tokens", h.server.deleteTokens(), []string{"access-1"})
}

// Ports "gives up on pi mcp login after --timeout, also while the authorization server hangs" (1.1.0, #10565;
// packages/coding-agent/test/suite/agent-session-mcp-oauth.test.ts).
func TestAgentSessionMCPOAuthGivesUpOnPigMcpLoginAfterTimeoutAlsoWhileTheAuthorizationServerHangs(t *testing.T) {
	server := startMcpOAuthServer(t)
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(`{"mcpServers":{"issues":{"url":"`+server.URL+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	login := func() (int, []string) {
		var output []string
		record := func(line string) { output = append(output, line) }
		exitCode := mcpext.RunMcpCommand(t.Context(), []string{"login", "issues", "--timeout", "0.5"}, mcpext.McpCommandOptions{
			Cwd: agentDir, AgentDir: agentDir,
			Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
			// The user never approves in the browser.
			OpenURL: func(string) {},
			Log:     record, Error: record,
		})
		return exitCode, output
	}

	exitCode, output := login()
	requireEqual(t, "exit code", exitCode, 1)
	if last := output[len(output)-1]; !strings.Contains(last, "was cancelled or not completed within") {
		t.Errorf("unapproved: last line = %q", last)
	}

	// #10565
	server.stallPath("/.well-known/oauth-authorization-server")
	exitCode, output = login()
	requireEqual(t, "exit code", exitCode, 1)
	if last := output[len(output)-1]; !strings.Contains(last, "was cancelled or not completed within") {
		t.Errorf("stalled: last line = %q", last)
	}
}

func TestAgentSessionMCPOAuthAcceptsAPastedRedirectURLWhenTheBrowserCannotReachTheCallback(t *testing.T) {
	h := setupMCPOAuth(t, "paste", nil)

	h.prompt(t, "/mcp login")
	requireEqual(t, "last notification", h.notes.last(), `Signed in to MCP server "issues" (1 tools).`)
	requireEqual(t, "whoami", mcpText(h.callWhoami(t)), "token access-1")
	if !slices.Contains(h.server.logEntries(), "token code") {
		t.Errorf("server log = %v", h.server.logEntries())
	}
}

func TestAgentSessionMCPOAuthUsesTheConfiguredCallbackURLAndScope(t *testing.T) {
	callbackURL := "http://localhost:" + strconv.Itoa(freePort(t)) + "/callback"
	h := setupMCPOAuth(t, "follow", &extension.McpOAuthConfig{CallbackURL: callbackURL, Scope: "issues:read"})

	h.prompt(t, "/mcp login issues")
	requireEqual(t, "last notification", h.notes.last(), `Signed in to MCP server "issues" (1 tools).`)
	opened := h.openedURLs()
	requireEqual(t, "redirect_uri", opened[0].Query().Get("redirect_uri"), callbackURL)
	requireEqual(t, "scope", opened[0].Query().Get("scope"), "issues:read")
	requireEqual(t, "whoami", mcpText(h.callWhoami(t)), "token access-1")
}

// #10226
func TestAgentSessionMCPOAuthRegistersWithTheConfiguredClientName(t *testing.T) {
	h := setupMCPOAuth(t, "follow", &extension.McpOAuthConfig{ClientName: "Claude Code"})
	h.prompt(t, "/mcp login issues")
	requireEqual(t, "last notification", h.notes.last(), `Signed in to MCP server "issues" (1 tools).`)
	requireEqual(t, "client names", h.server.registrationClientNames(), []any{"Claude Code"})

	fallback := setupMCPOAuth(t, "follow", nil)
	fallback.prompt(t, "/mcp login issues")
	// The fallback is the app name: `pig`, where Pi sends `pi` (docs/design/builtin-mcp.md).
	requireEqual(t, "client names", fallback.server.registrationClientNames(), []any{"pig"})
}

func TestAgentSessionMCPOAuthAddsTheListeningPortToACallbackURLWithoutOne(t *testing.T) {
	h := setupMCPOAuth(t, "follow", &extension.McpOAuthConfig{CallbackURL: "http://127.0.0.1/oauth/done"})
	h.prompt(t, "/mcp login issues")
	requireEqual(t, "last notification", h.notes.last(), `Signed in to MCP server "issues" (1 tools).`)
	if got := h.openedURLs()[0].Query().Get("redirect_uri"); !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/oauth/done$`).MatchString(got) {
		t.Errorf("redirect_uri = %q", got)
	}

	port := freePort(t)
	fixed := setupMCPOAuth(t, "follow", &extension.McpOAuthConfig{CallbackURL: "http://127.0.0.1/oauth/done", CallbackPort: &port})
	fixed.prompt(t, "/mcp login issues")
	requireEqual(t, "redirect_uri", fixed.openedURLs()[0].Query().Get("redirect_uri"), "http://127.0.0.1:"+strconv.Itoa(port)+"/oauth/done")
}

func TestAgentSessionMCPOAuthUsesCredentialsFromPigMcpLoginOnTheNextTurn(t *testing.T) {
	h := setupMCPOAuth(t, "follow", nil)
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(`{"mcpServers":{"issues":{"url":"`+h.server.URL+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// The agent runs `pig mcp login issues` through bash; the user approves in the browser.
	var output []string
	var mu sync.Mutex
	record := func(line string) {
		mu.Lock()
		output = append(output, line)
		mu.Unlock()
	}
	exitCode := mcpext.RunMcpCommand(t.Context(), []string{"login", "issues"}, mcpext.McpCommandOptions{
		Cwd: agentDir, AgentDir: agentDir,
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(h.backend, ""),
		OpenURL: func(target string) {
			go func() {
				request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
				if err != nil {
					return
				}
				if response, err := http.DefaultClient.Do(request); err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
			}()
		},
		Log: record, Error: record,
	})
	if exitCode != 0 {
		t.Fatalf("exit code = %d: %q", exitCode, output)
	}
	requireEqual(t, "last line", output[len(output)-1], `Signed in to MCP server "issues" (1 tools).`)

	// The session still waits for a sign-in, and reconnects when the next turn starts.
	requireEqual(t, "whoami", mcpText(h.callWhoami(t)), "token access-1")
}
