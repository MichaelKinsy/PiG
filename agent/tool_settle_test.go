package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// A parallel batch emits each call's tool_execution_end in the order the calls' tools settled, which a tool that learns its
// result from outside the process reports from the goroutine that reads the result (ToolSettleFromContext). The second call
// settles first here, so its end comes first even though the first call returns from Execute before it; and the first call
// settled before the second, so a second call that returns first is held until the first has ended.
func TestParallelToolEndsFollowTheOrderTheToolsSettle(t *testing.T) {
	ids := []string{"call-1", "call-2"}
	release := map[string]chan struct{}{"call-1": make(chan struct{}), "call-2": make(chan struct{})}
	returned := map[string]chan struct{}{"call-1": make(chan struct{}), "call-2": make(chan struct{})}
	var mu sync.Mutex
	slots := map[string]*jsloop.SettleSlot{}
	goroutines := map[string]uint64{}
	executing := make(chan struct{}, len(ids))
	tool := &scriptTool{name: "p", mode: ToolModeParallel, params: map[string]any{"type": "object"},
		execute: func(ctx context.Context, id string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			slot := ToolSettleFromContext(ctx, id)
			if slot == nil {
				t.Errorf("call %s has no settle slot", id)
				return AgentToolResult{}, nil
			}
			mu.Lock()
			slots[id] = slot
			goroutines[id] = goroutineid.Current()
			mu.Unlock()
			executing <- struct{}{}
			awaitSignal(t, release[id], "release of "+id)
			close(returned[id])
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: id}}}, nil
		}}
	rec := newEventRecorder(nil)
	calls := make([]ai.ToolCall, len(ids))
	for i, id := range ids {
		calls[i] = toolCall(id, "p", nil)
	}
	a := mustNewAgent(AgentOptions{
		Model:   scriptedModel(&scriptedProvider{respond: toolCallsThenText(calls...)}),
		Tools:   []AgentTool{tool},
		EventCh: rec.ch,
	})
	done := sendAsync(t, a, "run")
	for range ids {
		awaitSignal(t, executing, "both calls executing")
	}
	// The reader answers call-1 first, then call-2; the goroutine of call-2 then runs first.
	mu.Lock()
	slots["call-1"].Settle()
	slots["call-2"].Settle()
	mu.Unlock()
	close(release["call-2"])
	awaitSignal(t, returned["call-2"], "call-2 returns from Execute")
	// call-2 has returned and is held behind call-1, which settled before it: its goroutine parks before its hooks.
	mu.Lock()
	second := goroutines["call-2"]
	mu.Unlock()
	entered.Await(second, nil)
	close(release["call-1"])
	waitSignal(t, done, "the run")

	var ends, results []string
	for _, ev := range rec.stop() {
		switch ev := ev.(type) {
		case ToolExecutionEndEvent:
			ends = append(ends, ev.ToolCallID)
		case MessageEndEvent:
			if ev.Message.ToolResult != nil {
				results = append(results, ev.Message.ToolResult.ToolCallID)
			}
		}
	}
	if !reflect.DeepEqual(ends, ids) {
		t.Fatalf("tool_execution_end order = %v, want the settle order %v", ends, ids)
	}
	if !reflect.DeepEqual(results, ids) {
		t.Fatalf("tool result order = %v, want source order", results)
	}
}

// The calls of one parallel batch share a batch identifier a runtime can use to order only that batch's calls; another batch has another, and a call another tool
// makes inside its own (a different toolCallID) has none.
func TestToolBatchFromContextNamesTheParallelBatch(t *testing.T) {
	var mu sync.Mutex
	batches := map[string]string{}
	var nested string
	tool := &scriptTool{name: "p", mode: ToolModeParallel, params: map[string]any{"type": "object"},
		execute: func(ctx context.Context, id string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			mu.Lock()
			batches[id] = ToolBatchFromContext(ctx, id)
			nested = ToolBatchFromContext(ctx, "another-call")
			mu.Unlock()
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: id}}}, nil
		}}
	run := func(ids ...string) {
		t.Helper()
		calls := make([]ai.ToolCall, len(ids))
		for i, id := range ids {
			calls[i] = toolCall(id, "p", nil)
		}
		a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(calls...)}), Tools: []AgentTool{tool}})
		waitSignal(t, sendAsync(t, a, "run"), "the run")
	}
	run("a-1", "a-2")
	run("b-1", "b-2")
	mu.Lock()
	defer mu.Unlock()
	if batches["a-1"] == "" || batches["a-1"] != batches["a-2"] || batches["b-1"] == "" || batches["b-1"] != batches["b-2"] || batches["a-1"] == batches["b-1"] {
		t.Fatalf("batch identifiers = %v, want one per batch, shared by its calls", batches)
	}
	if nested != "" {
		t.Fatalf("a call under another call's id has batch %q, want none", nested)
	}
}

// settledBatch runs a batch of the calls in ids whose tools settle in ids order (the reader answers them in that order) and wake in wake order.
// Each call's after-tool-call hook is hook(ctx, id). It returns after the run.
type settledBatch struct {
	t      *testing.T
	ids    []string
	wake   []string
	hook   func(ctx context.Context, id string)
	events *eventRecorder
}

