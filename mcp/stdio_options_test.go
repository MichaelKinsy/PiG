package mcp_test

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// StdioTransportOptions.cwd, maxStderrBytes and maxMessageBytes
// (packages/mcp/src/transports/stdio.ts). Pi's stdio test does not exercise them,
// so these tests are PiG's own: each starts the transport with one option set and
// observes the effect the option documents.

func startAndWaitForExit(t *testing.T, transport *mcp.StdioTransport) {
	t.Helper()
	closed := make(chan struct{})
	transport.OnClose(func() { close(closed) })
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("the fixture did not exit")
	}
}

func TestStdioTransportRunsTheServerInTheConfiguredCwd(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command, args, env := fixtureStdioOptions("cwd")
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env, Cwd: dir})
	startAndWaitForExit(t, transport)
	got, err := filepath.EvalSymlinks(strings.TrimSpace(transport.Stderr()))
	if err != nil || got != dir {
		t.Fatalf("server cwd = %q (%v), want %q", transport.Stderr(), err, dir)
	}
}

func TestStdioTransportKeepsOnlyTheLastMaxStderrBytesOfStderr(t *testing.T) {
	command, args, env := fixtureStdioOptions("stderr-flood")
	var mu sync.Mutex
	var seen int
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{
		Command: command, Args: args, Env: env, MaxStderrBytes: 100,
		OnStderr: func(chunk string) {
			mu.Lock()
			seen += len(chunk)
			mu.Unlock()
		},
	})
	startAndWaitForExit(t, transport)
	got := transport.Stderr()
	if len(got) != 100 || !strings.HasSuffix(got, "aaaTAIL") {
		t.Fatalf("stderr = %d bytes %q, want the last 100 bytes ending in TAIL", len(got), got)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen != 4004 {
		t.Fatalf("OnStderr saw %d bytes, want the whole 4004: the cap bounds the retained text, not the callback", seen)
	}
}

func TestStdioTransportRejectsAStdoutLineLongerThanMaxMessageBytes(t *testing.T) {
	command, args, env := fixtureStdioOptions("stdout-flood")
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env, MaxMessageBytes: 1024})
	errs := make(chan error, 4)
	transport.OnError(func(err error) { errs <- err })
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	select {
	case err := <-errs:
		if err.Error() != "MCP stdio message exceeds 1024 bytes" {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no error for an oversized message")
	}
}

func TestStdioTransportInheritsStderrWithoutCapturingIt(t *testing.T) {
	command, args, env := fixtureStdioOptions("cwd")
	calls := 0
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{
		Command: command, Args: args, Env: env, Stderr: "inherit",
		OnStderr: func(string) { calls++ },
	})
	startAndWaitForExit(t, transport)
	if got := transport.Stderr(); got != "" || calls != 0 {
		t.Fatalf("with stderr \"inherit\" the transport captured %q and called OnStderr %d times", got, calls)
	}
}

// stdio.ts:200 skips a stdout line whose text.trim() is empty and reports any other line that does not parse. trim removes U+FEFF and Unicode
// space separators but not U+0085, so a BOM or no-break-space line is silent and a U+0085 line is a parse error.
func TestStdioTransportSkipsLinesOfJavaScriptWhitespaceOnly(t *testing.T) {
	command, args, env := fixtureStdioOptions("blank-lines")
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env})
	errs := make(chan error, 8)
	messages := make(chan mcp.JSONRPCMessage, 8)
	transport.OnError(func(err error) { errs <- err })
	transport.OnMessage(func(message mcp.JSONRPCMessage) { messages <- message })
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	for _, want := range []string{"message", "error"} {
		select {
		case message := <-messages:
			if want != "message" || message.Method != "ping" {
				t.Fatalf("got message %+v, want the %s", message, want)
			}
		case err := <-errs:
			if want != "error" {
				t.Fatalf("a blank line was reported: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("no %s", want)
		}
	}
	select {
	case err := <-errs:
		t.Fatalf("an extra error: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}
