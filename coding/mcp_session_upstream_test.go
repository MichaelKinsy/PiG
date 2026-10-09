//go:build !pig_strip_mcp

package coding

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 0.99.2), "AgentSession MCP integration": the
// whole Session with the built-in codemode, tool-search and MCP extensions, loaded as the CLI loads them. The extension
// boundary forms of the same cases are in coding/mcpext/extension_test.go.

func requireEqual[T any](t *testing.T, what string, got, want T) {
	t.Helper()
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("%s = %s, want %s", what, gotJSON, wantJSON)
	}
}

func requireNotContains(t *testing.T, what, text, substring string) {
	t.Helper()
	if strings.Contains(text, substring) {
		t.Errorf("%s contains %q: %q", what, substring, text)
	}
}

func mcpLastLine(t *testing.T, result agent.ToolResultMessage) string {
	t.Helper()
	lines := strings.Split(mcpText(result), "\n")
	return lines[len(lines)-1]
}

func TestAgentSessionMCPExposesCodemodeOnlyMCPToolsThroughCodemodeAndHidesThemFromTheModel(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{})
	searchName := mcpext.CreateMcpToolName("docs", "search", nil)
	// MCP tools resolve to their CallToolResult, errors included.
	h.respond(mcpCodemode(`
							const [a, b] = await Promise.allSettled([
								tools.`+searchName+`({ query: "mcp" }),
								tools.`+searchName+`({ query: "pi" }),
							]);
							const failure = await tools.mcp__docs__fail({});
							const shot = await tools.mcp__docs__shot({});
							image(shot.content[0]);
							text(JSON.stringify({
								hits: [...a.value.structuredContent.hits, ...b.value.structuredContent.hits],
								failed: failure.isError,
								failure: failure.content[0].text,
								found: ALL_TOOLS.filter((tool) => tool.name.includes("search")).map((tool) => tool.name),
							}));
						`), mcpDone())

	h.prompt(t, "search the docs")

	// Exec was activated for the codemode-exposed server; MCP tools are never declared.
	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"codemode"})
	requireEqual(t, "declared tools", h.declaredToolNames(), []string{"codemode"})
	requireEqual(t, "nested tools", h.nestedToolNames(), []string{searchName, "mcp__docs__fail", "mcp__docs__shot"})
	// The description lists neither the server nor its tools; scripts search for them.
	description, ok := h.description("codemode")
	if !ok {
		t.Fatal("no codemode tool")
	}
	requireNotContains(t, "codemode description", description, "mcp__docs")
	requireNotContains(t, "codemode description", description, "Shared MCP Types:")

	result := h.toolResult(t, "codemode")
	if result.IsError {
		t.Errorf("codemode failed: %s", mcpText(result))
	}
	// Output items keep the order the script produced them in; each image follows the path it was saved to.
	if len(result.Content) != 4 {
		t.Fatalf("content = %#v, want 4 items", result.Content)
	}
	if got := checkSavedImages(t, result.Content[1].(ai.TextContent).Text); got != "<saved>" {
		t.Errorf("content[1] = %#v", result.Content[1])
	}
	if image, ok := result.Content[2].(ai.ImageContent); !ok || image.Data != mcpTinyPNGBase64 || image.MimeType != "image/png" {
		t.Errorf("content[2] = %#v", result.Content[2])
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content[3].(ai.TextContent).Text), &got); err != nil {
		t.Fatal(err)
	}
	requireEqual(t, "script output", got, map[string]any{
		"hits":    []any{"mcp guide", "mcp faq", "pi guide", "pi faq"},
		"failed":  true,
		"failure": "server exploded",
		"found":   []any{searchName},
	})
	requireEqual(t, "server calls", h.calls.all(), []string{`search:{"query":"mcp"}`, `search:{"query":"pi"}`, "fail:{}", "shot:{}"})
}

