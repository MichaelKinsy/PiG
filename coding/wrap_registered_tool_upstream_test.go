package coding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports wrapRegisteredTool (packages/coding-agent/src/core/extensions/wrapper.ts:17-21), which is
// `wrapToolDefinition(definition, (toolCallId, signal) => runner.createToolContext(toolCallId, signal))` (tool-definition-wrapper.ts:8-30):
// the AgentTool carries the definition's identity and every call runs with the runner's tool context for that call's id.
func TestWrapRegisteredToolUpstream(t *testing.T) {
	t.Run("the tool carries the definition's name, label, description and mode", func(t *testing.T) {
		registered := fixtureTool("probe", "Probe description", `{"type":"object"}`, nil)
		registered.Definition.Label = "Probe label"
		registered.Definition.ExecutionMode = extension.ToolExecutionMode(agent.ToolModeSequential)
		tool, err := WrapRegisteredTool(registered, inproc.NewRunner(nil, t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		if tool.Name() != "probe" || tool.Label() != "Probe label" || tool.Schema().Description != "Probe description" || tool.ExecutionMode() != agent.ToolModeSequential {
			t.Errorf("tool = %q %q %q %v", tool.Name(), tool.Label(), tool.Schema().Description, tool.ExecutionMode())
		}
	})

	t.Run("each call runs with the runner's tool context for its own call id", func(t *testing.T) {
		cwd := t.TempDir()
		var gotCwd []string
		var nested []string
		registered := fixtureTool("probe", "d", `{"type":"object"}`, func(ctx context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			tc := extension.ToolContextFromContext(ctx)
			if tc == nil {
				t.Fatal("the call ran without a tool context")
			}
			dir, err := tc.CWD()
			if err != nil {
				t.Fatal(err)
			}
			gotCwd = append(gotCwd, dir)
			// The runner has no executeTool action: the nested outcome is an error whose id is `<caller>/0` (runner.ts), so it names the caller's id.
			outcome, err := tc.ExecuteTool("other", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			nested = append(nested, outcome.ToolCall.ID)
			return agent.AgentToolResult{}, nil
		})
		tool, err := WrapRegisteredTool(registered, inproc.NewRunner(nil, cwd))
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"call-1", "call-2"} {
			if _, err := tool.Execute(t.Context(), id, json.RawMessage(`{}`), nil); err != nil {
				t.Fatal(err)
			}
		}
		if len(nested) != 2 || nested[0] != "call-1/0" || nested[1] != "call-2/0" {
			t.Errorf("nested call ids = %v, want [call-1/0 call-2/0]", nested)
		}
		if len(gotCwd) != 2 || gotCwd[0] != cwd || gotCwd[1] != cwd {
			t.Errorf("tool context cwd = %v, want the runner's %s", gotCwd, cwd)
		}
	})

	t.Run("a schema that does not parse is an error", func(t *testing.T) {
		if _, err := WrapRegisteredTool(fixtureTool("bad", "d", `{`, nil), inproc.NewRunner(nil, t.TempDir())); err == nil {
			t.Fatal("wrapped a tool whose schema does not parse")
		}
	})
}
