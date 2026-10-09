// Command client is the client under test for the official MCP client conformance suite. The suite starts a scenario
// server and runs this program with the server URL as the last argument, the scenario name in
// MCP_CONFORMANCE_SCENARIO, and scenario data in MCP_CONFORMANCE_CONTEXT.
//
// It drives the code PiG runs for an `mcp.json` HTTP server: mcpext.Connection connects and calls tools, and
// mcpext.SignInMcpServer runs the OAuth sign-in that `/mcp` starts. The browser is simulated: the authorization URL is
// fetched without following its redirect, and the redirect is delivered to PiG's loopback callback server.
// Credentials stay in memory.
//
// Run through ../run, which writes the outcome to PIG_MCP_CONFORMANCE_REPORT.
//
// Ports packages/coding-agent/test/mcp-conformance/client.ts.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// maxSignIns: sign-ins PiG asks the user for are not limited, so a user who keeps approving them would loop forever
// against a server that never accepts the granted scope. This simulated user gives up after three, the limit
// `auth/scope-retry-limit` checks.
const maxSignIns = 3

const requestTimeoutSeconds = 20

type toolCall struct {
	name      string
	arguments map[string]any
}

// toolCalls are the tool calls of a scenario after connecting; ok is false for scenarios this client does not know.
func toolCalls(scenario string, connection *mcpext.Connection) (calls []toolCall, ok bool, err error) {
	if strings.HasPrefix(scenario, "auth/") {
		return []toolCall{{"test-tool", map[string]any{}}}, true, nil
	}
	switch scenario {
	case "initialize":
		return nil, true, nil
	case "tools_call":
		return []toolCall{{"add_numbers", map[string]any{"a": 2, "b": 3}}}, true, nil
	case "sse-retry":
		return []toolCall{{"test_reconnection", map[string]any{}}}, true, nil
	case "elicitation-sep1034-client-defaults":
		return []toolCall{{"test_client_elicitation_defaults", map[string]any{}}}, true, nil
	case "json-schema-2020-12-preservation":
		// The server compares the echoed schema with the one it listed, to detect dropped keywords.
		tools := connection.Tools()
		index := slices.IndexFunc(tools, func(tool mcp.Tool) bool { return tool.Name == "json_schema_2020_12_tool" })
		if index < 0 {
			return nil, true, errors.New("json_schema_2020_12_tool was not listed")
		}
		return []toolCall{{"json_schema_echo", map[string]any{"schema": tools[index].InputSchema}}}, true, nil
	}
	return nil, false, nil
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[pig-conformance] "+format+"\n", args...)
}

func readContext() (map[string]any, error) {
	raw := os.Getenv("MCP_CONFORMANCE_CONTEXT")
	if raw == "" {
		return map[string]any{}, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil || parsed == nil {
		return nil, errors.New("MCP_CONFORMANCE_CONTEXT is not an object")
	}
	return parsed, nil
}

func isLoopback(u *url.URL) bool {
	return u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
}

// visitAuthorizationURL is what a browser does with the authorization URL when the user approves at once.
func visitAuthorizationURL(ctx context.Context, authorizationURL *url.URL) error {
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizationURL.String(), nil)
	if err != nil {
		return err
	}
	response, err := noRedirect.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	location := response.Header.Get("Location")
	if location == "" {
		return fmt.Errorf("Authorization endpoint answered %d without a redirect", response.StatusCode)
	}
	callback, err := authorizationURL.Parse(location)
	if err != nil {
		return err
	}
	if callback.Scheme != "http" || !isLoopback(callback) {
		return fmt.Errorf("Authorization endpoint redirected to %s://%s, not to the loopback callback", callback.Scheme, callback.Host)
	}
	logf("delivering authorization response to %s://%s%s", callback.Scheme, callback.Host, callback.Path)
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, callback.String(), nil)
	if err != nil {
		return err
	}
	delivered, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = delivered.Body.Close() }()
	_, err = io.Copy(io.Discard, delivered.Body)
	return err
}

// simulatedBrowser follows the authorization URL like a user who approves at once.
type simulatedBrowser struct {
	ctx  context.Context
	done chan error
}

func (b *simulatedBrowser) ShowAuthorizationURL(authorizationURL *url.URL) {
	logf("authorization URL: %s://%s%s", authorizationURL.Scheme, authorizationURL.Host, authorizationURL.Path)
	b.done = make(chan error, 1)
	go func() { b.done <- visitAuthorizationURL(b.ctx, authorizationURL) }()
}

