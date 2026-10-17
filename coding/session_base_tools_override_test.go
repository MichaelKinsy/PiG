package coding

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func baseOverrideTool(t *testing.T, name string) agent.AgentTool {
	t.Helper()
	tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: name, Label: name, Description: name + " tool", Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: name}}}, nil
		}}})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// upstream: packages/coding-agent/src/core/agent-session.ts:294,3614-3620,3650-3652 and test/test-harness.test.ts:66, test/agent-session-retry.test.ts:309
// (`baseToolsOverride: { echo: echoTool }`): the override replaces the built-in tool set, and Object.keys names the default active tools.
func TestSessionBaseToolsOverrideReplacesTheBuiltinTools(t *testing.T) {
	// "echo" before "dummy" is not sorted order: Object.keys keeps insertion order, so the default active tools are echo, dummy.
	override := []BaseToolOverride{{Name: "echo", Tool: baseOverrideTool(t, "echo")}, {Name: "dummy", Tool: baseOverrideTool(t, "dummy")}}
	session := newRegistryPortSession(t, nil, SessionOptions{BaseToolsOverride: override}, nil, nil)
	if got, want := allRegistryNames(session), []string{"dummy", "echo"}; !slices.Equal(slices.Sorted(slices.Values(got)), want) {
		t.Fatalf("registered tools = %q, want only the override %q (no built-ins)", got, want)
	}
	if got, want := session.ActiveToolNames(), []string{"echo", "dummy"}; !slices.Equal(got, want) {
		t.Fatalf("active tools = %q, want every override key in insertion order %q", got, want)
	}
	// An explicit initial selection wins over the keys (agent-session.ts:3651 `options.activeToolNames ?? defaultActiveToolNames`).
	picked := newRegistryPortSession(t, nil, SessionOptions{BaseToolsOverride: override, InitialActiveToolNames: []string{"echo"}}, nil, nil)
	if got := sortedActiveNames(picked); !slices.Equal(got, []string{"echo"}) {
		t.Fatalf("active with an initial selection = %q, want [echo]", got)
	}
	// Pi's `{}` is truthy: an empty override registers and activates no tools.
	empty := newRegistryPortSession(t, nil, SessionOptions{BaseToolsOverride: []BaseToolOverride{}}, nil, nil)
	if got := registeredAndActiveNames(empty); len(got) != 0 {
		t.Fatalf("an empty override registered %q, want no tools", got)
	}
}

func registeredAndActiveNames(session *Session) []string {
	return append(allRegistryNames(session), session.ActiveToolNames()...)
}

// Without the override the same session registers the built-in tools and activates the default four, so the override test above fails for the right reason.
func TestSessionWithoutBaseToolsOverrideKeepsTheBuiltinTools(t *testing.T) {
	session := newRegistryPortSession(t, nil, SessionOptions{}, nil, nil)
	if got := sortedActiveNames(session); !slices.Equal(got, []string{"bash", "edit", "read", "write"}) {
		t.Fatalf("default active tools = %q", got)
	}
}
