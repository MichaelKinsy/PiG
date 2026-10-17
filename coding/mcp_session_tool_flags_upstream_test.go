//go:build !pig_strip_mcp

package coding

import (
	"slices"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports the 1.0.4 cases of packages/coding-agent/test/suite/agent-session-mcp.test.ts that name `--tools` and
// `--exclude-tools` (the MCP tools stay registered unless an entry starts with `mcp__`, patterns, `--no-tools`).

func toolSet(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

func (s *mcpSession) allToolNames() []string {
	var names []string
	for _, tool := range s.session.GetAllTools() {
		names = append(names, tool.Name)
	}
	return names
}

func requireContainsAll(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("%s = %v, want it to contain %q", what, got, name)
		}
	}
}

// "keeps MCP tools when --tools names no MCP tool"
func TestAgentSessionMCPKeepsMCPToolsWhenToolsNamesNoMCPTool(t *testing.T) {
	for _, exposure := range []extension.McpExposure{extension.McpExposureCodemode, extension.McpExposureDeferred} {
		h := setupMCP(t, exposure, nil, mcpSessionOptions{allowedTools: toolSet("read", "codemode"), resources: true})
		requireEqual(t, string(exposure)+" active tools", h.session.ActiveToolNames(), []string{"read", "codemode"})
		// Pi registers one server's tools in one synchronous step; here they register in turn, resource tools last.
		want := []string{"mcp__docs__search", "mcp__docs__fail", "list_mcp_resources", "read_mcp_resource"}
		mcpWaitFor(t, string(exposure)+" nested tools", func() bool {
			nested := h.nestedToolNames()
			return !slices.ContainsFunc(want, func(name string) bool { return !slices.Contains(nested, name) })
		})
		requireContainsAll(t, string(exposure)+" nested tools", h.nestedToolNames(), want...)
		if slices.Contains(h.allToolNames(), "bash") {
			t.Errorf("%s: tools = %v, want no bash", exposure, h.allToolNames())
		}
	}

	// A direct MCP tool stays registered but is declared only when --tools names it.
	direct := extension.NewOrderedExposures("fail", string(extension.McpExposureDirect))
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{allowedTools: toolSet("codemode"), toolExposure: direct})
	mcpWaitFor(t, "mcp__docs__fail to be registered", func() bool { return h.hasTool("mcp__docs__fail") })
	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"codemode"})
}

// "removes MCP tools with --no-tools"
func TestAgentSessionMCPRemovesMCPToolsWithNoTools(t *testing.T) {
	// The first prompt waits for servers with direct tools, so their tools would be registered by then.
	h := setupMCP(t, extension.McpExposureDirect, nil, mcpSessionOptions{allowedTools: toolSet(), skipToolWait: true})
	h.respond(mcpDone())
	h.prompt(t, "go")

	h.mu.Lock()
	servers := len(h.servers)
	h.mu.Unlock()
	if servers != 1 {
		t.Fatalf("servers = %d, want 1", servers)
	}
	requireEqual(t, "tools", h.allToolNames(), []string(nil))
	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{})
}

// "does not declare unnamed MCP tools restored from the transcript"
func TestAgentSessionMCPDoesNotDeclareUnnamedMCPToolsRestoredFromTheTranscript(t *testing.T) {
	first := setupMCP(t, extension.McpExposureDirect, nil, mcpSessionOptions{})
	first.respond(boundaryReply("one", ai.StopReasonStop, 0), boundaryReply("two", ai.StopReasonStop, 0))
	first.prompt(t, "first")
	first.prompt(t, "second")
	requireContainsAll(t, "declared tools", first.declaredToolNames(), "mcp__docs__search")

	// Like `pi --tools read,codemode -c` followed by /tree.
	second := setupMCP(t, extension.McpExposureDirect, nil, mcpSessionOptions{allowedTools: toolSet("read", "codemode"), sessionManager: first.session.Inner()})
	var firstAssistant string
	for _, entry := range second.session.Inner().GetBranch() {
		if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.Assistant != nil {
			firstAssistant = entry.Base().ID
			break
		}
	}
	if firstAssistant == "" {
		t.Fatal("No assistant entry")
	}
	if _, err := second.session.NavigateTree(t.Context(), firstAssistant, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}

	// The transcript's loadout is restored without the MCP tools it declared.
	requireEqual(t, "active tools", second.session.ActiveToolNames(), []string{})
	requireContainsAll(t, "tools", second.allToolNames(), "mcp__docs__search")
}

// "lets tool_search declare unnamed MCP tools when --tools names it"
func TestAgentSessionMCPLetsToolSearchDeclareUnnamedMCPToolsWhenToolsNamesIt(t *testing.T) {
	h := setupMCP(t, extension.McpExposureDeferred, nil, mcpSessionOptions{allowedTools: toolSet("tool_search")})
	h.respond(mcpToolCalls(mcpToolCall{"tool_search", ai.JsonObject{"query": "search the docs", "limit": float64(1)}}), mcpDone())
	h.prompt(t, "load")

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"tool_search", "mcp__docs__search"})
}

// "filters MCP tools by the mcp__ entries of --tools"
func TestAgentSessionMCPFiltersMCPToolsByTheMcpEntriesOfTools(t *testing.T) {
	direct := extension.NewOrderedExposures("shot", string(extension.McpExposureDirect))
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{allowedTools: toolSet("codemode", "mcp__docs__s*"), resources: true, toolExposure: direct})
	mcpWaitFor(t, "mcp__docs__shot to be active", func() bool {
		return slices.Equal(h.session.ActiveToolNames(), []string{"codemode", "mcp__docs__shot"})
	})
	// The first prompt waits for the connection of a server with direct tools, so its resource tools are registered or
	// filtered out by then (Pi registers them in the same synchronous step as the tools).
	h.respond(mcpDone())
	h.prompt(t, "go")
	registered := h.allToolNames()
	requireContainsAll(t, "registered tools", registered, "mcp__docs__search", "mcp__docs__shot")
	for _, name := range []string{"mcp__docs__fail", "list_mcp_resources"} {
		if slices.Contains(registered, name) {
			t.Errorf("registered tools = %v, want no %s", registered, name)
		}
	}
}

// "removes MCP tools matching --exclude-tools patterns"
func TestAgentSessionMCPRemovesMCPToolsMatchingExcludeToolsPatterns(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{allowedTools: toolSet("codemode"), excludedTools: toolSet("mcp__docs__f*")})
	// The tools register in list order, search, fail, shot: with shot present, a missing fail was filtered out.
	mcpWaitFor(t, "mcp__docs__shot to be registered", func() bool { return h.hasTool("mcp__docs__shot") })
	registered := h.allToolNames()
	requireContainsAll(t, "registered tools", registered, "mcp__docs__search")
	if slices.Contains(registered, "mcp__docs__fail") {
		t.Errorf("registered tools = %v, want no mcp__docs__fail", registered)
	}
}
