//go:build !windows

package env

// The production McpPipe is RemoteProcess (packages/mcp README: `openProcess()` of pi-env returns an McpPipe without an adapter). Pi's
// packages/mcp/test/pipe.test.ts:251 ("runs initialize, tools/list and tools/call") drives PipeTransport over a spawned program; these tests drive
// it over a program a pi-env daemon runs, which is what Write, CloseStdin, Close and Exited of McpPipe are for. Not upstream tests: Pi's pipe suite
// runs its transport over a Node child process only.

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// mcpServerProgram answers initialize and tools/list, and ends when its stdin ends (keepAlive false) or stays running after it (keepAlive true).
func mcpServerProgram(keepAlive bool) string {
	script := `
const rl = require("readline").createInterface({ input: process.stdin });
rl.on("line", (line) => {
	const message = JSON.parse(line);
	if (message.id === undefined) return;
	let result = {};
	if (message.method === "initialize") result = { protocolVersion: "2025-06-18", capabilities: { tools: {} }, serverInfo: { name: "env-fixture", version: "1.0.0" } };
	if (message.method === "tools/list") result = { tools: [{ name: "echo", inputSchema: { type: "object" } }] };
	process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: message.id, result }) + "\n");
});
`
	if keepAlive {
		return script + "setInterval(() => {}, 1000);\n"
	}
	return script + "rl.on(\"close\", () => process.exit(0));\n"
}

// openMcpPipeTransport is a PipeTransport over a RemoteProcess; pid is set once the program runs, and closed is closed when the transport closes.
func openMcpPipeTransport(t *testing.T, connection *Connection, cwd string, program string, options mcp.PipeTransportOptions) (transport *mcp.PipeTransport, pid *int, closed chan struct{}) {
	t.Helper()
	pid = new(int)
	options.Open = func(handlers mcp.McpPipeHandlers) (mcp.McpPipe, error) {
		process, err := OpenProcess(context.Background(), connection, ProcessOptions{Cwd: cwd, Argv: nodeProgram(t, program), OnStdout: handlers.OnStdout, OnStderr: handlers.OnStderr})
		if err != nil {
			return nil, err
		}
		*pid = process.Pid
		return process, nil
	}
	transport = mcp.NewPipeTransport(options)
	closed = make(chan struct{})
	transport.OnClose(func() { close(closed) })
	return transport, pid, closed
}

func connectOverPipe(t *testing.T, transport *mcp.PipeTransport) *mcp.Client {
	t.Helper()
	client := mcp.NewClient(mcp.ClientOptions{Name: "env-pipe-test", Version: "1.0.0"})
	result, err := client.Connect(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerInfo.Name != "env-fixture" {
		t.Fatalf("server = %+v", result.ServerInfo)
	}
	tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	return client
}

func waitClosed(t *testing.T, closed chan struct{}) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(processTestTimeout):
		t.Fatal("the transport did not close")
	}
}

// Write carries each request line to the program, CloseStdin ends it and Exited reports it, so Close leaves a program that exits in time alone.
func TestPipeTransportOverARemoteProcessRunsInitializeAndToolsListThenClosesStdinAndTheProgramExits(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		const closeTimeoutMs = 10_000
		transport, pid, closed := openMcpPipeTransport(t, connection, cwd, mcpServerProgram(false), mcp.PipeTransportOptions{CloseTimeoutMs: closeTimeoutMs})
		client := connectOverPipe(t, transport)
		started := time.Now()
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		waitClosed(t, closed)
		// The program ends because its stdin ended, not because Close killed it after CloseTimeoutMs.
		if elapsed := time.Since(started); elapsed >= closeTimeoutMs*time.Millisecond/2 {
			t.Fatalf("the program took %v to end after its stdin was closed; CloseStdin did not end it", elapsed)
		}
		if !waitUntilGone(*pid) {
			t.Fatalf("program %d still runs after its stdin ended", *pid)
		}
	})
}

// A program that does not end with its stdin is killed by Close after CloseTimeoutMs: McpPipe.Close kills the program through the daemon.
func TestPipeTransportOverARemoteProcessKillsAProgramThatDoesNotEndAfterItsStdinEnds(t *testing.T) {
	eachProcessDaemon(t, func(t *testing.T) {
		connection, cwd := processConnection(t)
		transport, pid, closed := openMcpPipeTransport(t, connection, cwd, mcpServerProgram(true), mcp.PipeTransportOptions{CloseTimeoutMs: 300})
		client := connectOverPipe(t, transport)
		started := time.Now()
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		waitClosed(t, closed)
		if elapsed := time.Since(started); elapsed < 250*time.Millisecond {
			t.Fatalf("the program was killed after %v, before CloseTimeoutMs", elapsed)
		}
		if !waitUntilGone(*pid) {
			t.Fatalf("program %d still runs after Close killed it", *pid)
		}
	})
}
