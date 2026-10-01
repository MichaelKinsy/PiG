package mcpext_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/coding-agent/test/mcp-extension.test.ts ("MCP connections").

type transportSource func() mcp.Transport

// connect is connect() of mcp-extension.test.ts: a connection whose
// transports come, in order, from the given factories.
func connect(t *testing.T, entry mcpext.McpServerEntry, transports []transportSource, log *mcpext.McpServerLog) (*mcpext.Connection, *atomic.Int32) {
	t.Helper()
	var opened atomic.Int32
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: entry,
		Cwd:   ".",
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			return transports[opened.Add(1)-1](), nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		Log:         log,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	return connection, &opened
}

func stdioEntry() mcpext.McpServerEntry {
	return mcpext.McpServerEntry{Name: "fake", Config: extension.McpServerConfig{Command: "unused"}, Source: "test"}
}

func httpEntry(headers ...string) mcpext.McpServerEntry {
	config := extension.McpServerConfig{URL: "http://unused.invalid"}
	if len(headers) > 0 {
		config.Headers = extension.NewOrderedStrings(headers...)
	}
	return mcpext.McpServerEntry{Name: "fake", Config: config, Source: "test"}
}

func echoResult() string { return `{"content":[{"type":"text","text":"ok"}]}` }

func TestMCPConnectionsStartsANewSessionAndRetriesOnceWhenTheSessionExpired(t *testing.T) {
	servers := &serverSet{}
	connection, opened := connect(t, stdioEntry(), []transportSource{
		func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{expireFirstCall: true}) },
		func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) },
	}, nil)
	var wg sync.WaitGroup
	results := make([]*mcp.CallToolResult, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Go(func() {
			results[i], errs[i] = connection.CallTool(t.Context(), "echo", map[string]any{}, mcp.RequestOptions{})
		})
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		jsonEqual(t, results[i], echoResult())
	}
	if opened.Load() != 2 {
		t.Fatalf("opened = %d", opened.Load())
	}
}

// The command, arguments, and cwd of stdio servers expand `~`. The server is
// the test binary (stdio.mjs upstream), started through a link in the fake
// home so the command itself needs the expansion.
func TestMCPConnectionsExpandsTildeInTheCommandArgumentsAndCwdOfStdioServers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("upstream skips this case on win32")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, os.Args[0], filepath.Join(home, "server"))
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: mcpext.McpServerEntry{Name: "home", Source: "test", Config: extension.McpServerConfig{
			Command: "~/server", Args: []string{"-test.run=^$", "~/server.mjs"}, Cwd: "~/work",
			Env: extension.NewOrderedStrings(fixtureEnv, "cwd-server"),
		}},
		Cwd:             os.TempDir(),
		CreateTransport: mcpext.CreateDefaultTransport,
		Credentials:     mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	result, err := connection.CallTool(t.Context(), "cwd", map[string]any{}, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cwd, arg, _ := strings.Cut(result.Content[0].Text, "\n")
	realCwd, _ := filepath.EvalSymlinks(cwd)
	realWork, _ := filepath.EvalSymlinks(filepath.Join(home, "work"))
	if realCwd != realWork {
		t.Fatalf("cwd = %s, want %s", realCwd, realWork)
	}
	if arg != filepath.Join(home, "server.mjs") {
		t.Fatalf("argument = %s", arg)
	}
}

func TestMCPConnectionsConnectsToServersWithoutTheToolsCapabilityWithoutListingTools(t *testing.T) {
	servers := &serverSet{}
	methods := &methodLog{}
	connection, _ := connect(t, stdioEntry(), []transportSource{
		func() mcp.Transport {
			return createFakeTransport(servers, fakeTransportOptions{noTools: true, methods: methods})
		},
	}, nil)
	if _, err := connection.GetClient(t.Context()); err != nil {
		t.Fatal(err)
	}
	if connection.State() != mcpext.StateConnected {
		t.Fatalf("state = %s", connection.State())
	}
	if len(connection.Tools()) != 0 {
		t.Fatalf("tools = %v", connection.Tools())
	}
	jsonEqual(t, methods.all(), `["initialize"]`)
}