func TestAgentSessionMCPKeepsCodemodeOnlyMCPToolsCallableAcrossTreeNavigation(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{})
	searchName := mcpext.CreateMcpToolName("docs", "search", nil)
	h.respond(boundaryReply("one", ai.StopReasonStop, 0), boundaryReply("two", ai.StopReasonStop, 0))
	h.prompt(t, "first")
	h.prompt(t, "second")
	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"codemode"})
	if !slices.Contains(h.nestedToolNames(), searchName) {
		t.Fatalf("nested tools = %v", h.nestedToolNames())
	}

	var firstAssistant string
	for _, entry := range h.session.Inner().GetBranch() {
		if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.Assistant != nil {
			firstAssistant = entry.Base().ID
			break
		}
	}
	if firstAssistant == "" {
		t.Fatal("No assistant entry")
	}
	if _, err := h.session.NavigateTree(t.Context(), firstAssistant, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"codemode"})
	if !slices.Contains(h.nestedToolNames(), searchName) {
		t.Fatalf("nested tools = %v", h.nestedToolNames())
	}
}

// Regression: #10239.
func TestAgentSessionMCPRoutesToolsWhoseNamesDifferOnlyInDashAndUnderscore(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "in order", true: "reversed"}[reverse], func(t *testing.T) {
			tools := []map[string]any{
				{"name": "read-file", "description": "dashed", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
				{"name": "read_file", "description": "underscored", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
			}
			if reverse {
				slices.Reverse(tools)
			}
			h := setupMCP(t, extension.McpExposureCodemode, func() []map[string]any { return append(slices.Clone(tools), mcpServerTools...) }, mcpSessionOptions{})
			h.respond(mcpCodemode(`for (const query of ["dashed", "underscored"]) await tools[(await searchTools(query))[0].name]({});`), mcpDone())

			h.prompt(t, "go")

			requireEqual(t, "server calls", h.calls.all(), []string{"read-file:{}", "read_file:{}"})
		})
	}
}

func TestAgentSessionMCPRejectsDirectModelCallsToCodemodeOnlyMCPTools(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{})
	h.respond(mcpToolCalls(mcpToolCall{mcpext.CreateMcpToolName("docs", "search", nil), ai.JsonObject{"query": "x"}}), mcpDone())

	h.prompt(t, "go")

	result := h.toolResult(t, "mcp__docs__search")
	if !result.IsError {
		t.Error("isError = false")
	}
	requireEqual(t, "result text", mcpText(result), "Tool mcp__docs__search not found")
	requireEqual(t, "server calls", h.calls.all(), []string(nil))
}

func TestAgentSessionMCPDeclaresDirectlyExposedMCPToolsToTheModel(t *testing.T) {
	h := setupMCP(t, extension.McpExposureDirect, nil, mcpSessionOptions{})
	h.respond(mcpToolCalls(mcpToolCall{"mcp__docs__search", ai.JsonObject{"query": "direct"}}), mcpDone())

	h.prompt(t, "go")

	requireEqual(t, "declared tools", h.declaredToolNames(), []string{"mcp__docs__search", "mcp__docs__fail", "mcp__docs__shot"})
	if slices.Contains(h.session.ActiveToolNames(), "codemode") {
		t.Errorf("active tools = %v", h.session.ActiveToolNames())
	}
	// Boolean annotation hints are passed on for permission extensions.
	annotations := map[string]*extension.ToolAnnotations{}
	for _, tool := range h.session.GetAllTools() {
		annotations[tool.Name] = tool.Annotations
	}
	requireEqual(t, "fail annotations", annotations["mcp__docs__fail"], &extension.ToolAnnotations{DestructiveHint: new(true), ReadOnlyHint: new(false)})
	if annotations["mcp__docs__search"] != nil {
		t.Errorf("search annotations = %+v, want none", annotations["mcp__docs__search"])
	}
	requireEqual(t, "result text", mcpText(h.toolResult(t, "mcp__docs__search")), "direct guide\ndirect faq")
}

