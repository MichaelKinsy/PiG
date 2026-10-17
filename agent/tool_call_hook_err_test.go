package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A beforeToolCall hook that throws ends the call with an error result carrying the thrown message, as agent-loop.ts prepareToolCall's catch does: an empty message stays empty (no "Tool execution was blocked" default) and the batch does not terminate.
func TestBeforeToolCallHookErrorBecomesTheToolResult(t *testing.T) {
	executed := false
	tool := &scriptTool{name: "echo", params: valueSchema, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		executed = true
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "should not execute"}}}, nil
	}}
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{tool},
		BeforeToolCallHooks: []BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) ToolCallHookResult {
			return ToolCallHookResult{Err: errors.New(""), Block: true, Reason: "ignored", Terminate: true}
		}},
	})

	msgs := mustSend(t, a, "echo something")

	result := findToolResult(t, msgs, "tool-1")
	if executed || !result.IsError || result.Text() != "" {
		t.Fatalf("executed %v, result %+v; want the hook error's empty message", executed, result)
	}
	if provider.calls() != 2 {
		t.Fatalf("provider called %d times; a thrown hook does not terminate the run", provider.calls())
	}
}