func (b *settledBatch) run() {
	t := b.t
	release := map[string]chan struct{}{}
	for _, id := range b.ids {
		release[id] = make(chan struct{})
	}
	var mu sync.Mutex
	slots := map[string]*jsloop.SettleSlot{}
	goroutines := map[string]uint64{}
	executing := make(chan struct{}, len(b.ids))
	tool := &scriptTool{name: "p", mode: ToolModeParallel, params: map[string]any{"type": "object"},
		execute: func(ctx context.Context, id string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			mu.Lock()
			slots[id] = ToolSettleFromContext(ctx, id)
			goroutines[id] = goroutineid.Current()
			mu.Unlock()
			executing <- struct{}{}
			awaitSignal(t, release[id], "release of "+id)
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: id}}}, nil
		}}
	b.events = newEventRecorder(nil)
	calls := make([]ai.ToolCall, len(b.ids))
	for i, id := range b.ids {
		calls[i] = toolCall(id, "p", nil)
	}
	a := mustNewAgent(AgentOptions{
		Model:   scriptedModel(&scriptedProvider{respond: toolCallsThenText(calls...)}),
		Tools:   []AgentTool{tool},
		EventCh: b.events.ch,
		AfterToolCallHooks: []AfterToolCallHook{func(ctx context.Context, id, _ string, _ json.RawMessage, _ AgentToolResult) AfterToolCallResult {
			b.hook(ctx, id)
			return AfterToolCallResult{}
		}},
	})
	done := sendAsync(t, a, "run")
	for range b.ids {
		awaitSignal(t, executing, "every call executing")
	}
	// The reader answers the calls in ids order before it wakes any goroutine.
	mu.Lock()
	for _, id := range b.ids {
		slots[id].Settle()
	}
	mu.Unlock()
	// Each woken goroutine runs up to its next wait before the next is woken, as the runtime may run them in any order: with the gate the later
	// call parks before its hooks, without it the hooks have started.
	for _, id := range b.wake {
		close(release[id])
		mu.Lock()
		goroutine := goroutines[id]
		mu.Unlock()
		entered.Await(goroutine, nil)
	}
	waitSignal(t, done, "the run")
}

// agent-loop.ts:619-647: each call continues from its own execute promise, so the afterToolCall hooks of a parallel batch start in the order the
// tools settled. The reader answers call-1, call-2, call-3 in that order and the goroutines wake in the reverse order: call-3 and call-2 must
// not start their hooks before call-1's.
func TestParallelToolHooksStartInTheOrderTheToolsSettle(t *testing.T) {
	ids := []string{"call-1", "call-2", "call-3"}
	var mu sync.Mutex
	var started []string
	batch := &settledBatch{t: t, ids: ids, wake: []string{"call-3", "call-2", "call-1"}, hook: func(_ context.Context, id string) {
		mu.Lock()
		started = append(started, id)
		mu.Unlock()
	}}
	batch.run()
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(started, ids) {
		t.Fatalf("after-tool-call hooks started as %v, want the settle order %v", started, ids)
	}
	var ends []string
	for _, ev := range batch.events.stop() {
		if end, ok := ev.(ToolExecutionEndEvent); ok {
			ends = append(ends, end.ToolCallID)
		}
	}
	if !reflect.DeepEqual(ends, ids) {
		t.Fatalf("tool_execution_end order = %v, want the settle order %v", ends, ids)
	}
}

// Pi starts the next call's hooks when the previous call's hooks reach their first await, not when they finish: a hook that reports it started
// (ToolHookStartedFromContext, which the extension dispatch calls) and then waits must not hold the next call's hook back.
func TestAToolHookThatStartedDoesNotHoldBackTheNextCallsHook(t *testing.T) {
	ids := []string{"call-1", "call-2"}
	secondStarted := make(chan struct{})
	batch := &settledBatch{t: t, ids: ids, wake: ids, hook: func(ctx context.Context, id string) {
		switch id {
		case "call-1":
			started := ToolHookStartedFromContext(ctx)
			if started == nil {
				t.Error("a call of a parallel batch has no hook start report")
				return
			}
			started()
			awaitSignal(t, secondStarted, "the second call's hook to start while the first waits")
		case "call-2":
			close(secondStarted)
		}
	}}
	batch.run()
	batch.events.stop()
}

// A call whose hooks invoke no extension handler never reports a start; it must not hold the later calls' hooks back once its hooks returned.
func TestAToolHookThatReportsNoStartDoesNotHoldBackTheNextCallsHook(t *testing.T) {
	ids := []string{"call-1", "call-2"}
	var mu sync.Mutex
	var started []string
	batch := &settledBatch{t: t, ids: ids, wake: ids, hook: func(_ context.Context, id string) {
		mu.Lock()
		started = append(started, id)
		mu.Unlock()
	}}
	batch.run()
	batch.events.stop()
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(started, ids) {
		t.Fatalf("hooks started as %v, want %v", started, ids)
	}
}