func TestAgentSessionMCPWithdrawsAndRestoresMCPToolsTheServerChanges(t *testing.T) {
	for _, exposure := range []extension.McpExposure{extension.McpExposureDirect, extension.McpExposureCodemode} {
		t.Run(string(exposure), func(t *testing.T) {
			var mu sync.Mutex
			tools := mcpServerTools
			h := setupMCP(t, exposure, func() []map[string]any {
				mu.Lock()
				defer mu.Unlock()
				return tools
			}, mcpSessionOptions{})
			h.respond(boundaryReply("ready", ai.StopReasonStop, 0))
			h.prompt(t, "start")
			// Direct tools are declared to the model, codemode tools are only callable from codemode.
			reachable := func() []string {
				if exposure == extension.McpExposureDirect {
					return h.session.ActiveToolNames()
				}
				return h.nestedToolNames()
			}
			listChanged := func(want func([]string) bool) {
				t.Helper()
				if err := h.lastServer().Send(mcp.NewNotification("notifications/tools/list_changed", nil)); err != nil {
					t.Fatal(err)
				}
				mcpWaitFor(t, "the tool list to change", func() bool { return want(reachable()) })
			}
			if !slices.Contains(reachable(), "mcp__docs__fail") {
				t.Fatalf("reachable = %v", reachable())
			}

			mu.Lock()
			tools = nil
			for _, tool := range mcpServerTools {
				if tool["name"] != "fail" {
					tools = append(tools, tool)
				}
			}
			mu.Unlock()
			listChanged(func(names []string) bool { return !slices.Contains(names, "mcp__docs__fail") })
			if !slices.Contains(reachable(), "mcp__docs__search") {
				t.Fatalf("reachable = %v", reachable())
			}
			description, _ := h.description("codemode")
			requireNotContains(t, "codemode description", description, "mcp__docs__fail")

			mu.Lock()
			tools = mcpServerTools
			mu.Unlock()
			listChanged(func(names []string) bool { return slices.Contains(names, "mcp__docs__fail") })
		})
	}
}

func TestAgentSessionMCPListsAndReadsResourcesWithCodexResourceTools(t *testing.T) {
	h := setupMCP(t, extension.McpExposureDirect, nil, mcpSessionOptions{resources: true})
	h.respond(
		mcpToolCalls(
			mcpToolCall{"list_mcp_resources", ai.JsonObject{}},
			mcpToolCall{"list_mcp_resource_templates", ai.JsonObject{"server": "docs"}},
			mcpToolCall{"read_mcp_resource", ai.JsonObject{"server": "docs", "uri": "docs://pages/setup"}},
		),
		mcpToolCalls(mcpToolCall{"read_mcp_resource", ai.JsonObject{"server": "nope", "uri": "docs://x"}}),
		mcpDone(),
	)

	h.prompt(t, "read")

	// The resource tools take the exposure of the servers they reach.
	for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
		if !slices.Contains(h.session.ActiveToolNames(), name) {
			t.Errorf("active tools = %v, want %s", h.session.ActiveToolNames(), name)
		}
	}
	var listed map[string]any
	if err := json.Unmarshal([]byte(mcpText(h.toolResult(t, "list_mcp_resources"))), &listed); err != nil {
		t.Fatal(err)
	}
	requireEqual(t, "resources", listed, map[string]any{"resources": []any{map[string]any{"server": "docs", "uri": "docs://intro", "name": "intro", "mimeType": "text/markdown"}}})
	var templates map[string]any
	if err := json.Unmarshal([]byte(mcpText(h.toolResult(t, "list_mcp_resource_templates"))), &templates); err != nil {
		t.Fatal(err)
	}
	requireEqual(t, "resource templates", templates, map[string]any{"server": "docs", "resourceTemplates": []any{map[string]any{"server": "docs", "uriTemplate": "docs://pages/{slug}", "name": "page"}}})
	var reads []agent.ToolResultMessage
	for _, message := range h.session.Messages() {
		if message.ToolResult != nil && message.ToolResult.ToolName == "read_mcp_resource" {
			reads = append(reads, *message.ToolResult)
		}
	}
	if len(reads) != 2 {
		t.Fatalf("read_mcp_resource results = %d", len(reads))
	}
	requireEqual(t, "first read", mcpText(reads[0]), "# docs://pages/setup")
	if !reads[1].IsError {
		t.Error("second read is not an error")
	}
	requireEqual(t, "second read", mcpText(reads[1]), `MCP server "nope" has no resources. Servers with resources: docs`)
	requireEqual(t, "server calls", h.calls.all(), []string{"read:docs://pages/setup"})
	for _, tool := range h.session.GetAllTools() {
		if tool.Name == "read_mcp_resource" {
			requireEqual(t, "annotations", tool.Annotations, &extension.ToolAnnotations{ReadOnlyHint: new(true)})
		}
	}
}

