package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// panicTool panics from Execute with value.
type panicTool struct {
	name  string
	mode  ToolExecutionMode
	value any
}

func (t *panicTool) Name() string                     { return t.name }
func (t *panicTool) Label() string                    { return "" }
func (t *panicTool) Description() string              { return t.name }
func (t *panicTool) Schema() ai.ToolSchema            { return ai.ToolSchema{Name: t.name} }
func (t *panicTool) ExecutionMode() ToolExecutionMode { return t.mode }
func (t *panicTool) Execute(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
	panic(t.value)
}

// Upstream executePreparedToolCall catches whatever tool.execute throws and
// returns createErrorToolResult(message), so every tool call gets a result and
// the run continues. A Go tool that panics must do the same instead of ending
// the process between the tool call and its result.
func TestSend_ToolPanic_BecomesLinkedErrorResult(t *testing.T) {
	for _, mode := range []ToolExecutionMode{ToolModeSequential, ToolModeParallel} {
		t.Run(string(mode), func(t *testing.T) {
			prov := providerFromSeqs(
				toolCallSeq(struct{ id, name string }{"tc-panic", "boom"}, struct{ id, name string }{"tc-ok", "fine"}),
				textSeq("recovered"),
			)
			a := NewAgent(AgentOptions{
				Model:    fakeTestModel(prov),
				Tools:    []AgentTool{&panicTool{name: "boom", mode: mode, value: "index out of range"}, &fakeTool{name: "fine", mode: mode, content: "ok"}},
				MaxTurns: 5,
			})

			msgs, err := a.Send(context.Background(), "run boom")
			if err != nil {
				t.Fatalf("Send returned an error instead of an error tool result: %v", err)
			}
			res := findToolResult(t, msgs, "tc-panic")
			if !res.IsError || res.Text() != "index out of range" {
				t.Fatalf("panicking tool result = %+v, want IsError with the panic message", res)
			}
			if ok := findToolResult(t, msgs, "tc-ok"); ok.IsError || ok.Text() != "ok" {
				t.Fatalf("sibling tool result = %+v, want its own success", ok)
			}
			if last := msgs[len(msgs)-1]; last.Assistant == nil || !strings.Contains(panicTestAssistantText(last.Assistant), "recovered") {
				t.Fatalf("run did not continue to the next assistant turn: %+v", last)
			}
		})
	}
}

// Upstream prepareToolCall wraps beforeToolCall in the same try/catch.
func TestSend_BeforeToolCallHookPanic_BecomesErrorResult(t *testing.T) {
	prov := providerFromSeqs(toolCallSeq(struct{ id, name string }{"tc-1", "mytool"}), textSeq("done"))
	tool := &fakeTool{name: "mytool", mode: ToolModeSequential, content: "should not run"}
	a := NewAgent(AgentOptions{
		Model: fakeTestModel(prov),
		Tools: []AgentTool{tool},
		BeforeToolCall: []BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) ToolCallHookResult {
			panic(errBeforeHook)
		}},
		MaxTurns: 5,
	})
	msgs, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res := findToolResult(t, msgs, "tc-1"); !res.IsError || res.Text() != errBeforeHook.Error() {
		t.Fatalf("result = %+v, want the hook panic as an error result", res)
	}
}

// Upstream finalizeExecutedToolCall catches afterToolCall and replaces the
// result with an error result.
func TestSend_AfterToolCallHookPanic_BecomesErrorResult(t *testing.T) {
	prov := providerFromSeqs(toolCallSeq(struct{ id, name string }{"tc-1", "mytool"}), textSeq("done"))
	tool := &fakeTool{name: "mytool", mode: ToolModeSequential, content: "original"}
	a := NewAgent(AgentOptions{
		Model: fakeTestModel(prov),
		Tools: []AgentTool{tool},
		AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			panic("after hook failed")
		}},
		MaxTurns: 5,
	})
	msgs, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res := findToolResult(t, msgs, "tc-1"); !res.IsError || res.Text() != "after hook failed" {
		t.Fatalf("result = %+v, want the hook panic as an error result", res)
	}
}

var errBeforeHook = &hookError{"before hook failed"}

type hookError struct{ message string }

func (e *hookError) Error() string { return e.message }

func panicTestAssistantText(message *AssistantMessage) string {
	var text strings.Builder
	for _, block := range message.Content {
		if block, ok := block.(ai.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}
