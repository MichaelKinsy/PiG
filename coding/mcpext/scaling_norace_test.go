//go:build !race

package mcpext_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Scaling gates for the MCP extension over N, 10N and 100N configured servers of three tools each (agent-session-mcp.test.ts's server, one `docs` per name).

// scalingMcpSession is a started MCP extension with count in-memory servers, every one connected and its tools registered.
type scalingMcpSession struct {
	h *sessionHarness
}

func newScalingMcpSession(t testing.TB, count int) *scalingMcpSession {
	h := &sessionHarness{host: newFakeHost(), calls: &callLog{}, servers: &serverSet{}, notes: &notifications{}}
	h.host.registerBuiltin(mcpext.CodemodeToolName, "builtin:codemode")
	h.host.registerBuiltin(mcpext.ToolSearchToolName, "builtin:tool-search")
	entries := make([]mcpext.McpServerEntry, count)
	for i := range entries {
		entries[i] = mcpext.McpServerEntry{Name: fmt.Sprintf("docs%d", i), Config: extension.McpServerConfig{URL: "http://unused.invalid", Exposure: extension.McpExposureDirect}, Source: "test"}
	}
	h.ext = mcpext.New(h.host, mcpext.Options{
		LoadConfig: func(context.Context) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: entries}
		},
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			client, server := createFakeServer(h.calls, func() []map[string]any { return serverTools }, false)
			h.servers.add(server)
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	})
	h.ctx = eventContext(t.TempDir(), h.notes)
	t.Cleanup(func() {
		h.ext.SessionShutdown()
		h.servers.closeAll()
	})
	return &scalingMcpSession{h: h}
}

func TestScalingMcpServers(t *testing.T) {
	// Connecting every server and registering its tools is linear in the servers: one connection and three tool definitions each.
	testscaling.Check(t, testscaling.Case{Name: "mcp session start over n servers", Size: 3, Bound: testscaling.Linear, Prepare: func(n int) func() {
		s := newScalingMcpSession(t, n)
		return func() {
			s.h.ext.SessionStart(s.h.ctx)
			s.h.ext.Pending()
		}
	}})
}

func TestScalingMcpServersAfterStart(t *testing.T) {
	started := func(n int) *scalingMcpSession {
		s := newScalingMcpSession(t, n)
		s.h.start()
		return s
	}
	sizes := [3]int{3, 30, 300}
	// Every prompt lists the servers in the system prompt, so it is linear in them.
	perfgate.CheckScaling(t, "mcp.prompt_over_servers", sizes, func(n int) func() {
		s := started(n)
		return s.h.prompt
	})
	// A tool call finds its server by name: its cost does not depend on how many other servers are configured.
	perfgate.CheckScaling(t, "mcp.tool_call_over_servers", sizes, func(n int) func() {
		s := started(n)
		definition, ok := s.h.host.tool("mcp__docs0__search")
		if !ok {
			t.Fatal("mcp__docs0__search is not registered")
		}
		params := json.RawMessage(`{"query":"scaling"}`)
		return func() {
			if _, err := definition.Execute(context.Background(), "call-1", params, nil); err != nil {
				panic(err)
			}
		}
	})
}
