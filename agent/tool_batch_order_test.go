package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestParallelToolBatchEventOrder pins the event order of upstream
// executeToolCallsParallel (packages/agent/src/agent-loop.ts): for every call
// in source order, tool_execution_start is emitted and the call is prepared
// (beforeToolCall) before the next call starts, and no call executes until
// every call is prepared. Executions then overlap, tool_execution_end follows
// completion order, and tool-result messages follow source order.
func TestParallelToolBatchEventOrder(t *testing.T) {
	ids := []string{"call-1", "call-2", "call-3"}
	releaseOrder := []string{"call-3", "call-1", "call-2"}
	var log orderLog
	var rec *eventRecorder

	var mu sync.Mutex
	executing := 0
	allExecuting := make(chan struct{})
	release := map[string]chan struct{}{}
	for _, id := range ids {
		release[id] = make(chan struct{})
	}
	tool := &scriptTool{name: "p", mode: ToolModeParallel, params: map[string]any{"type": "object"},
		execute: func(_ context.Context, id string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
			log.add("exec:" + id)
			onUpdate("running", nil)
			mu.Lock()
			executing++
			if executing == len(ids) {
				close(allExecuting)
			}
			mu.Unlock()
			awaitSignal(t, release[id], "release of "+id)
			return AgentToolResult{Content: id}, nil
		}}
	rec = newEventRecorder(nil)
	calls := make([]ai.ToolCall, len(ids))
	for i, id := range ids {
		calls[i] = toolCall(id, "p", nil)
	}
	a := NewAgent(AgentOptions{
		Model:   scriptedModel(&scriptedProvider{respond: toolCallsThenText(calls...)}),
		Tools:   []AgentTool{tool},
		EventCh: rec.ch,
		BeforeToolCall: []BeforeToolCallHook{func(_ context.Context, id, _ string, _ json.RawMessage) ToolCallHookResult {
			// The call's tool_execution_start precedes its preparation.
			rec.waitFor(t, func(ev AgentEvent) bool {
				start, ok := ev.(ToolExecutionStartEvent)
				return ok && start.ToolCallID == id
			})
			log.add("prepare:" + id)
			return ToolCallHookResult{}
		}},
	})

	done := sendAsync(t, a, "run")
	// Every call executes at once before any is released.
	waitSignal(t, allExecuting, "all three executions")
	for _, id := range releaseOrder {
		close(release[id])
		rec.waitFor(t, func(ev AgentEvent) bool {
			end, ok := ev.(ToolExecutionEndEvent)
			return ok && end.ToolCallID == id
		})
	}
	waitSignal(t, done, "the run")
	events := rec.stop()

	entries := log.list()
	if want := []string{"prepare:call-1", "prepare:call-2", "prepare:call-3"}; !reflect.DeepEqual(entries[:3], want) {
		t.Fatalf("hook/execution log = %v, want every preparation in source order before any execution", entries)
	}
	var starts, ends, results []string
	for _, ev := range events {
		switch ev := ev.(type) {
		case ToolExecutionStartEvent:
			starts = append(starts, ev.ToolCallID)
		case ToolExecutionUpdateEvent:
			if len(starts) != len(ids) {
				t.Fatalf("tool_execution_update for %s before every tool_execution_start (starts %v)", ev.ToolCallID, starts)
			}
		case ToolExecutionEndEvent:
			ends = append(ends, ev.ToolCallID)
		case MessageEndEvent:
			if ev.Message.ToolResult != nil {
				results = append(results, ev.Message.ToolResult.ToolCallID)
			}
		}
	}
	if !reflect.DeepEqual(starts, ids) {
		t.Fatalf("tool_execution_start order = %v, want source order", starts)
	}
	if !reflect.DeepEqual(ends, releaseOrder) {
		t.Fatalf("tool_execution_end order = %v, want completion order %v", ends, releaseOrder)
	}
	if !reflect.DeepEqual(results, ids) {
		t.Fatalf("tool result order = %v, want source order", results)
	}
}
