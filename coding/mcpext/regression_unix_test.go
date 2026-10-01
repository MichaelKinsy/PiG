//go:build unix

package mcpext_test

import (
	"os"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Not an upstream test: a regression guard for the Go realization.

// A stdio server that died is never reused: the connection marks it dead on the
// first unexpected close and the next call starts a new process.
func TestConnectionNeverReusesAStdioServerThatDied(t *testing.T) {
	var transports []*mcp.StdioTransport
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: mcpext.McpServerEntry{Name: "fixture", Source: "test", Config: extension.McpServerConfig{
			Command: os.Args[0], Args: []string{"-test.run=^$"}, Env: extension.NewOrderedStrings(fixtureEnv, "cwd-server", "GORACE", "atexit_sleep_ms=0"),
		}},
		Cwd: t.TempDir(),
		CreateTransport: func(entry mcpext.McpServerEntry, cwd string, auth mcp.AuthProvider) (mcp.Transport, error) {
			transport, err := mcpext.CreateDefaultTransport(entry, cwd, auth)
			if stdio, ok := transport.(*mcp.StdioTransport); ok {
				transports = append(transports, stdio)
			}
			return transport, err
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.CallTool(t.Context(), "cwd", map[string]any{}, mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	first := transports[0].PID()
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the connection to notice the death", func() bool { return connection.State() == mcpext.StateDisconnected })
	if _, err := connection.CallTool(t.Context(), "cwd", map[string]any{}, mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(transports) != 2 || transports[1].PID() == first {
		t.Fatalf("transports = %d, second pid = %d, first pid = %d", len(transports), transports[1].PID(), first)
	}
	if syscall.Kill(first, 0) == nil {
		// A killed child may linger as a zombie until reaped; the transport reaps it.
		waitFor(t, "the dead server to be reaped", func() bool { return syscall.Kill(first, 0) != nil })
	}
}
