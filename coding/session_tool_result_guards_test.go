package coding

// pi: packages/coding-agent/src/core/extensions/types.ts

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// types.ts:1315-1338: an extension's tool_result handler tells the built-in tool that ran from `e.toolName`. The Session emits CustomToolResultEvent for every
// tool, so the Is<Tool>ToolResult guards must hold for the event a real run produces, whatever the tool.
//
// mutation-checked: the mutant "the Session emits the tool_result event without the tool's name" fails it.
func TestToolResultGuardsHoldForTheEventARealRunEmits(t *testing.T) {
	guards := map[string]func(extension.ToolResultEvent) bool{
		"bash": extension.IsBashToolResult, "powershell": extension.IsPowerShellToolResult, "read": extension.IsReadToolResult, "edit": extension.IsEditToolResult,
		"write": extension.IsWriteToolResult, "grep": extension.IsGrepToolResult, "find": extension.IsFindToolResult, "ls": extension.IsLsToolResult,
	}
	for name := range guards {
		t.Run(name, func(t *testing.T) {
			var matched []string
			h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{&fakeTool{name: name}}, extension: extension.Extension{Path: "guards", Handlers: map[string][]extension.HandlerFn{"tool_result": {func(args ...any) (any, error) {
				event := args[0].(extension.ToolResultEvent)
				for other, guard := range guards {
					if guard(event) {
						matched = append(matched, other)
					}
				}
				return nil, nil
			}}}}}, fauxToolCall(name), fauxReply("done", ai.StopReasonStop, 0))
			if _, err := h.session.Send(t.Context(), "run it"); err != nil {
				t.Fatal(err)
			}
			if err := h.session.Close(); err != nil {
				t.Fatal(err)
			}
			<-h.done
			if !slices.Equal(matched, []string{name}) {
				t.Fatalf("guards that held for the %s result = %v, want only %q", name, matched, name)
			}
		})
	}
}
