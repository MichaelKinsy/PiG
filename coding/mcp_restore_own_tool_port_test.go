//go:build !pig_strip_mcp

package coding

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/suite/agent-session-mcp.test.ts (Pi 3345c2925c59, #12054): like extensions that register
// a loader tool and hide it at session_start. The restore or reload activates the loader as a tool the branch never
// recorded, and the extension deactivates it again; that must not drop the restored MCP tools that are still pending.
func dropLoaderExtension() extension.Extension {
	return extension.Extension{
		Name: "drop-loader", Path: "/drop-loader", ResolvedPath: "/drop-loader",
		ToolOrder: []string{"loader"},
		Tools: map[string]extension.RegisteredTool{"loader": {Definition: extension.ToolDefinition{
			Name: "loader", Label: "loader", Description: "Loads the extension's tools.", Parameters: json.RawMessage(`{"type":"object"}`),
			Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
				return extension.AgentToolResult{}, nil
			},
		}}},
		Handlers: map[string][]extension.HandlerFn{
			"session_start": {func(args ...any) (any, error) {
				ctx, _ := args[1].(context.Context)
				pi := extension.FromContext(ctx)
				pi.SetActiveTools(slices.DeleteFunc(pi.GetActiveTools(), func(name string) bool { return name == "loader" }))
				return nil, nil
			}},
		},
	}
}

func TestAgentSessionMCPToolsAfterResumeAndReloadKeepsRestoredToolsWhenAnExtensionDeactivatesOnlyItsOwnToolBeforeTheyRegister(t *testing.T) {
	first := setupMCPResume(t, nil, []func() extension.Extension{dropLoaderExtension}, 0)
	first.loadDocsSearch(t)
	second := setupMCPResume(t, first.session.Inner(), []func() extension.Extension{dropLoaderExtension}, 0)
	mcpWaitFor(t, "mcp__docs__search to be registered", func() bool { return second.hasTool("mcp__docs__search") })
	mcpWaitFor(t, "mcp__docs__search to be active", func() bool {
		return slices.Contains(second.session.ActiveToolNames(), "mcp__docs__search")
	})
	if slices.Contains(second.session.ActiveToolNames(), "loader") {
		t.Fatalf("active tools = %v, want loader inactive", second.session.ActiveToolNames())
	}
}

func TestAgentSessionMCPToolsAfterResumeAndReloadKeepsToolsToolSearchLoadedAfterReloadWhenAnExtensionDeactivatesOnlyItsOwnTool(t *testing.T) {
	h := setupMCPResume(t, nil, []func() extension.Extension{dropLoaderExtension}, 0)
	h.loadDocsSearch(t)

	h.reload(t)

	mcpWaitFor(t, `connections ["docs","docs"]`, func() bool { return slices.Equal(h.connected.all(), []string{"docs", "docs"}) })
	mcpWaitFor(t, "mcp__docs__search to be active", func() bool {
		return slices.Contains(h.session.ActiveToolNames(), "mcp__docs__search")
	})
	if slices.Contains(h.session.ActiveToolNames(), "loader") {
		t.Fatalf("active tools = %v, want loader inactive", h.session.ActiveToolNames())
	}
}
