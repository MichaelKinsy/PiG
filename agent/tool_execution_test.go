package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// findToolResult returns the single tool result for toolCallID from a finished
// Send message history, or fails if it is missing. The tool_use/tool_result
// linkage this asserts is the invariant that keeps the next turn's history
// un-poisoned (see D48 / NormalizeMessages): a result that loses its
// ToolCallID orphans on the next turn and the provider rejects the request.
func findToolResult(t *testing.T, msgs []AgentMessage, toolCallID string) ToolResultMessage {
	t.Helper()
	var found []ToolResultMessage
	for _, m := range msgs {
		if m.ToolResult != nil && m.ToolResult.ToolCallID == toolCallID {
			found = append(found, *m.ToolResult)
		}
	}
	if len(found) != 1 {
		t.Fatalf("tool result for %q appeared %d times, want exactly 1: %+v", toolCallID, len(found), msgs)
	}
	return found[0]
}

func TestPendingToolCallsArgumentsRemainMutableAfterCollection(t *testing.T) {
	calls := pendingToolCalls(&AssistantMessage{Content: []ai.AssistantContentBlock{
		ai.ToolCall{ID: "call-1", Name: "echo", Arguments: ai.JsonObject{"value": "x"}},
	}})
	if len(calls) != 1 {
		t.Fatalf("pendingToolCalls returned %d calls, want 1", len(calls))
	}
	if _, err := calls[0].args.WriteString(" "); err != nil {
		t.Fatalf("append to collected arguments: %v", err)
	}
	if got := calls[0].args.String(); got != `{"value":"x"} ` {
		t.Fatalf("arguments = %q, want serialized arguments plus appended byte", got)
	}
}

// TestSend_ToolExecuteError_BecomesLinkedErrorResult drives the production agent
// loop end to end: the model requests a tool whose Execute returns a Go error.
// Upstream turns a thrown tool into an error tool-result the model can recover
// from (agent-loop finalizeExecutedToolCall); it must not crash the run or drop
// the turn. pig must produce an IsError result whose ToolCallID still matches the
// call, so the tool_use/tool_result pair stays intact in history.
func TestSend_ToolExecuteError_BecomesLinkedErrorResult(t *testing.T) {
	tool := &fakeTool{name: "boom", mode: ToolModeSequential, execErr: errors.New("disk on fire")}
	prov := providerFromSeqs(
		toolCallSeq(struct{ id, name string }{"tc-err", "boom"}),
		textSeq("recovered"),
	)
	a := NewAgent(AgentOptions{Model: fakeTestModel(prov), Tools: []AgentTool{tool}, MaxTurns: 5})

	msgs, err := a.Send(context.Background(), "run boom")
	if err != nil {
		t.Fatalf("Send returned an error instead of an error tool-result: %v", err)
	}
	res := findToolResult(t, msgs, "tc-err")
	if !res.IsError {
		t.Fatalf("tool result IsError = false, want true for a thrown tool: %+v", res)
	}
	if !strings.Contains(res.Text(), "disk on fire") {
		t.Fatalf("tool result content = %q, want the tool's error message", res.Text())
	}
	if res.ToolName != "boom" {
		t.Fatalf("tool result ToolName = %q, want boom", res.ToolName)
	}
}

// TestSend_InvalidToolArgs_BecomesLinkedErrorResult drives the loop when the
// model calls a tool with arguments that fail the tool's JSON schema (here a
// required field is missing). pig validates before Execute and must return an
// IsError result with the calling ToolCallID rather than run the tool on bad
// input or drop the call.
func TestSend_InvalidToolArgs_BecomesLinkedErrorResult(t *testing.T) {
	tool := &fakeTool{
		name:    "needs-path",
		mode:    ToolModeSequential,
		content: "should not run",
		params: map[string]any{
			"type":     "object",
			"required": []any{"path"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
	}
	prov := providerFromSeqs(
		toolCallSeq(struct{ id, name string }{"tc-bad", "needs-path"}),
		textSeq("done"),
	)
	a := NewAgent(AgentOptions{Model: fakeTestModel(prov), Tools: []AgentTool{tool}, MaxTurns: 5})

	msgs, err := a.Send(context.Background(), "call with bad args")
	if err != nil {
		t.Fatalf("Send returned an error instead of a validation tool-result: %v", err)
	}
	res := findToolResult(t, msgs, "tc-bad")
	if !res.IsError {
		t.Fatalf("invalid-args result IsError = false, want true: %+v", res)
	}
	if res.Text() == "should not run" {
		t.Fatal("tool executed on invalid args; validation must run before Execute")
	}
	if res.ToolName != "needs-path" {
		t.Fatalf("invalid-args result ToolName = %q, want needs-path", res.ToolName)
	}
}

// TestSend_UnknownTool_BecomesLinkedErrorResult drives the loop when the model
// calls a tool that is not registered. pig must synthesize an IsError result
// ("not found") with the calling ToolCallID rather than crash or silently drop
// the call, so the turn's tool pairing stays valid.
func TestSend_UnknownTool_BecomesLinkedErrorResult(t *testing.T) {
	prov := providerFromSeqs(
		toolCallSeq(struct{ id, name string }{"tc-ghost", "ghost"}),
		textSeq("done"),
	)
	a := NewAgent(AgentOptions{Model: fakeTestModel(prov), Tools: nil, MaxTurns: 5})

	msgs, err := a.Send(context.Background(), "call ghost")
	if err != nil {
		t.Fatalf("Send returned an error instead of a not-found tool-result: %v", err)
	}
	res := findToolResult(t, msgs, "tc-ghost")
	if !res.IsError {
		t.Fatalf("unknown-tool result IsError = false, want true: %+v", res)
	}
	if !strings.Contains(res.Text(), "not found") {
		t.Fatalf("unknown-tool result content = %q, want a not-found message", res.Text())
	}
	if res.ToolName != "ghost" {
		t.Fatalf("unknown-tool result ToolName = %q, want ghost", res.ToolName)
	}
}
