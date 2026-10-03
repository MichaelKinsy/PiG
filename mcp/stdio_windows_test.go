//go:build windows

package mcp_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// writeCmdShim writes an npm-style .cmd shim, in a directory whose name holds
// a space, that runs the stdio-server fixture with the shim's arguments.
func writeCmdShim(t *testing.T) (dir, shim string, env map[string]string) {
	t.Helper()
	command, _, env := fixtureStdioOptions("stdio-server")
	dir = filepath.Join(t.TempDir(), "mcp server dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shim = filepath.Join(dir, "srv.cmd")
	body := "@ECHO off\r\n\"" + command + "\" -test.run=NONE %*\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, shim, env
}

// Pi's StdioTransport starts the server with cross-spawn, which runs a .cmd
// shim through `cmd.exe /d /s /c "..."` (packages/mcp/src/transports/stdio.ts).
// Oracle, pinned pi-mcp 0.99.1 on Node 24.19: an absolute shim path and a bare
// name found through PATH and PATHEXT both connect, list ["echo"] and answer
// tools/call in about 80 ms.
func TestStdioTransportRunsACmdShimServerThroughCmdExe(t *testing.T) {
	dir, shim, env := writeCmdShim(t)
	pathEnv := map[string]string{"PATH": dir + ";" + os.Getenv("PATH")}
	for name, options := range map[string]mcp.StdioTransportOptions{
		"absolute path": {Command: shim, Args: []string{"plain", "two words"}, Env: env},
		"bare name":     {Command: "srv", Env: pathEnv},
	} {
		t.Run(name, func(t *testing.T) {
			for key, value := range env {
				if _, ok := options.Env[key]; !ok {
					options.Env[key] = value
				}
			}
			transport := mcp.NewStdioTransport(options)
			client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "stdio-test", Version: "1.0.0"}, RequestTimeoutMs: 10_000})
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.Connect(t.Context(), transport); err != nil {
				t.Fatalf("connect: %v (stderr %q)", err, transport.Stderr())
			}
			tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
			if err != nil {
				t.Fatal(err)
			}
			jsonEqual(t, tools, `[{"name":"echo","inputSchema":{"type":"object"}}]`)
			result, err := client.CallTool(t.Context(), "echo", map[string]any{"text": "hello"}, mcp.RequestOptions{})
			if err != nil {
				t.Fatal(err)
			}
			jsonEqual(t, result, `{"content":[{"type":"text","text":"hello"}]}`)
		})
	}
}

// cross-spawn runs a command it cannot find through cmd.exe too; cmd.exe
// reports it on stderr and exits, and the transport closes. Oracle, pinned
// pi-mcp 0.99.1: connect rejects with McpConnectionClosedError ("MCP
// connection closed") after 45 ms, with cmd.exe's own stderr ("'pig-no-such-mcp-server'
// is not recognized as an internal or external command, ..." in English).
func TestStdioTransportReportsAMissingCommandAsAClosedConnection(t *testing.T) {
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: "pig-no-such-mcp-server"})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "stdio-test", Version: "1.0.0"}, RequestTimeoutMs: 20_000})
	t.Cleanup(func() { _ = client.Close() })
	start := time.Now()
	_, err := client.Connect(t.Context(), transport)
	if _, ok := errors.AsType[*mcp.McpConnectionClosedError](err); !ok {
		t.Fatalf("connect error = %T %v, want McpConnectionClosedError (stderr %q)", err, err, transport.Stderr())
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("connect took %v; the transport should close when cmd.exe exits", elapsed)
	}
	if want := cmdExeStderr(t, "pig-no-such-mcp-server"); transport.Stderr() != want {
		t.Fatalf("stderr = %q, want %q", transport.Stderr(), want)
	}
}

// cmdExeStderr is what cmd.exe writes to stderr, in the system's language, for
// cross-spawn's command line of an unresolved command.
func cmdExeStderr(t *testing.T, command string) string {
	t.Helper()
	cmd := exec.Command(os.Getenv("ComSpec"))
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + command + `"`}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("cmd.exe found %s", command)
	}
	if stderr.Len() == 0 {
		t.Fatal("cmd.exe wrote nothing to stderr")
	}
	return stderr.String()
}