func TestAgentSessionMCPMakesTheResourceToolsCallableFromCodemodeForCodemodeServers(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{resources: true})
	h.respond(mcpCodemode(`const listed = await tools.list_mcp_resources({ server: "docs" });
const read = await tools.read_mcp_resource({ server: "docs", uri: listed.resources[0].uri });
return { uris: listed.resources.map((r) => r.uri), text: read.contents[0].text };`), mcpDone())

	h.prompt(t, "read")

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"codemode"})
	var got map[string]any
	if err := json.Unmarshal([]byte(mcpLastLine(t, h.toolResult(t, "codemode"))), &got); err != nil {
		t.Fatal(err)
	}
	requireEqual(t, "script result", got, map[string]any{"uris": []any{"docs://intro"}, "text": "# docs://intro"})
	requireEqual(t, "server calls", h.calls.all(), []string{"read:docs://intro"})
}

func TestAgentSessionMCPAppliesPerToolExposureOverrides(t *testing.T) {
	h := setupMCP(t, extension.McpExposureHidden, nil, mcpSessionOptions{toolExposure: extension.NewOrderedExposures("search", "direct", "s*", "codemode")})
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))
	h.prompt(t, "start")

	// `search` is declared, `shot` is only callable from codemode, `fail` keeps the server's `hidden`.
	requireEqual(t, "declared tools", h.declaredToolNames(), []string{"codemode", "mcp__docs__search"})
	requireEqual(t, "nested tools", h.nestedToolNames(), []string{"mcp__docs__search", "mcp__docs__shot"})
	description, _ := h.description("codemode")
	requireNotContains(t, "codemode description", description, "mcp__docs__shot")
	requireNotContains(t, "codemode description", description, "mcp__docs__fail")
}

func TestAgentSessionMCPDescribesTheServerWithItsConfiguredDescriptionAndReturnsItsInstructionsToScripts(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{
		description:  "Search the product docs",
		instructions: "Always search before reading.",
		builtInTools: []string{"tool_search"},
	})
	h.respond(mcpCodemode(`const docs = await describeNamespace("mcp__docs");
const aliases = await Promise.all(["docs", "mcp__docs"].map((name) => describeNamespace(name)));
return { docs, sameForAliases: aliases.every((alias) => JSON.stringify(alias) === JSON.stringify(docs)), none: await describeNamespace("mcp__nope") };`), mcpDone())

	h.prompt(t, "go")

	// The instructions stay out of every tool description.
	for _, name := range []string{"codemode", "tool_search"} {
		description, _ := h.description(name)
		requireNotContains(t, name+" description", description, "Always search")
	}
	codemodeDescription, _ := h.description("codemode")
	requireNotContains(t, "codemode description", codemodeDescription, "mcp__docs")
	if description, _ := h.description("tool_search"); description != toolsearch.ToolSearchDescription {
		t.Errorf("tool_search description = %q, want TOOL_SEARCH_DESCRIPTION", description)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(mcpLastLine(t, h.toolResult(t, "codemode"))), &got); err != nil {
		t.Fatal(err)
	}
	requireEqual(t, "script result", got, map[string]any{
		"docs": map[string]any{
			"name":         "mcp__docs",
			"description":  "Search the product docs",
			"instructions": "Always search before reading.",
			"tools":        []any{"mcp__docs__search", "mcp__docs__fail", "mcp__docs__shot"},
		},
		"sameForAliases": true,
	})
	// The system prompt lists the server with its configured description rather than its instructions.
	if section, _ := h.serversSection(); !strings.Contains(section, "- mcp__docs (codemode): Search the product docs") {
		t.Errorf("servers section = %q", section)
	}
}

func TestAgentSessionMCPListsServersByTheFirstLineOfTheirInstructionsWithoutAConfiguredDescription(t *testing.T) {
	h := setupMCP(t, extension.McpExposureDeferred, nil, mcpSessionOptions{instructions: "Docs search.\nLong guidance."})
	h.respond(boundaryReply("done", ai.StopReasonStop, 0))

	h.prompt(t, "go")

	section, _ := h.serversSection()
	if !strings.Contains(section, "- mcp__docs (tool_search): Docs search.\n") {
		t.Errorf("servers section = %q", section)
	}
	requireNotContains(t, "servers section", section, "Long guidance")
}

