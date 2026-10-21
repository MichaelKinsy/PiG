package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// signal notes that a goroutine parked, without ever blocking the gate's lock when the note is already pending.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Slots run their actions in the order they settled, whatever order their goroutines reach Run in; a slot that never settled
// settles when it reaches Run; a settled slot that is closed without running stops holding up the slots behind it.
func TestSettleGateRunsActionsInSettleOrder(t *testing.T) {
	var gate SettleGate
	parked := make(chan struct{}, 8)
	gate.SetParkedHook(func() { signal(parked) })
	first, second, third := gate.NewSlot(), gate.NewSlot(), gate.NewSlot()
	third.Settle()
	first.Settle()
	// second is never settled by a reader: it settles on reaching Run, behind both.

	var mu sync.Mutex
	var order []string
	record := func(name string) func() {
		return func() {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
		}
	}
	var done sync.WaitGroup
	done.Go(func() { second.Run(record("second")) })
	// second holds the turn behind third and first, which settled before it reached Run.
	awaitSignal(t, parked, "second waits for the slots that settled before it")
	done.Go(func() { first.Run(record("first")) })
	done.Go(func() { third.Run(record("third")) })
	done.Wait()
	if want := []string{"third", "first", "second"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("actions ran in order %v, want the settle order %v", order, want)
	}

	// A settled slot that is closed without running releases the slots behind it.
	var gate2 SettleGate
	abandoned, waiting := gate2.NewSlot(), gate2.NewSlot()
	abandoned.Settle()
	parked2 := make(chan struct{}, 1)
	gate2.SetParkedHook(func() { signal(parked2) })
	ran := make(chan struct{})
	go waiting.Run(func() { close(ran) })
	awaitSignal(t, parked2, "the slot waits behind the settled one")
	abandoned.Close()
	awaitSignal(t, ran, "the slot behind an abandoned one runs")
}

// A parallel batch emits each call's tool_execution_end in the order the calls' tools settled, which a tool that learns its
// result from outside the process reports from the goroutine that reads the result (ToolSettleFromContext). The second call
// settles first here, so its end comes first even though the first call returns from Execute before it; and the first call
// settled before the second, so a second call that returns first is held until the first has ended.
func TestParallelToolEndsFollowTheOrderTheToolsSettle(t *testing.T) {
	ids := []string{"call-1", "call-2"}
	release := map[string]chan struct{}{"call-1": make(chan struct{}), "call-2": make(chan struct{})}
	returned := map[string]chan struct{}{"call-1": make(chan struct{}), "call-2": make(chan struct{})}
	var mu sync.Mutex
	slots := map[string]*SettleSlot{}
	executing := make(chan struct{}, len(ids))
	parked := make(chan struct{}, len(ids))
	tool := &scriptTool{name: "p", mode: ToolModeParallel, params: map[string]any{"type": "object"},
		execute: func(ctx context.Context, id string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			slot := ToolSettleFromContext(ctx, id)
			if slot == nil {
				t.Errorf("call %s has no settle slot", id)
				return AgentToolResult{}, nil
			}
			mu.Lock()
			slots[id] = slot
			mu.Unlock()
			slot.gate.SetParkedHook(func() { signal(parked) })
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
	// call-2 has returned and is held behind call-1, which settled before it.
	awaitSignal(t, parked, "call-2 waits for call-1")
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
