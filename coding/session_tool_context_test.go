package coding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream wrapper.ts:14-19 and tool-definition-wrapper.ts:7-30: a session wraps every registered tool so each call gets a tool context created for that call's id (runner.ts:948-985).
func TestSessionToolCallsGetAToolContextForTheirCallID(t *testing.T) {
	type observed struct {
		id      string
		context *extension.ToolContext
		outcome extension.AgentToolCallOutcome
	}
	seen := make(chan observed, 1)
	tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "caller", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`), Execute: func(ctx context.Context, id string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		toolContext := extension.ToolContextFromContext(ctx)
		var outcome extension.AgentToolCallOutcome
		if toolContext != nil {
			outcome, _ = toolContext.ExecuteTool("other", nil, nil)
		}
		seen <- observed{id, toolContext, outcome}
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}}, admissionResponse("caller", "value", "x"), fauxReply("done", ai.StopReasonStop, 0))
	if _, err := h.session.Send(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	got := <-seen
	if got.context == nil {
		t.Fatal("the tool call got no tool context")
	}
	// A Session binds executeTool (agent-session.ts:3382), so the nested call runs through NestedToolCallRunner: ids start at `<caller>/1` (nested-tool-calls.ts:180) and an unknown tool fails in the tool pipeline. `<caller>/0` is only the unbound runner's outcome (runner.ts:965-976).
	result, _ := got.outcome.Result.(agent.AgentToolResult)
	if got.outcome.ToolCall.ID != got.id+"/1" || !got.outcome.IsError || len(result.Content) != 1 || result.Content[0] != (ai.TextContent{Text: "Tool other not found"}) {
		t.Fatalf("nested outcome = %+v for call %q", got.outcome, got.id)
	}
}