func TestAgentSessionMCPDoesNotActivateCodemodeWhenAutoEnableCodemodeIsFalse(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{autoEnableCodemode: new(false)})
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))
	h.prompt(t, "start")

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{})
	if !slices.Contains(h.nestedToolNames(), "mcp__docs__search") {
		t.Errorf("nested tools = %v", h.nestedToolNames())
	}
	requireEqual(t, "notifications", h.notes.all(), []string{"MCP tools are only reachable from the codemode or tool_search tool, but neither is active (autoEnableCodemode is false); they cannot be called."})
}

func TestAgentSessionMCPTreatsCodemodeMCPToolsAsReachableThroughAnActiveToolSearch(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{autoEnableCodemode: new(false), builtInTools: []string{"tool_search"}})
	h.respond(
		mcpToolCalls(mcpToolCall{"tool_search", ai.JsonObject{"query": "search the docs", "limit": float64(1)}}),
		mcpToolCalls(mcpToolCall{"mcp__docs__search", ai.JsonObject{"query": "loaded"}}),
		mcpDone(),
	)
	h.prompt(t, "go")

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"tool_search", "mcp__docs__search"})
	requireEqual(t, "result text", mcpText(h.toolResult(t, "mcp__docs__search")), "loaded guide\nloaded faq")
	requireEqual(t, "notifications", h.notes.all(), []string(nil))
}

func TestAgentSessionMCPReachesDeferredMCPToolsThroughCodemodeWhenToolSearchIsNotAvailable(t *testing.T) {
	h := setupMCP(t, extension.McpExposureDeferred, nil, mcpSessionOptions{builtInTools: []string{"codemode"}, withoutToolSearch: true})
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))
	h.prompt(t, "start")

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"codemode"})
	if !slices.Contains(h.nestedToolNames(), "mcp__docs__search") {
		t.Errorf("nested tools = %v", h.nestedToolNames())
	}
	requireEqual(t, "notifications", h.notes.all(), []string(nil))
}

func TestAgentSessionMCPWarnsWhenDeferredMCPToolsHaveNeitherToolSearchNorCodemode(t *testing.T) {
	h := setupMCP(t, extension.McpExposureDeferred, nil, mcpSessionOptions{withoutToolSearch: true})
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))
	h.prompt(t, "start")

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{})
	requireEqual(t, "notifications", h.notes.all(), []string{"MCP tools are only reachable from the codemode or tool_search tool, but neither is active; they cannot be called."})
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:2044 (RegisteredTool.sourceInfo).
func TestAgentSessionMCPDoesNotActivateAnotherExtensionsToolNamedCodemode(t *testing.T) {
	// Registered first, so it wins over the codemode extension's codemode.
	inactive := false
	otherInfo := extension.SourceInfo(extension.SourceInfo{Path: "/extensions/other.ts", Source: "local", Scope: "temporary", Origin: "top-level"})
	other := extension.Extension{
		Name: "other", Path: "/extensions/other.ts", ResolvedPath: "/extensions/other.ts", SourceInfo: otherInfo,
		Tools: map[string]extension.RegisteredTool{"codemode": {SourceInfo: otherInfo, Definition: extension.ToolDefinition{
			Name: "codemode", Label: "codemode", Description: "Another extension's codemode tool.", Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
			DefaultActive: &inactive,
			Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
				return extension.AgentToolResult{}, nil
			},
		}}},
		ToolOrder: []string{"codemode"},
	}
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{extensions: []extension.Extension{other}})
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))
	h.prompt(t, "start")

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{})
}

// setupSlow is setupSlow() of agent-session-mcp.test.ts: a server named `slow` (by default) whose answer to `initialize`
// takes initializeDelay, without waiting for it.
func setupSlow(t *testing.T, config mcpSessionOptions, exposure extension.McpExposure, initializeDelay time.Duration) *mcpSession {
	t.Helper()
	if config.name == "" {
		config.name = "slow"
	}
	config.initializeDelay = initializeDelay
	return newMCPSession(t, exposure, nil, config)
}

// never is an initializeDelay for a server that never answers `initialize`.
const never = -time.Second

func TestAgentSessionMCPDoesNotHoldTheFirstPromptForCodemodeServersThatAreStillConnecting(t *testing.T) {
	// The server never answers `initialize`.
	h := setupSlow(t, mcpSessionOptions{}, extension.McpExposureCodemode, never)
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))

	h.prompt(t, "start")

	requireEqual(t, "assistant texts", assistantTexts(h), []string{"ready"})
	// Codemode is activated from the config, before the server connects.
	requireEqual(t, "declared tools", h.declaredToolNames(), []string{"codemode"})
}