func (b *simulatedBrowser) PromptForRedirectURL(ctx context.Context) (string, error) {
	select {
	case err := <-b.done:
		if err != nil {
			// Cancels the sign-in instead of waiting for a callback that never comes.
			logf("browser failed: %s", err)
			return "", nil
		}
	case <-ctx.Done():
		return "", nil
	}
	<-ctx.Done()
	return "", nil
}

func run(ctx context.Context, serverURL, scenario string) (err error) {
	scenarioContext, err := readContext()
	if err != nil {
		return err
	}
	timeout := float64(requestTimeoutSeconds)
	config := extension.McpServerConfig{URL: serverURL, Timeout: &timeout}
	if clientID, ok := scenarioContext["client_id"].(string); ok {
		// Pre-registered clients are configured in `mcp.json`.
		config.OAuth = &extension.McpOAuthConfig{ClientID: clientID}
		if secret, ok := scenarioContext["client_secret"].(string); ok {
			config.OAuth.ClientSecret = secret
		}
	}
	entry := mcpext.McpServerEntry{Name: "conformance", Config: config, Source: "conformance"}
	credentials := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "")
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry:           entry,
		Cwd:             os.TempDir(),
		CreateTransport: mcpext.CreateDefaultTransport,
		Credentials:     credentials,
		OnTools:         func(*mcpext.Connection) {},
	})
	if err != nil {
		return err
	}
	defer connection.Close()

	signIns := 0
	// withSignIn runs operation, signing in like a user answering `/mcp` whenever the server asks for it.
	withSignIn := func(operation func() error) error {
		for {
			err := operation()
			if err == nil {
				return nil
			}
			serverURL := connection.OAuthURL()
			if connection.State() != mcpext.StateNeedsAuth || serverURL == "" {
				return err
			}
			if signIns >= maxSignIns {
				return fmt.Errorf("Still requires sign-in after %d sign-ins", signIns)
			}
			signIns++
			challenge := connection.Challenge()
			if challenge != nil && challenge.Scope != "" {
				logf("sign-in %d (scope: %s)", signIns, challenge.Scope)
			} else {
				logf("sign-in %d", signIns)
			}
			store, err := credentials.ForServer(entry.Name, serverURL)
			if err != nil {
				return err
			}
			settings, err := connection.OAuthSettings()
			if err != nil {
				return err
			}
			if err := mcpext.SignInMcpServer(ctx, mcpext.SignInOptions{
				ServerURL: serverURL, Store: store, Settings: settings, Challenge: challenge, Prompt: &simulatedBrowser{ctx: ctx},
			}); err != nil {
				return err
			}
			connection.ClearChallenge()
		}
	}

	if err := withSignIn(func() error { _, err := connection.GetClient(ctx); return err }); err != nil {
		return err
	}
	names := make([]string, 0)
	for _, tool := range connection.Tools() {
		names = append(names, tool.Name)
	}
	if len(names) == 0 {
		names = append(names, "(none)")
	}
	logf("connected, tools: %s", strings.Join(names, ", "))
	calls, known, err := toolCalls(scenario, connection)
	if err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("Unknown scenario %s", scenario)
	}
	for _, call := range calls {
		var result *mcp.CallToolResult
		if err := withSignIn(func() (callErr error) {
			result, callErr = connection.CallTool(ctx, call.name, call.arguments, mcp.RequestOptions{})
			return callErr
		}); err != nil {
			return err
		}
		content, _ := json.Marshal(result.Content)
		logf("%s: %s", call.name, content)
		if result.IsError != nil && *result.IsError {
			return fmt.Errorf("Tool %s failed: %s", call.name, content)
		}
	}
	return nil
}

type report struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func main() {
	serverURL := ""
	if len(os.Args) > 1 {
		serverURL = os.Args[len(os.Args)-1]
	}
	scenario := os.Getenv("MCP_CONFORMANCE_SCENARIO")
	var result report
	if serverURL == "" || scenario == "" {
		result = report{Error: "Usage: MCP_CONFORMANCE_SCENARIO=<scenario> client <server-url>"}
	} else if err := run(context.Background(), serverURL, scenario); err != nil {
		result = report{Error: err.Error()}
	} else {
		result.Success = true
	}
	if result.Error != "" {
		logf("error: %s", result.Error)
	}
	if path := os.Getenv("PIG_MCP_CONFORMANCE_REPORT"); path != "" {
		data, _ := json.MarshalIndent(result, "", "  ")
		_ = os.WriteFile(path, append(data, '\n'), 0o600)
	}
	// Exit explicitly: a failed scenario can leave sockets of the server under test open.
	if !result.Success {
		os.Exit(1)
	}
	os.Exit(0)
}
