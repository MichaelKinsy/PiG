package coding

// pi: packages/coding-agent/src/core/nested-tool-calls.ts

// Ports .upstream/v0.99.1/packages/coding-agent/test/nested-tool-calls.test.ts (6 cases) with the same inputs and expectations.

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func nestedUsage(input int, cost float64) ai.Usage {
	return ai.Usage{Input: input, TotalTokens: input, Cost: ai.UsageCost{Input: cost, Total: cost}}
}

// nestedTestTool is a tool with a function body. Mirrors the AgentTool literals of the upstream test.
type nestedTestTool struct {
	name string
	mode agent.ToolExecutionMode
	run  func(ctx context.Context, id string, onUpdate agent.ToolUpdateCallback) agent.AgentToolResult
}

func (t *nestedTestTool) Name() string          { return t.name }
func (t *nestedTestTool) Label() string         { return t.name }
func (t *nestedTestTool) Schema() ai.ToolSchema { return ai.ToolSchema{Name: t.name} }
func (t *nestedTestTool) ExecutionMode() agent.ToolExecutionMode {
	return t.mode
}
func (t *nestedTestTool) Execute(ctx context.Context, id string, _ json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return t.run(ctx, id, onUpdate), nil
}

type nestedTestHost struct {
	tools      *[]agent.AgentTool
	sequential bool
	mu         sync.Mutex
	events     []agent.AgentEvent
	// durationMs is what a call that finds its tool reports, as the agent loop's outcome does for a call that ran.
	durationMs *int64
}

func (h *nestedTestHost) GetTools() []agent.AgentTool { return *h.tools }
func (h *nestedTestHost) IsSequential() bool          { return h.sequential }
func (h *nestedTestHost) RunToolCall(ctx context.Context, toolCall agent.AgentToolCall, _ string, onUpdate agent.ToolUpdateSink) (agent.AgentToolCallOutcome, error) {
	for _, tool := range *h.tools {
		if tool.Name() != toolCall.Name {
			continue
		}
		raw, _ := json.Marshal(toolCall.Arguments)
		result, _ := tool.Execute(ctx, toolCall.ID, raw, func(partial agent.AgentToolResult) { _ = onUpdate(partial) })
		return agent.AgentToolCallOutcome{ToolCall: toolCall, Result: result, IsError: result.IsError, DurationMs: h.durationMs}, nil
	}
	return agent.AgentToolCallOutcome{ToolCall: toolCall, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Tool " + toolCall.Name + " not found"}}, Details: map[string]any{}}, IsError: true}, nil
}
func (h *nestedTestHost) Emit(event agent.AgentEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
}

func newNestedTestRunner(tools *[]agent.AgentTool, sequential bool) (*NestedToolCallRunner, *nestedTestHost) {
	host := &nestedTestHost{tools: tools, sequential: sequential}
	return NewNestedToolCallRunner(host), host
}

func textResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: map[string]any{}}
}

// nestedEventKey is upstream's `[event.type, event.toolCallId, event.parentToolCallId]`.
func nestedEventKey(t *testing.T, event agent.AgentEvent) [3]string {
	t.Helper()
	switch event := event.(type) {
	case agent.ToolExecutionStartEvent:
		return [3]string{"tool_execution_start", event.ToolCallID, event.ParentToolCallID}
	case agent.ToolExecutionUpdateEvent:
		return [3]string{"tool_execution_update", event.ToolCallID, event.ParentToolCallID}
	case agent.ToolExecutionEndEvent:
		return [3]string{"tool_execution_end", event.ToolCallID, event.ParentToolCallID}
	}
	t.Fatalf("unexpected event %T", event)
	return [3]string{}
}

// durationSet asserts upstream's `durationMs: expect.any(Number)` and clears it for comparison.
func durationSet(t *testing.T, calls *ai.NestedToolCalls) *ai.NestedToolCalls {
	t.Helper()
	for i := range calls.Calls {
		if calls.Calls[i].DurationMs == nil {
			t.Fatalf("call %q has no durationMs", calls.Calls[i].ID)
		}
		calls.Calls[i].DurationMs = nil
	}
	return calls
}