func assistantTexts(h *mcpSession) []string {
	texts := []string{}
	for _, message := range h.session.Messages() {
		if message.Assistant == nil {
			continue
		}
		for _, block := range message.Assistant.Content {
			if block, ok := block.(ai.TextContent); ok {
				texts = append(texts, block.Text)
			}
		}
	}
	return texts
}

func TestAgentSessionMCPKeepsTheCodemodeDescriptionUnchangedWhenTheServerConnects(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{}, extension.McpExposureCodemode, 20*time.Millisecond)
	before, ok := h.description("codemode")
	if !ok {
		t.Fatal("no codemode tool")
	}
	mcpWaitFor(t, "mcp__slow__search to be callable", func() bool { return slices.Contains(h.nestedToolNames(), "mcp__slow__search") })
	if after, _ := h.description("codemode"); after != before {
		t.Errorf("codemode description changed:\n%q\nto\n%q", before, after)
	}
}

func TestAgentSessionMCPListsAServerBeforeItConnectsAndAppendsItsSummaryWithTheNextPrompt(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{instructions: "Slow docs.\nMore."}, extension.McpExposureCodemode, 30*time.Millisecond)
	h.respond(boundaryReply("one", ai.StopReasonStop, 0), boundaryReply("two", ai.StopReasonStop, 0))

	h.prompt(t, "first")
	mcpWaitFor(t, "mcp__slow__search to be callable", func() bool { return slices.Contains(h.nestedToolNames(), "mcp__slow__search") })
	h.prompt(t, "second")

	systemMessages := h.systemMessages()
	// The first request lists the server by name; its summary follows as an appended patch.
	if len(systemMessages) != 2 {
		t.Fatalf("system messages = %d, want 2", len(systemMessages))
	}
	sectionOf := func(message *ai.SystemMessage) (string, bool) {
		for _, section := range message.Sections {
			if section.Name == mcpext.McpServersSection && section.Value != nil {
				return *section.Value, true
			}
		}
		return "", false
	}
	if first, _ := sectionOf(systemMessages[0]); !strings.Contains(first, "- mcp__slow (codemode)\n") {
		t.Errorf("first section = %q", first)
	}
	if len(systemMessages[1].Sections) != 1 {
		t.Errorf("second sections = %+v, want only the servers section", systemMessages[1].Sections)
	}
	if second, _ := sectionOf(systemMessages[1]); !strings.Contains(second, "- mcp__slow (codemode): Slow docs.") {
		t.Errorf("second section = %q", second)
	}
	messages := h.session.Messages()
	secondIndex := slices.IndexFunc(messages, func(message agent.AgentMessage) bool { return message.System == systemMessages[1] })
	firstAssistant := slices.IndexFunc(messages, func(message agent.AgentMessage) bool { return message.Assistant != nil })
	if secondIndex <= firstAssistant {
		t.Errorf("second system message at %d, first assistant message at %d", secondIndex, firstAssistant)
	}
}

func TestAgentSessionMCPWaitsForServersWithDirectToolsBeforeListingTheServers(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{toolExposure: extension.NewOrderedExposures("shot", "codemode"), instructions: "Slow docs."}, extension.McpExposureDirect, 30*time.Millisecond)
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))

	h.prompt(t, "start")

	if section, _ := h.serversSection(); !strings.Contains(section, "- mcp__slow (codemode): Slow docs.") {
		t.Errorf("servers section = %q", section)
	}
}

func TestAgentSessionMCPLeavesServersWithOnlyDirectToolsOutOfTheServersSection(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{}, extension.McpExposureDirect, 0)
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))

	h.prompt(t, "start")

	if section, present := h.serversSection(); present {
		t.Errorf("servers section = %q, want none", section)
	}
}

func TestAgentSessionMCPWaitsForTheServersACodemodeScriptNames(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{}, extension.McpExposureCodemode, 30*time.Millisecond)
	h.respond(mcpCodemode(`return (await tools.mcp__slow__search({ query: "q" })).structuredContent;`), mcpDone())

	h.prompt(t, "go")

	result := h.toolResult(t, "codemode")
	if result.IsError {
		t.Errorf("codemode failed: %s", mcpText(result))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(mcpLastLine(t, result)), &got); err != nil {
		t.Fatal(err)
	}
	requireEqual(t, "script result", got, map[string]any{"hits": []any{"q guide", "q faq"}})
	requireEqual(t, "server calls", h.calls.all(), []string{`search:{"query":"q"}`})
}

