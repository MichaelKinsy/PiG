// Ports the Pi 1.1.0 case "gives tools and hooks the Harness's models" of packages/durable/test/harness-tools.test.ts
// (src/harness/types.ts HookApi.models and ToolExecutionApi.models, tool.ts `models: runtime.models`). See
// harness_tasks_test.go for the Go mappings that apply to every case.

package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// TestToolsAndHooksSeeTheHarnessModels mirrors packages/durable/test/harness-tools.test.ts:115 ("gives tools and hooks the
// Harness's models"): ToolExecutionApi.models and HookApi.models are HarnessOptions.models.
func TestToolsAndHooksSeeTheHarnessModels(t *testing.T) {
	setup := chatSetup(t)
	var seen []durable.Models
	addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		seen = append(seen, api.Models())
		return tlNoContent(context.Background(), nil, api)
	}))
	addHooks(t, setup.Registry, ToolTask, &ToolHooks{BeforeTool: func(_ context.Context, _ ai.ToolCall, api HookApi) (*BeforeToolResult, error) {
		seen = append(seen, api.Models())
		return nil, nil
	}})
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"echo", map[string]any{}, "c1"}), tlDone()}, nil)
	if run.status != durable.SubmissionDone {
		t.Fatalf("status %s, want done", run.status)
	}
	if len(seen) != 2 {
		t.Fatalf("%d models reads, want one from the hook and one from the tool", len(seen))
	}
	for index, models := range seen {
		if models != durable.Models(setup.Models) {
			t.Fatalf("read %d returned %v, want the Harness's models %v", index, models, setup.Models)
		}
	}
	mustClose(t, run.harness)
}

// TestToolResultRecordsHowLongExecuteTook mirrors packages/durable/test/harness-tools.test.ts:432 ("records how long execute()
// took, excluding hooks, and nothing for calls that did not run"; src/harness/tool.ts:253-262,383,460-476): the duration spans
// the tool's own work, a tool that throws still has one, and a call a beforeTool hook blocked has none.
func TestToolResultRecordsHowLongExecuteTook(t *testing.T) {
	setup := chatSetup(t)
	work := func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		time.Sleep(30 * time.Millisecond) // the tool's simulated work is what the duration measures
		return tlNoContent(context.Background(), nil, api)
	}
	addTool(t, setup.Registry, tlTool("slow", work))
	addTool(t, setup.Registry, tlTool("thrower", func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		_, _ = work(ctx, args, api)
		return durable.ToolExecutionResult{}, errors.New("boom")
	}))
	addHooks(t, setup.Registry, ToolTask, &ToolHooks{BeforeTool: func(_ context.Context, call ai.ToolCall, _ HookApi) (*BeforeToolResult, error) {
		time.Sleep(100 * time.Millisecond) // hook time is not execute time
		if call.ID == "blocked" {
			return &BeforeToolResult{Block: new("no")}, nil
		}
		return nil, nil
	}})
	run := tlRun(t, setup, []ai.FauxResponseStep{
		tlCallsStep(tlCall{"slow", map[string]any{}, "slow"}, tlCall{"thrower", map[string]any{}, "thrower"}, tlCall{"slow", map[string]any{}, "blocked"}),
		tlDone(),
	}, nil)
	byId := map[string]ai.ToolResultMessage{}
	for _, result := range tlResults(run.entries) {
		byId[result.ToolCallID] = result
	}
	for _, id := range []string{"slow", "thrower"} {
		duration := byId[id].DurationMs
		if duration == nil || *duration < 25 || *duration >= 100 {
			t.Fatalf("%s: durationMs %v, want about 30 and below the 100 ms hook time", id, duration)
		}
	}
	if !byId["thrower"].IsError {
		t.Fatal("the thrower's result is not an error")
	}
	if byId["blocked"].DurationMs != nil {
		t.Fatalf("a blocked call has durationMs %d, want none", *byId["blocked"].DurationMs)
	}
	mustClose(t, run.harness)
}