// upstream nested-tool-calls.test.ts:54-98
func TestNestedToolCallRunnerAssignsIDsBelowTheCallerEmitsEventsWithTheParentIDAndRecordsTheCalls(t *testing.T) {
	echo := &nestedTestTool{name: "echo", run: func(_ context.Context, _ string, onUpdate agent.ToolUpdateCallback) agent.AgentToolResult {
		onUpdate(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{}})
		return textResult("ok")
	}}
	tools := []agent.AgentTool{echo}
	runner, host := newNestedTestRunner(&tools, false)
	var updates []string
	options := NestedToolCallOptions{OnUpdate: func(partial agent.AgentToolResult) error { updates = append(updates, partial.Text()); return nil }}

	first, _ := runner.Execute(t.Context(), "call", "echo", json.RawMessage(`{"a":1}`), options)
	missing, _ := runner.Execute(t.Context(), "call", "missing", json.RawMessage(`{}`), NestedToolCallOptions{})

	if first.ToolCall.ID != "call/1" {
		t.Fatalf("first id = %q", first.ToolCall.ID)
	}
	if missing.ToolCall.ID != "call/2" || !missing.IsError {
		t.Fatalf("missing = %+v", missing)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %v", updates)
	}
	var got [][3]string
	for _, event := range host.events {
		got = append(got, nestedEventKey(t, event))
	}
	want := [][3]string{
		{"tool_execution_start", "call/1", "call"},
		{"tool_execution_update", "call/1", "call"},
		{"tool_execution_end", "call/1", "call"},
		{"tool_execution_start", "call/2", "call"},
		{"tool_execution_end", "call/2", "call"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	summary := runner.TakeRecord("call")
	if summary == nil || summary.Calls == nil {
		t.Fatalf("record = %+v", summary)
	}
	wantCalls := &ai.NestedToolCalls{Complete: true, Calls: []ai.NestedToolCallRecord{
		{ID: "call/1", Name: "echo", Arguments: ai.JsonObject{"a": float64(1)}, Status: ai.NestedToolCallOK},
		{ID: "call/2", Name: "missing", Arguments: ai.JsonObject{}, Status: ai.NestedToolCallError, Error: "Tool missing not found"},
	}}
	if calls := durationSet(t, summary.Calls); !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %+v, want %+v", calls, wantCalls)
	}
	// The record is taken once.
	if runner.TakeRecord("call") != nil || runner.TakeRecord("other") != nil {
		t.Fatal("a record was taken twice, or an unknown call had one")
	}
}

// upstream nested-tool-calls.test.ts:100-126
func TestNestedToolCallRunnerRecordsCallsOfNestedToolsOnTheModelIssuedCall(t *testing.T) {
	leaf := &nestedTestTool{name: "leaf", run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}
	}}
	tools := []agent.AgentTool{leaf}
	runner, _ := newNestedTestRunner(&tools, false)
	tools = append(tools, &nestedTestTool{name: "middle", run: func(ctx context.Context, id string, _ agent.ToolUpdateCallback) agent.AgentToolResult {
		runner.Execute(ctx, id, "leaf", json.RawMessage(`{}`), NestedToolCallOptions{})
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}
	}})

	runner.Execute(t.Context(), "call", "middle", json.RawMessage(`{}`), NestedToolCallOptions{})

	var ids []string
	for _, call := range runner.TakeRecord("call").Calls.Calls {
		ids = append(ids, call.ID)
	}
	if want := []string{"call/1", "call/1/1"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

// upstream nested-tool-calls.test.ts:128-162
func TestNestedToolCallRunnerSumsTheUsageOfNestedResultsAtEveryDepth(t *testing.T) {
	leaf := &nestedTestTool{name: "leaf", run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
		usage := nestedUsage(10, 0.01)
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}, Usage: &usage}
	}}
	plain := &nestedTestTool{name: "plain", run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}
	}}
	tools := []agent.AgentTool{leaf, plain}
	runner, _ := newNestedTestRunner(&tools, false)
	tools = append(tools, &nestedTestTool{name: "middle", run: func(ctx context.Context, id string, _ agent.ToolUpdateCallback) agent.AgentToolResult {
		runner.Execute(ctx, id, "leaf", json.RawMessage(`{}`), NestedToolCallOptions{})
		// Its own usage only: the leaf's usage is counted once, by the recorder.
		usage := nestedUsage(5, 0.005)
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}, Usage: &usage}
	}})

	runner.Execute(t.Context(), "call", "middle", json.RawMessage(`{}`), NestedToolCallOptions{})
	runner.Execute(t.Context(), "call", "leaf", json.RawMessage(`{}`), NestedToolCallOptions{})
	runner.Execute(t.Context(), "call", "plain", json.RawMessage(`{}`), NestedToolCallOptions{})
	runner.Execute(t.Context(), "free", "plain", json.RawMessage(`{}`), NestedToolCallOptions{})

	summary := runner.TakeRecord("call")
	if summary == nil || summary.Usage == nil {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Usage.Input != 25 {
		t.Fatalf("input = %d, want 25", summary.Usage.Input)
	}
	if math.Abs(summary.Usage.Cost.Total-0.025) > 1e-10 {
		t.Fatalf("cost = %v, want 0.025", summary.Usage.Cost.Total)
	}
	free := runner.TakeRecord("free")
	if free == nil || free.Calls == nil || !free.Calls.Complete || free.Usage != nil {
		t.Fatalf("free = %+v", free)
	}
}