func TestAgentSessionMCPWaitsForServersWhoseScriptIdentifiersDifferFromTheirNames(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{name: "slow-docs"}, extension.McpExposureCodemode, 30*time.Millisecond)
	h.respond(mcpCodemode(`await tools.mcp__slow_docs__search({ query: "q" }); return (await describeNamespace("slow_docs")).name;`), mcpDone())

	h.prompt(t, "go")

	result := h.toolResult(t, "codemode")
	if result.IsError {
		t.Errorf("codemode failed: %s", mcpText(result))
	}
	requireEqual(t, "last line", mcpLastLine(t, result), "mcp__slow_docs")
	requireEqual(t, "server calls", h.calls.all(), []string{`search:{"query":"q"}`})
}

func TestAgentSessionMCPDoesNotWaitForServersACodemodeScriptDoesNotName(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{}, extension.McpExposureCodemode, never)
	h.respond(mcpCodemode("return 1 + 1;"), mcpDone())

	h.prompt(t, "go")

	requireEqual(t, "last line", mcpLastLine(t, h.toolResult(t, "codemode")), "2")
}

func TestAgentSessionMCPWaitsForServersBeforeToolSearchSearches(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{}, extension.McpExposureDeferred, 30*time.Millisecond)
	h.respond(
		mcpToolCalls(mcpToolCall{"tool_search", ai.JsonObject{"query": "search the docs", "limit": float64(1)}}),
		mcpToolCalls(mcpToolCall{"mcp__slow__search", ai.JsonObject{"query": "late"}}),
		mcpDone(),
	)

	h.prompt(t, "go")

	requireEqual(t, "result text", mcpText(h.toolResult(t, "mcp__slow__search")), "late guide\nlate faq")
}

func TestAgentSessionMCPHoldsTheFirstPromptForServersWithDirectTools(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{}, extension.McpExposureDirect, 30*time.Millisecond)
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))

	h.prompt(t, "start")

	if !slices.Contains(h.declaredToolNames(), "mcp__slow__search") {
		t.Errorf("declared tools = %v", h.declaredToolNames())
	}
}

func TestAgentSessionMCPHoldsTheFirstPromptForServersWithDirectToolsOnlyUpToStartupWait(t *testing.T) {
	h := setupSlow(t, mcpSessionOptions{startupWait: new(20)}, extension.McpExposureDirect, never)
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))

	h.prompt(t, "start")

	requireEqual(t, "assistant texts", assistantTexts(h), []string{"ready"})
	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{})
	requireEqual(t, "notifications", h.notes.all(), []string{"MCP servers are still connecting; their tools become available once connected."})
}

func TestAgentSessionMCPDoesNotLetCodemodeCallItselfOrInactiveDirectTools(t *testing.T) {
	h := setupMCP(t, extension.McpExposureDirect, nil, mcpSessionOptions{})
	h.respond(boundaryReply("ready", ai.StopReasonStop, 0))
	h.prompt(t, "start")
	h.session.SetActiveToolsByName([]string{"codemode", "mcp__docs__search"})

	requireEqual(t, "nested tools", h.nestedToolNames(), []string{"mcp__docs__search"})
}

func TestAgentSessionMCPFindsToolsFromScriptsWithSearchToolsAndDescribeTool(t *testing.T) {
	h := setupMCP(t, extension.McpExposureCodemode, nil, mcpSessionOptions{})
	h.respond(mcpCodemode(`
							const [match] = await searchTools("search the docs", { limit: 1 });
							const none = await searchTools("docs", { namespace: "mcp__other" });
							const declaration = await describeTool(match.name);
							const result = await tools[match.name]({ query: "found" });
							text(JSON.stringify({
								name: match.name,
								sameAsAllTools: ALL_TOOLS.find((tool) => tool.name === match.name).description === match.description,
								none: none.length,
								declared: declaration.includes("codemode tool declaration:"),
								missing: (await describeTool("nope")) === undefined,
								hits: result.structuredContent.hits,
							}));
						`), mcpDone())

	h.prompt(t, "go")

	result := h.toolResult(t, "codemode")
	if result.IsError {
		t.Errorf("codemode failed: %s", mcpText(result))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content[1].(ai.TextContent).Text), &got); err != nil {
		t.Fatal(err)
	}
	requireEqual(t, "script output", got, map[string]any{
		"name":           "mcp__docs__search",
		"sameAsAllTools": true,
		"none":           0,
		"declared":       true,
		"missing":        true,
		"hits":           []any{"found guide", "found faq"},
	})
	requireEqual(t, "server calls", h.calls.all(), []string{`search:{"query":"found"}`})
}

