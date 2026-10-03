package coding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// A nested call comes from a script's JSON; `callTool(name, args)` hands the runner JSON.stringify of a JavaScript object, in
// the object's member order. Pi passes that object on unchanged: the start event, the tool and the session's nestedCalls record
// all see the script's order.
// upstream: .upstream/v0.99.1/packages/coding-agent/src/core/nested-tool-calls.ts:56-77 (record.arguments = JSON.parse(encoded)),
// :171-248 (execute passes args to runToolCall), ai/src/utils/validation.ts:317-339.
// orderRecordingHost keeps the text of the arguments of the call it was asked to run.
type orderRecordingHost struct {
	nestedTestHost
	executed string
}

func (h *orderRecordingHost) RunToolCall(ctx context.Context, toolCall agent.AgentToolCall, id string, onUpdate agent.ToolUpdateSink) (agent.AgentToolCallOutcome, error) {
	raw, _ := toolCall.ArgumentsJSON()
	h.executed = string(raw)
	return agent.AgentToolCallOutcome{ToolCall: toolCall, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{}}}, nil
}

func TestNestedToolCallKeepsTheScriptsArgumentOrder(t *testing.T) {
	const arguments = `{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`
	tools := []agent.AgentTool{}
	host := &orderRecordingHost{nestedTestHost: nestedTestHost{tools: &tools}}
	runner := NewNestedToolCallRunner(host)
	if _, err := runner.Execute(t.Context(), "call", "probe", json.RawMessage(arguments), NestedToolCallOptions{}); err != nil {
		t.Fatal(err)
	}
	if host.executed != arguments {
		t.Errorf("the host ran the call with %s, want %s", host.executed, arguments)
	}
	var started *agent.ToolExecutionStartEvent
	for _, event := range host.events {
		if e, ok := event.(agent.ToolExecutionStartEvent); ok {
			started = &e
		}
	}
	if started == nil || string(started.Args) != arguments {
		t.Errorf("tool_execution_start args = %s, want %s", started.Args, arguments)
	}
	summary := runner.TakeRecord("call")
	if summary == nil || len(summary.Calls.Calls) != 1 {
		t.Fatalf("record = %+v", summary)
	}
	encoded, err := json.Marshal(summary.Calls.Calls[0])
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire.Arguments) != arguments {
		t.Errorf("the recorded arguments are %s, want %s", wire.Arguments, arguments)
	}
}