// upstream nested-tool-calls.test.ts:164-187
func TestNestedToolCallRunnerSerializesConcurrentCallsToSequentialTools(t *testing.T) {
	var active atomic.Int32
	var mu sync.Mutex
	maxActive := map[string]int32{"sequential": 0, "parallel": 0}
	makeTool := func(name string, mode agent.ToolExecutionMode) agent.AgentTool {
		return &nestedTestTool{name: name, mode: mode, run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
			now := active.Add(1)
			mu.Lock()
			maxActive[name] = max(maxActive[name], now)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
			return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}
		}}
	}
	tools := []agent.AgentTool{makeTool("sequential", agent.ToolModeSequential), makeTool("parallel", "")}
	runner, _ := newNestedTestRunner(&tools, false)
	runAll := func(name string) {
		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() { runner.Execute(t.Context(), "call", name, json.RawMessage(`{}`), NestedToolCallOptions{}) })
		}
		wg.Wait()
	}

	runAll("sequential")
	runAll("parallel")

	if maxActive["sequential"] != 1 || maxActive["parallel"] != 3 {
		t.Fatalf("maxActive = %v, want sequential 1 and parallel 3", maxActive)
	}
}

func recorderCall(id string, args ai.JsonObject) agent.AgentToolCall {
	return agent.AgentToolCall{ID: id, Name: "t", Arguments: args}
}

// upstream nested-tool-calls.test.ts:198-220
func TestNestedCallRecorderOmitsOversizedArgumentsAndDropsCallsBeyondTheLimit(t *testing.T) {
	recorder := NewNestedCallRecorder()
	if recorder.Snapshot() != nil {
		t.Fatal("an empty recorder has a snapshot")
	}
	small := recorder.Start(recorderCall("a", ai.JsonObject{"x": float64(1)}))
	recorder.Finish(small, false, "")
	want := &ai.NestedToolCalls{Complete: true, Calls: []ai.NestedToolCallRecord{{ID: "a", Name: "t", Arguments: ai.JsonObject{"x": float64(1)}, Status: ai.NestedToolCallOK}}}
	if got := durationSet(t, recorder.Snapshot()); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %+v, want %+v", got, want)
	}

	big := recorder.Start(recorderCall("b", ai.JsonObject{"text": strings.Repeat("x", NestedCallLimits.MaxArgumentBytesPerCall)}))
	recorder.Finish(big, true, strings.Repeat("e", 1000))
	snapshot := recorder.Snapshot()
	if snapshot.Complete {
		t.Fatal("snapshot with omitted arguments is complete")
	}
	second := snapshot.Calls[1]
	if second.ID != "b" || second.Status != ai.NestedToolCallError {
		t.Fatalf("second = %+v", second)
	}
	if second.Arguments != nil {
		t.Fatalf("arguments = %v, want omitted", second.Arguments)
	}
	if second.ArgumentsBytes == nil || *second.ArgumentsBytes <= NestedCallLimits.MaxArgumentBytesPerCall {
		t.Fatalf("argumentsBytes = %v", second.ArgumentsBytes)
	}
	if len(second.Error) != NestedCallLimits.MaxErrorChars {
		t.Fatalf("error length = %d", len(second.Error))
	}

	for i := range NestedCallLimits.MaxCalls {
		recorder.Finish(recorder.Start(recorderCall("c"+strconv.Itoa(i), ai.JsonObject{})), false, "")
	}
	if n := len(recorder.Snapshot().Calls); n != NestedCallLimits.MaxCalls {
		t.Fatalf("calls = %d, want %d", n, NestedCallLimits.MaxCalls)
	}
}

// upstream nested-tool-calls.test.ts:222-232
func TestNestedCallRecorderCapsTheTotalArgumentSizeAndMarksUnfinishedCallsIncomplete(t *testing.T) {
	recorder := NewNestedCallRecorder()
	chunk := ai.JsonObject{"text": strings.Repeat("x", 7000)}
	records := make([]*ai.NestedToolCallRecord, 6)
	for i := range records {
		records[i] = recorder.Start(recorderCall("c"+strconv.Itoa(i), chunk))
	}
	snapshot := recorder.Snapshot()
	// 32 KiB fits four 7000-byte argument objects.
	withArguments := 0
	for _, entry := range snapshot.Calls {
		if entry.Arguments != nil {
			withArguments++
		}
		if entry.Status != ai.NestedToolCallUnfinished {
			t.Fatalf("status = %q, want unfinished", entry.Status)
		}
	}
	if withArguments != 4 {
		t.Fatalf("calls with arguments = %d, want 4", withArguments)
	}
	if snapshot.Complete {
		t.Fatal("snapshot with unfinished calls is complete")
	}
	if len(records) != 6 {
		t.Fatalf("records = %d", len(records))
	}
}
