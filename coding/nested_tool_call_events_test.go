package coding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 0.99.1 nested-tool-calls.ts:240-247 emits tool_execution_end with `result: outcome.result, isError: outcome.isError`. A call that never
// reaches a tool (arguments that are not an object) gets createErrorToolResult, `{content, details: {}}`, so the failure is only the event's
// isError; a tool that returns isError keeps it inside the result.
func TestNestedToolCallEndEventsKeepTheResultFlagApartFromTheCallFlag(t *testing.T) {
	failing := &nestedTestTool{name: "failing", run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "bad"}}, Details: map[string]any{"partial": true}, IsError: true}
	}}
	tools := []agent.AgentTool{failing}
	for _, tc := range []struct {
		name      string
		tool      string
		args      string
		resultErr bool
	}{
		{"returned failure", "failing", `{}`, true},
		{"arguments that are not an object", "failing", `[1]`, false},
		{"unknown tool", "missing", `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, host := newNestedTestRunner(&tools, false)
			outcome, err := runner.Execute(t.Context(), "call", tc.tool, json.RawMessage(tc.args), NestedToolCallOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !outcome.IsError || outcome.Result.IsError != tc.resultErr {
				t.Fatalf("outcome = %+v, want isError true and result.isError %v", outcome, tc.resultErr)
			}
			var end *agent.ToolExecutionEndEvent
			for _, event := range host.events {
				if e, ok := event.(agent.ToolExecutionEndEvent); ok {
					end = &e
				}
			}
			if end == nil || !end.IsError || end.Result.IsError != tc.resultErr {
				t.Fatalf("tool_execution_end = %+v, want isError true and result.isError %v", end, tc.resultErr)
			}
		})
	}
}
