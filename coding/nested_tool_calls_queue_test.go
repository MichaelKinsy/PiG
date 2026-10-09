package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// The limits are upstream's literal NESTED_CALL_LIMITS values (nested-tool-calls.ts:26-31); the upstream test reads them back from the constant.
func TestNestedCallLimitsAreUpstreamsLiterals(t *testing.T) {
	if NestedCallLimits.MaxCalls != 256 || NestedCallLimits.MaxArgumentBytesPerCall != 8*1024 || NestedCallLimits.MaxArgumentBytesTotal != 32*1024 || NestedCallLimits.MaxErrorChars != 500 {
		t.Fatalf("limits = %+v", NestedCallLimits)
	}
}

// upstream nested-tool-calls.ts:196-199: a host that runs tool calls sequentially makes every nested call exclusive, including calls to a tool whose own mode is parallel.
func TestNestedToolCallRunnerSequentialHostSerializesParallelTools(t *testing.T) {
	var active, peak atomic.Int32
	tools := []agent.AgentTool{&nestedTestTool{name: "parallel", run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
		now := active.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return textResult("")
	}}}
	runner, _ := newNestedTestRunner(&tools, true)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			_, _ = runner.Execute(t.Context(), "call", "parallel", json.RawMessage(`{}`), NestedToolCallOptions{})
		})
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatalf("peak concurrency = %d, want 1", peak.Load())
	}
}

// upstream nested-tool-calls.ts:184,209-213 (holdsQueue): a call that holds the exclusive queue makes its own nested calls without waiting on it, so a sequential tool that calls a sequential tool completes.
func TestNestedToolCallRunnerNestedCallsOfAQueueHolderDoNotWaitOnTheQueue(t *testing.T) {
	// nested-tool-calls.ts:200-216: the call is exclusive because the host is sequential or because its tool is.
	for _, sequentialHost := range []bool{false, true} {
		t.Run(fmt.Sprintf("sequential host %t", sequentialHost), func(t *testing.T) {
			mode := agent.ToolModeSequential
			if sequentialHost {
				mode = agent.ToolModeParallel
			}
			var runner *NestedToolCallRunner
			tools := []agent.AgentTool{}
			tools = append(tools,
				&nestedTestTool{name: "outer", mode: mode, run: func(ctx context.Context, id string, _ agent.ToolUpdateCallback) agent.AgentToolResult {
					_, _ = runner.Execute(ctx, id, "inner", json.RawMessage(`{}`), NestedToolCallOptions{})
					return textResult("outer")
				}},
				&nestedTestTool{name: "inner", mode: mode, run: func(context.Context, string, agent.ToolUpdateCallback) agent.AgentToolResult {
					return textResult("inner")
				}},
			)
			runner, _ = newNestedTestRunner(&tools, sequentialHost)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = runner.Execute(t.Context(), "call", "outer", json.RawMessage(`{}`), NestedToolCallOptions{})
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("a nested call of a queue holder waited on its own exclusive queue")
			}
			summary := runner.TakeRecord("call")
			if summary == nil || summary.Calls == nil || len(summary.Calls.Calls) != 2 {
				t.Fatalf("summary = %+v, want the outer and its inner call recorded on the model-issued call", summary)
			}
		})
	}
}
