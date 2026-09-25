package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream agent-loop.ts has no turn cap: after tool results it always makes
// the next request until the model stops calling tools. Pig defaulted
// MaxTurns to 100, and at that count the loop broke out, emitted agent_end and
// returned nil with the last message a tool result: the run ended silently
// mid-task, with no response and no error. Worker sessions show it twice at
// exactly 100 turns (tui-md 23:57Z, next-share-gateway 01:32Z), each needing a
// manual "continue".
func TestSend_DefaultHasNoTurnCap(t *testing.T) {
	const toolTurns = 105
	prov := providerFromSeqs()
	for range toolTurns {
		prov.seqs = append(prov.seqs, toolCallSeq(struct{ id, name string }{"c", "t"}))
	}
	prov.seqs = append(prov.seqs, textSeq("done"))
	a := NewAgent(AgentOptions{Model: fakeTestModel(prov), Tools: []AgentTool{&fakeTool{name: "t", mode: ToolModeSequential, content: "ok"}}})

	msgs, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	last := msgs[len(msgs)-1]
	if last.Assistant == nil || panicTestAssistantText(last.Assistant) != "done" {
		t.Fatalf("run ended on %+v after %d messages; want the model's final response after %d tool turns", last, len(msgs), toolTurns)
	}
}

// An explicit cap still stops the loop, but visibly: the run returns
// ErrMaxTurnsReached instead of ending as if the task were complete.
func TestSend_ExplicitTurnCapIsAnError(t *testing.T) {
	prov := providerFromSeqs(
		toolCallSeq(struct{ id, name string }{"c1", "t"}),
		toolCallSeq(struct{ id, name string }{"c2", "t"}),
		textSeq("done"),
	)
	a := NewAgent(AgentOptions{Model: fakeTestModel(prov), Tools: []AgentTool{&fakeTool{name: "t", mode: ToolModeSequential, content: "ok"}}, MaxTurns: 1})
	msgs, err := a.Send(context.Background(), "go")
	if !errors.Is(err, ErrMaxTurnsReached) {
		t.Fatalf("Send err = %v, want ErrMaxTurnsReached", err)
	}
	if last := msgs[len(msgs)-1]; last.ToolResult == nil {
		t.Fatalf("last message = %+v, want the unanswered tool result", last)
	}
}

// Terminate is upstream's only legitimate way to end a run on tool results
// (shouldTerminateToolBatch); it stays a clean end.
func TestSend_TerminatedBatchEndsCleanlyOnToolResults(t *testing.T) {
	prov := providerFromSeqs(toolCallSeq(struct{ id, name string }{"c1", "stop"}), textSeq("unused"))
	a := NewAgent(AgentOptions{Model: fakeTestModel(prov), Tools: []AgentTool{&terminateTool{fakeTool{mode: ToolModeSequential}}}})
	msgs, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if last := msgs[len(msgs)-1]; last.ToolResult == nil {
		t.Fatalf("last message = %+v, want the terminating tool result", last)
	}
}

type terminateTool struct{ fakeTool }

func (*terminateTool) Name() string          { return "stop" }
func (*terminateTool) Schema() ai.ToolSchema { return ai.ToolSchema{Name: "stop"} }
func (*terminateTool) Execute(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
	return AgentToolResult{Content: "stopped", Terminate: true}, nil
}

// checkRunEnd turns any clean loop exit that leaves a tool result unanswered
// into an error, whatever path produced it, while terminate, a FinishTurn end,
// cancellation, and a normal final response stay clean.
func TestCheckRunEndReportsUnansweredToolResults(t *testing.T) {
	toolResult := AgentMessage{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "c"}}
	assistant := AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: ai.StopReasonStop}}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name     string
		last     AgentMessage
		ctx      context.Context
		cleanEnd bool
		in       error
		want     error
	}{
		{"unanswered tool result", toolResult, context.Background(), false, nil, ErrToolResultsUnanswered},
		{"terminated batch", toolResult, context.Background(), true, nil, nil},
		{"cancelled", toolResult, cancelled, false, nil, nil},
		{"final response", assistant, context.Background(), false, nil, nil},
		{"existing error kept", toolResult, context.Background(), false, ErrMaxTurnsReached, ErrMaxTurnsReached},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAgent(AgentOptions{})
			a.messages = []AgentMessage{tc.last}
			r := &loopRun{a: a, ctx: tc.ctx, toolResultsEndRun: tc.cleanEnd}
			if got := r.checkRunEnd(tc.in); !errors.Is(got, tc.want) || (tc.want == nil && got != nil) {
				t.Fatalf("checkRunEnd = %v, want %v", got, tc.want)
			}
		})
	}
}