func TestAgentSessionMCPActivatesToolSearchForDeferredMCPToolsAndKeepsLoadedToolsDeclaredOnTheBranch(t *testing.T) {
	// No built-in tools are active; the MCP extension activates tool_search, not codemode.
	h := setupMCP(t, extension.McpExposureDeferred, nil, mcpSessionOptions{})
	searchName := mcpext.CreateMcpToolName("docs", "search", nil)
	h.respond(
		mcpToolCalls(mcpToolCall{"tool_search", ai.JsonObject{"query": "search the docs", "limit": float64(1)}}),
		mcpToolCalls(mcpToolCall{searchName, ai.JsonObject{"query": "loaded"}}),
		mcpDone(),
	)
	h.prompt(t, "find a docs tool")

	requireEqual(t, "active tools", h.session.ActiveToolNames(), []string{"tool_search", searchName})
	// The description does not depend on the connected servers.
	if description, _ := h.description("tool_search"); description != toolsearch.ToolSearchDescription {
		t.Errorf("tool_search description = %q", description)
	}

	requireEqual(t, "tool_search result", mcpText(h.toolResult(t, "tool_search")), "Loaded 1 tool. They are available from your next call:\n- "+searchName+": Search the docs.")
	// Only the loaded tool is added; earlier declarations are not repeated.
	var loadMessages []*ai.SystemMessage
	for _, message := range h.systemMessages() {
		if slices.ContainsFunc(message.ToolsAdded, func(tool ai.ToolSchema) bool { return tool.Name == searchName }) {
			loadMessages = append(loadMessages, message)
		}
	}
	if len(loadMessages) != 1 {
		t.Fatalf("load messages = %d, want 1", len(loadMessages))
	}
	var added []string
	for _, tool := range loadMessages[0].ToolsAdded {
		added = append(added, tool.Name)
	}
	requireEqual(t, "added tools", added, []string{searchName})
	requireEqual(t, "loaded tool result", mcpText(h.toolResult(t, searchName)), "loaded guide\nloaded faq")
	requireEqual(t, "server calls", h.calls.all(), []string{`search:{"query":"loaded"}`})

	// Loads are recorded in the transcript: navigating back before the load drops the tool,
	// navigating to a later entry restores it.
	branch := h.session.Inner().GetBranch()
	var firstUser, last string
	for _, entry := range branch {
		if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.User != nil && firstUser == "" {
			firstUser = entry.Base().ID
		}
	}
	if len(branch) > 0 {
		last = branch[len(branch)-1].Base().ID
	}
	if firstUser == "" || last == "" {
		t.Fatal("Missing entries")
	}
	if _, err := h.session.NavigateTree(t.Context(), firstUser, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(h.session.ActiveToolNames(), searchName) {
		t.Errorf("active tools = %v, want %s dropped", h.session.ActiveToolNames(), searchName)
	}
	if _, err := h.session.NavigateTree(t.Context(), last, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.session.ActiveToolNames(), searchName) {
		t.Errorf("active tools = %v, want %s restored", h.session.ActiveToolNames(), searchName)
	}
}

func TestAgentSessionMCPFindsNothingToLoadWhenEveryMatchingToolIsAlreadyDeclared(t *testing.T) {
	h := setupMCP(t, extension.McpExposureDirect, nil, mcpSessionOptions{builtInTools: []string{"tool_search"}})
	h.respond(mcpToolCalls(mcpToolCall{"tool_search", ai.JsonObject{"query": "docs"}}), mcpDone())

	h.prompt(t, "go")

	requireEqual(t, "tool_search result", mcpText(h.toolResult(t, "tool_search")), "No matching tools found.")
}