func TestMCPConnectionsMarksADroppedConnectionAndReconnectsOnTheNextCall(t *testing.T) {
	servers := &serverSet{}
	connection, opened := connect(t, stdioEntry(), []transportSource{
		func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) },
		func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) },
	}, nil)
	if _, err := connection.GetClient(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = servers.last().Close()
	waitFor(t, "the connection to notice the drop", func() bool { return connection.State() == mcpext.StateDisconnected })
	if connection.Error() != "Connection closed" {
		t.Fatalf("error = %q", connection.Error())
	}
	result, err := connection.CallTool(t.Context(), "echo", map[string]any{}, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, echoResult())
	if connection.State() != mcpext.StateConnected {
		t.Fatalf("state = %s", connection.State())
	}
	if opened.Load() != 2 {
		t.Fatalf("opened = %d", opened.Load())
	}
}

// failingSend replaces the client end's Send with one that fails with err.
func failingSend(servers *serverSet, err error) mcp.Transport {
	inner := createFakeTransport(servers, fakeTransportOptions{}).(interface {
		mcp.Transport
	})
	return &sendOverride{Transport: inner, send: func(mcp.JSONRPCMessage) error { return err }}
}

type sendOverride struct {
	mcp.Transport
	send func(mcp.JSONRPCMessage) error
}

func (s *sendOverride) Send(m mcp.JSONRPCMessage) error { return s.send(m) }

func TestMCPConnectionsRetriesHTTPConnectionsThatFailWithATransientError(t *testing.T) {
	servers := &serverSet{}
	connection, opened := connect(t, httpEntry("Authorization", "x"), []transportSource{
		func() mcp.Transport {
			return failingSend(servers, &mcp.McpHttpError{Status: 503, Message: "MCP HTTP request failed with status 503"})
		},
		func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) },
	}, nil)
	if _, err := connection.GetClient(t.Context()); err != nil {
		t.Fatal(err)
	}
	if connection.State() != mcpext.StateConnected || opened.Load() != 2 {
		t.Fatalf("state = %s, opened = %d", connection.State(), opened.Load())
	}
	connection.Close()

	failing, failingOpened := connect(t, httpEntry("Authorization", "x"), []transportSource{
		func() mcp.Transport {
			return failingSend(servers, &mcp.McpHttpError{Status: 400, Message: "MCP HTTP request failed with status 400: bad"})
		},
	}, nil)
	if _, err := failing.GetClient(t.Context()); err == nil || !strings.Contains(err.Error(), "status 400: bad") {
		t.Fatalf("err = %v", err)
	}
	if failing.State() != mcpext.StateFailed || failingOpened.Load() != 1 {
		t.Fatalf("state = %s, opened = %d", failing.State(), failingOpened.Load())
	}
}

func TestMCPConnectionsRetriesResourceReadsButNotToolCallsAfterATransientHTTPError(t *testing.T) {
	servers := &serverSet{}
	inner := createFakeTransport(servers, fakeTransportOptions{})
	var mu sync.Mutex
	failed := map[string]bool{}
	// The first read and the first call fail with 502.
	transport := &sendOverride{Transport: inner, send: func(message mcp.JSONRPCMessage) error {
		if message.Method == "resources/read" || message.Method == "tools/call" {
			mu.Lock()
			first := !failed[message.Method]
			failed[message.Method] = true
			mu.Unlock()
			if first {
				return &mcp.McpHttpError{Status: 502, Message: "MCP HTTP request failed with status 502"}
			}
		}
		return inner.Send(message)
	}}
	connection, _ := connect(t, stdioEntry(), []transportSource{func() mcp.Transport { return transport }}, nil)
	read, err := connection.ReadResource(t.Context(), "docs://a", mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, read, `{"contents":[{"uri":"docs://a","text":"ok"}]}`)
	if _, err := connection.CallTool(t.Context(), "echo", map[string]any{}, mcp.RequestOptions{}); err == nil || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("err = %v", err)
	}
}

