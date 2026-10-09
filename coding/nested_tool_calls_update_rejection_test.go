package coding

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// runToolCallHost runs nested calls through the real agent.RunToolCall, as the session's host does, and counts the afterToolCall hooks.
type runToolCallHost struct {
	*nestedTestHost
	afterHooks atomic.Int32
}

func (h *runToolCallHost) RunToolCall(ctx context.Context, toolCall agent.AgentToolCall, _ string, onUpdate agent.ToolUpdateSink) (agent.AgentToolCallOutcome, error) {
	return agent.RunToolCall(ctx, toolCall, agent.RunToolCallOptions{
		Tools: *h.tools,
		ToolCallHooks: agent.ToolCallHooks{AfterToolCallHooks: []agent.AfterToolCallHook{func(context.Context, string, string, json.RawMessage, agent.AgentToolResult) agent.AfterToolCallResult {
			h.afterHooks.Add(1)
			return agent.AfterToolCallResult{}
		}}},
		OnUpdate: onUpdate,
	})
}

// When the caller's onUpdate throws, upstream's callback is an async function that has not reached `await this.host.emit(tool_execution_update)`, so that update raises no event; the rejected promise makes runToolCall reject after the tool returned (no afterToolCall), and execute() rejects before it finishes the record or emits tool_execution_end. Later updates still reach onUpdate and emit their events.
//
// upstream: .upstream/v0.99.1/packages/coding-agent/src/core/nested-tool-calls.ts:219-231 (the onUpdate callback), 232-248 (finally, finish, end event); .upstream/v0.99.1/packages/agent/src/agent-loop.ts:820-849
func TestNestedToolCallRunnerUpdateCallbackFailureRejectsTheCallBeforeItsEndAndAfterHook(t *testing.T) {
	var finished atomic.Bool
	progress := &nestedTestTool{name: "progress", run: func(_ context.Context, _ string, onUpdate agent.ToolUpdateCallback) agent.AgentToolResult {
		onUpdate(textResult("one"))
		onUpdate(textResult("two"))
		finished.Store(true)
		return textResult("done")
	}}
	tools := []agent.AgentTool{progress}
	host := &runToolCallHost{nestedTestHost: &nestedTestHost{tools: &tools}}
	runner := NewNestedToolCallRunner(host)
	boom := errors.New("callback threw")
	var delivered []string
	outcome, err := runner.Execute(t.Context(), "call", "progress", json.RawMessage(`{}`), NestedToolCallOptions{OnUpdate: func(partial agent.AgentToolResult) error {
		content := partial.Text()
		delivered = append(delivered, content)
		if content == "one" {
			return boom
		}
		return nil
	}})
	if !errors.Is(err, boom) {
		t.Fatalf("Execute error = %v, want the callback's error", err)
	}
	if outcome.ToolCall.ID != "" {
		t.Fatalf("a rejected call carries no outcome: %+v", outcome)
	}
	if !finished.Load() {
		t.Fatal("the tool did not run to its end")
	}
	if !reflect.DeepEqual(delivered, []string{"one", "two"}) {
		t.Fatalf("callback saw %v, want both updates", delivered)
	}
	if n := host.afterHooks.Load(); n != 0 {
		t.Fatalf("afterToolCall ran %d times for a rejected call", n)
	}
	var got [][3]string
	for _, event := range host.events {
		got = append(got, nestedEventKey(t, event))
	}
	want := [][3]string{
		{"tool_execution_start", "call/1", "call"},
		{"tool_execution_update", "call/1", "call"}, // only the second update: the first one's callback threw before its event
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v (no tool_execution_end)", got, want)
	}
	if update, ok := host.events[1].(agent.ToolExecutionUpdateEvent); !ok || update.PartialResult.Text() != "two" {
		t.Fatalf("the update event is %+v, want the second update", host.events[1])
	}
	summary := runner.TakeRecord("call")
	if summary == nil || summary.Calls == nil || len(summary.Calls.Calls) != 1 || summary.Calls.Calls[0].Status != ai.NestedToolCallUnfinished || summary.Calls.Complete {
		t.Fatalf("record = %+v, want one unfinished call", summary)
	}
}
