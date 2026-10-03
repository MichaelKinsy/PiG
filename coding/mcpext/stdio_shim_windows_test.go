//go:build windows

package mcpext_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// A configured stdio server whose command is an npm-style .cmd shim (npx and
// every npm bin on Windows) starts through cmd.exe as Pi's cross-spawn starts
// it, with its arguments and cwd. Oracle, pinned pi-mcp 0.99.1 StdioTransport
// on Node 24.19: the shim in a directory whose name holds a space connects and
// the server receives ["plain", "two words"] in about 80 ms.
func TestConnectionStartsAConfiguredCmdShimServer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mcp server dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "srv.cmd")
	if err := os.WriteFile(shim, []byte("@ECHO off\r\n\""+os.Args[0]+"\" -test.run=NONE %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: mcpext.McpServerEntry{Name: "shim", Source: "test", Config: extension.McpServerConfig{
			Command: shim, Args: []string{"plain", "two words"}, Cwd: dir,
			Env: extension.NewOrderedStrings(fixtureEnv, "cwd-server", "GORACE", "atexit_sleep_ms=0"),
		}},
		Cwd:             t.TempDir(),
		CreateTransport: mcpext.CreateDefaultTransport,
		Credentials:     mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	start := time.Now()
	result, err := connection.CallTool(t.Context(), "cwd", map[string]any{}, mcp.RequestOptions{TimeoutMs: 20_000})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the shim server answered after %v", elapsed)
	}
	if got, want := result.Content[0].Text, dir+"\n"+"two words"; got != want {
		t.Fatalf("server cwd and last argument = %q, want %q", got, want)
	}
}