func TestMCPConnectionsAsksOAuthServersThatKeepRejectingRequestsForANewSignIn(t *testing.T) {
	servers := &serverSet{}
	connection, _ := connect(t, mcpext.McpServerEntry{Name: "fake", Config: extension.McpServerConfig{URL: "http://unused.invalid"}, Source: "test"}, []transportSource{
		func() mcp.Transport {
			//nolint:bodyclose // the response has no body
			return failingSend(servers, mcp.NewAuthRequiredError(unauthorizedResponse(), ""))
		},
	}, nil)
	_, err := connection.GetClient(t.Context())
	if err == nil || err.Error() != `MCP server "fake" requires sign-in. Run /mcp to sign in.` {
		t.Fatalf("err = %v", err)
	}
	if connection.State() != mcpext.StateNeedsAuth {
		t.Fatalf("state = %s", connection.State())
	}
}

// mcp-extension.test.ts "sends the provider token and asks for the provider login when the server rejects it".
func TestMCPConnectionsSendsTheProviderTokenAndAsksForTheProviderLoginWhenTheServerRejectsIt(t *testing.T) {
	var mu sync.Mutex
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: mcpext.McpServerEntry{
			Name: "radius", Source: "test",
			Config: extension.McpServerConfig{URL: server.URL + "/mcp", Auth: &extension.McpAuthConfig{Provider: "radius"}},
		},
		Cwd:             ".",
		CreateTransport: mcpext.CreateDefaultTransport,
		Credentials:     mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		ProviderToken: func(_ context.Context, provider string) string {
			if provider == "radius" {
				return "tok"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if connection.OAuthURL() != "" {
		t.Fatalf("oauthUrl = %q, want none for a server with auth.provider", connection.OAuthURL())
	}
	_, err = connection.GetClient(t.Context())
	if want := `MCP server "radius" requires sign-in. Run /login radius to sign in.`; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if connection.State() != mcpext.StateNeedsAuth {
		t.Fatalf("state = %s", connection.State())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(authorizations) != 1 || authorizations[0] != "Bearer tok" {
		t.Fatalf("authorizations = %q, want one \"Bearer tok\"", authorizations)
	}
}

func TestMCPConnectionsAppendsServerLogMessagesToTheLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.log")
	servers := &serverSet{}
	connection, _ := connect(t, stdioEntry(), []transportSource{
		func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) },
	}, mcpext.NewMcpServerLog(path))
	if _, err := connection.GetClient(t.Context()); err != nil {
		t.Fatal(err)
	}
	server := servers.last()
	if err := server.Send(mcp.NewNotification("notifications/message", json.RawMessage(`{"level":"warning","logger":"db","data":"slow\nquery"}`))); err != nil {
		t.Fatal(err)
	}
	if err := server.Send(mcp.NewNotification("notifications/message", json.RawMessage(`{"level":"error","data":{"code":7}}`))); err != nil {
		t.Fatal(err)
	}
	var lines string
	waitFor(t, "the log lines", func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		lines = regexp.MustCompile(`(?m)^\S+ `).ReplaceAllString(string(data), "")
		return strings.Count(lines, "\n") >= 3
	})
	if lines != "[fake] warning db: slow\n    query\n[fake] error {\"code\":7}\n" {
		t.Fatalf("log = %q", lines)
	}
}

func TestMCPConnectionsResolvesTheOAuthClientSecretLazily(t *testing.T) {
	servers := &serverSet{}
	connection, _ := connect(t, mcpext.McpServerEntry{
		Name: "fake", Source: "test",
		Config: extension.McpServerConfig{URL: "http://unused.invalid", OAuth: &extension.McpOAuthConfig{ClientSecret: "!exit 1"}},
	}, []transportSource{func() mcp.Transport { return createFakeTransport(servers, fakeTransportOptions{}) }}, nil)
	result, err := connection.CallTool(t.Context(), "echo", map[string]any{}, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, echoResult())
	if _, err := connection.OAuthSettings(); err == nil || !strings.Contains(err.Error(), "oauth.clientSecret") {
		t.Fatalf("oauthSettings err = %v", err)
	}
}
