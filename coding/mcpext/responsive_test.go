package mcpext_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// The `/mcp` manager stays usable while servers connect (1.1.0, #10562; packages/coding-agent/src/extensions/mcp/
// index.ts runInBackground, startConnection, waitForServers). Upstream added no test for it, so the expectations are
// its changelog entry and the commit message: the manager opens before the startup connections finish, connection
// actions run in the background, tool calls wait for their own server, and a prepared call resolves the active
// connection when it executes.

func tuiCommandContext(h *sessionHarness, ui mcpext.McpUi, shown *int) mcpext.CommandContext {
	return mcpext.CommandContext{
		EventContext: h.ctx, Mode: extension.ModeTUI, HasUI: true,
		ShowManager: func(_ context.Context, manage func(mcpext.McpUi) error) error {
			*shown++
			return manage(ui)
		},
	}
}

func TestMcpCommandOpensTheManagerBeforeTheStartupConnectionsFinish(t *testing.T) {
	// The server never answers `initialize`.
	h := setupSlow(t, slowConfig{}, never)
	shown := 0
	ui := &scriptedUI{}

	within(t, "/mcp", 2*time.Second, func() {
		if err := h.ext.RunCommand(t.Context(), "", tuiCommandContext(h.sessionHarness, ui, &shown)); err != nil {
			t.Error(err)
		}
	})

	if shown != 1 || len(ui.menus) == 0 {
		t.Fatalf("manager shown %d times with %d menus", shown, len(ui.menus))
	}
	if got := itemsOf(ui.menus[0]); len(got) != 1 || !strings.HasPrefix(got[0], "slow|slow|") {
		t.Fatalf("servers menu = %q", got)
	}
}

func TestMcpCommandWithoutTheManagerStillWaitsForTheStartupConnections(t *testing.T) {
	h := setupSlow(t, slowConfig{}, 30*time.Millisecond)
	notes := &leveledNotes{}
	c := mcpext.CommandContext{EventContext: h.ctx, Mode: extension.ModePrint}
	c.Notify = notes.notify

	if err := h.ext.RunCommand(t.Context(), "", c); err != nil {
		t.Fatal(err)
	}

	got := notes.all()
	if len(got) != 1 || !strings.Contains(got[0].message, "slow: connected, 3 tools") {
		t.Fatalf("notifications = %q, want the status after the server connected", got)
	}
}

func writeDocsConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"docs":{"url":"http://unused.invalid","exposure":"direct"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMcpManagerEnablesAServerInTheBackground(t *testing.T) {
	// The first connection answers at once; the connection after the re-enable never answers `initialize`.
	h := setupSession(t, extension.McpExposureDirect, nil, setupOptions{configPath: writeDocsConfig(t), connectDelay: func(n int) time.Duration {
		if n == 0 {
			return 0
		}
		return never
	}})
	h.start()
	shown := 0
	ui := &scriptedUI{answers: []string{"docs", "disable", "enable", "", ""}}

	within(t, "manager", 5*time.Second, func() {
		if err := h.ext.RunCommand(t.Context(), "", tuiCommandContext(h, ui, &shown)); err != nil {
			t.Error(err)
		}
	})

	servers := h.ext.Servers()
	if len(servers) != 1 || !servers[0].Enabled {
		t.Fatalf("servers = %+v, want docs enabled", servers)
	}
	if state := h.ext.DescribeState("docs", false); state != "connecting…" && state != "starting" {
		t.Fatalf("state = %q, want the connection still opening", state)
	}
}

func TestMcpDirectToolCallWaitsForItsServersReconnect(t *testing.T) {
	const reconnectDelay = 200 * time.Millisecond
	h := setupSession(t, extension.McpExposureDirect, nil, setupOptions{configPath: writeDocsConfig(t), connectDelay: func(n int) time.Duration {
		if n == 0 {
			return 0
		}
		return reconnectDelay
	}})
	h.start()
	shown := 0
	ui := &scriptedUI{answers: []string{"docs", "reconnect", "", ""}}

	// The reconnect closes the old transport before it opens a new one; the display state stays "connected" meanwhile.
	start := time.Now()
	if err := h.ext.RunCommand(t.Context(), "", tuiCommandContext(h, ui, &shown)); err != nil {
		t.Fatal(err)
	}
	h.ext.ToolCall(t.Context(), "mcp__docs__search", map[string]any{"query": "q"})

	if elapsed := time.Since(start); elapsed < reconnectDelay {
		t.Fatalf("the call returned after %s, before the reconnect finished (%s)", elapsed, reconnectDelay)
	}
	if state := h.ext.DescribeState("docs", false); state != "connected · 3 tools" {
		t.Fatalf("state after the call's wait = %q", state)
	}
}

func TestMcpPreparedToolCallResolvesTheConnectionWhenItExecutes(t *testing.T) {
	h := setupSession(t, extension.McpExposureDirect, nil, setupOptions{configPath: writeDocsConfig(t)})
	h.start()
	prepared, ok := h.host.tool("mcp__docs__search")
	if !ok {
		t.Fatal("mcp__docs__search is not registered")
	}
	call := func() error {
		_, err := prepared.Execute(t.Context(), "call-1", []byte(`{"query":"q"}`), nil)
		return err
	}

	// Disabled: the call does not reach the closed connection.
	if failed := h.ext.SetEnabled(h.ctx, "docs", false); failed != "" {
		t.Fatal(failed)
	}
	if err := call(); err == nil || err.Error() != `MCP server "docs" is disabled.` {
		t.Fatalf("call while disabled: err = %v", err)
	}

	// Re-enabled: the definition prepared before resolves the replacement connection.
	if failed := h.ext.SetEnabled(h.ctx, "docs", true); failed != "" {
		t.Fatal(failed)
	}
	h.ext.Pending()
	if err := call(); err != nil {
		t.Fatalf("call after re-enable: %v", err)
	}
}

func TestMcpPreparedToolCallFailsWhenTheServerNoLongerOffersTheTool(t *testing.T) {
	var offered []map[string]any
	offered = slices.Clone(serverTools)
	h := setupSession(t, extension.McpExposureDirect, func() []map[string]any { return slices.Clone(offered) }, setupOptions{configPath: writeDocsConfig(t)})
	h.start()
	prepared, _ := h.host.tool("mcp__docs__search")
	offered = offered[1:]
	if message := h.ext.Reconnect(t.Context(), "docs"); message != "" {
		t.Fatal(message)
	}

	_, err := prepared.Execute(t.Context(), "call-1", []byte(`{"query":"q"}`), nil)

	if err == nil || err.Error() != `MCP tool "docs/search" is no longer available.` {
		t.Fatalf("err = %v", err)
	}
}
