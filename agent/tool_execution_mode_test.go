package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/agent.ts:253 `this.toolExecution = runtimeOptions.toolExecution ?? "parallel"` and :479 createLoopConfig reads this.toolExecution once per run.

// overlapTool records the peak number of concurrently running calls. While gate holds a channel it signals entered and waits for the channel to close; otherwise it sleeps long enough for a parallel sibling to start.
func overlapTool(running, peak *atomic.Int32, entered chan<- struct{}, gate *atomic.Pointer[chan struct{}]) *scriptTool {
	return &scriptTool{name: "slow", params: map[string]any{"type": "object"}, execute: func(ctx context.Context, _ string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		if release := gate.Load(); release != nil {
			entered <- struct{}{}
			select {
			case <-*release:
			case <-ctx.Done():
			}
		} else {
			time.Sleep(60 * time.Millisecond)
		}
		running.Add(-1)
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}, nil
	}}
}

// agent.ts:253 an Agent built without toolExecution runs one message's tool calls in parallel.
func TestAgentDefaultsToParallelToolExecution(t *testing.T) {
	var running, peak atomic.Int32
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("a", "slow", nil), toolCall("b", "slow", nil))}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{overlapTool(&running, &peak, nil, &atomic.Pointer[chan struct{}]{})}})
	if a.ToolExecutionMode() != ToolModeParallel {
		t.Fatalf("default ToolExecutionMode = %q, want parallel", a.ToolExecutionMode())
	}
	mustSend(t, a, "go")
	if peak.Load() < 2 {
		t.Fatalf("default run peak concurrency = %d, want the two calls to overlap", peak.Load())
	}
}

// agent.ts:479 the mode is read when a run starts: assigning toolExecution while a batch is in flight leaves that batch parallel and takes effect on the next run.
func TestSetToolExecutionDuringARunAppliesToTheNextRun(t *testing.T) {
	var running, peak atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var gate atomic.Pointer[chan struct{}]
	gate.Store(&release)
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		switch call {
		case 1:
			return doneStream(toolUseMessage(toolCall("a", "slow", nil), toolCall("b", "slow", nil)))
		case 3:
			return doneStream(toolUseMessage(toolCall("c", "slow", nil), toolCall("d", "slow", nil)))
		}
		return doneStream(textMessage("done"))
	}}
	tool := overlapTool(&running, &peak, entered, &gate)
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tool}, ToolExecution: ToolModeParallel})

	done := sendAsync(t, a, "first")
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the second call never started: the batch did not run in parallel")
		}
	}
	a.SetToolExecution(ToolModeSequential)
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not finish")
	}
	if peak.Load() != 2 {
		t.Fatalf("first run peak = %d, want 2", peak.Load())
	}

	peak.Store(0)
	gate.Store(nil)
	mustSend(t, a, "second")
	if peak.Load() != 1 {
		t.Fatalf("second run peak = %d, want 1: the sequential mode applies to the next run", peak.Load())
	}
}
