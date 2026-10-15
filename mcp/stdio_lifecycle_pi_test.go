package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// runEnvironFixture starts the environ fixture with the given inheritEnv option and returns the environment the server reported on stderr.
func runEnvironFixture(t *testing.T, inherit *bool) []string {
	t.Helper()
	command, args, env := fixtureStdioOptions("environ")
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env, InheritEnv: inherit})
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
	var got []string
	if err := json.Unmarshal([]byte(transport.Stderr()), &got); err != nil {
		t.Fatalf("decode %q: %v", transport.Stderr(), err)
	}
	return got
}

// TestStdioTransportInheritsTheParentEnvironmentUnlessInheritEnvIsFalse ports stdio.ts:94: the server gets {...process.env, ...options.env} unless
// `inheritEnv === false`, so an unset option (like true) passes the parent's variables and only an explicit false hides them.
func TestStdioTransportInheritsTheParentEnvironmentUnlessInheritEnvIsFalse(t *testing.T) {
	t.Setenv("PIG_PARENT_MARK", "from-parent")
	on, off := true, false
	for name, tc := range map[string]struct {
		inherit *bool
		want    bool
	}{"unset": {nil, true}, "true": {&on, true}, "false": {&off, false}} {
		got := runEnvironFixture(t, tc.inherit)
		if has := slices.Contains(got, "PIG_PARENT_MARK=from-parent"); has != tc.want {
			t.Errorf("inheritEnv %s: parent variable visible = %v, want %v (env %q)", name, has, tc.want, got)
		}
		if !slices.Contains(got, fixtureEnv+"=environ") {
			t.Errorf("inheritEnv %s: options.env variable missing from %q", name, got)
		}
	}
}

// TestStdioTransportLifecycleErrorsMatchPi ports stdio.ts:90-92 and :143-146: a second start fails with "MCP stdio transport already started", a start
// after close and a send before start, after close or after the server exited fail with McpConnectionClosedError, and close is idempotent.
func TestStdioTransportLifecycleErrorsMatchPi(t *testing.T) {
	isClosed := func(err error) bool {
		var closed *mcp.McpConnectionClosedError
		return errors.As(err, &closed)
	}
	command, args, env := fixtureStdioOptions("stdio-server")
	options := mcp.StdioTransportOptions{Command: command, Args: args, Env: env}

	transport := mcp.NewStdioTransport(options)
	ping := mcp.JSONRPCMessage{}
	if err := transport.Send(context.Background(), ping); !isClosed(err) {
		t.Errorf("send before start = %v, want McpConnectionClosedError", err)
	}
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	if err := transport.Start(); err == nil || err.Error() != "MCP stdio transport already started" {
		t.Errorf("second start = %v, want the already-started error", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err := transport.Close(); err != nil {
		t.Errorf("second close = %v, want nil", err)
	}
	if err := transport.Send(context.Background(), ping); !isClosed(err) {
		t.Errorf("send after close = %v, want McpConnectionClosedError", err)
	}

	closedFirst := mcp.NewStdioTransport(options)
	if err := closedFirst.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closedFirst.Start(); !isClosed(err) {
		t.Errorf("start after close = %v, want McpConnectionClosedError", err)
	}
}
