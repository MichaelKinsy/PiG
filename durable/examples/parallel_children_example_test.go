// Ports packages/durable/test/examples/42-parallel-children.ts: a tool that starts two nested calls without waiting for
// them and parks until either ends, so the Session holds no process while they run. Each wake adds the ended calls to
// its checkpoint; the last one collects every result by key. The script's printing is the transcript this test reads.
//
// Go mapping: the script's pauses of a few milliseconds become settledWake; the faux tool call has an ID, so the nested
// calls' IDs ("<call>/a", "<call>/b") are known.

package examples_test

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/schema"
)

type reportValue struct {
	Text string `json:"text"`
}

type researchCheckpoint struct {
	Done []durable.TaskId `json:"done"`
}

// Pi: packages/durable/src/harness/parks.ts (task waits), packages/durable/src/harness/tool.ts (nested calls without waiting).
func TestExample42ParallelChildren(t *testing.T) {
	var script printed

	// A research call parks until a (simulated) external researcher reports back.
	reportSignal := durable.DefineSignal(durable.SignalDefinition[reportValue]{
		Name: "example.report", Version: 1, Schema: schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
	})
	research := &durable.ToolRegistration{
		Name:        "research",
		Description: "Research a topic",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"topic": map[string]any{"type": "string"}},
			"required":   []any{"topic"},
		},
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			topic := args.(map[string]any)["topic"].(string)
			state, err := durable.ReadSignal(ctx, api, reportSignal, "report")
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if state != nil && !state.Expired {
				return textResult(topic + ": " + state.Value.Text), nil
			}
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Signals: []durable.SignalKey{reportSignal.Key("report")}}}}, nil
		},
	}

	var runs atomic.Int32
	researchBoth := &durable.ToolRegistration{
		Name:        "research_both",
		Description: "Research two topics in parallel",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"x": map[string]any{"type": "string"}, "y": map[string]any{"type": "string"}},
			"required":   []any{"x", "y"},
		},
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			input := args.(map[string]any)
			// Keys name the calls across wakes: the same key finds the same call again.
			a, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": input["x"]}, durable.NestedCallOptions{Key: new("a")})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			b, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": input["y"]}, durable.NestedCallOptions{Key: new("b")})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			runs.Add(1)
			checkpoint, err := checkpointOf[researchCheckpoint](api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			done := slices.Clone(checkpoint.Done)
			wake := api.Wake()
			if wake != nil && wake.Reason == durable.WakeTasks {
				for _, id := range wake.Ended {
					if !slices.Contains(done, id) {
						done = append(done, id)
					}
				}
			}
			wakeJSON := "undefined"
			if wake != nil {
				wakeJSON = jsonText(wake)
			}
			script.add(fmt.Sprintf("  [research_both] run with wake %s, %d of 2 done", wakeJSON, len(done)))
			var open []durable.TaskId
			for _, call := range []durable.NestedToolCall{a, b} {
				if !slices.Contains(done, call.TaskId) {
					open = append(open, call.TaskId)
				}
			}
			if len(open) > 0 {
				if done == nil {
					done = []durable.TaskId{}
				}
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Wait: &durable.ToolParkWait{
					Checkpoint: must(durable.ToJsonValue(researchCheckpoint{Done: done})), HasCheckpoint: true, On: open, Policy: durable.JoinAny,
				}}}, nil
			}
			ra, err := a.Result(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			rb, err := b.Result(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return textResult(fmt.Sprintf("%v\n%v", ra.StructuredOutput, rb.StructuredOutput)), nil
		},
	}

	models := fauxModels(
		fauxToolTurn("research_both", map[string]any{"x": "durable objects", "y": "sqlite"}, "both"),
		fauxAnswer("Both researched."),
	)
	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{Name: "research", Tools: []*durable.ToolRegistration{research, researchBoth}})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry, OnWake: func(harness.Wake) {}})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Research both")}))

	// Wait until both nested calls are parked; their tool call waits on them.
	nested := func() []harness.NestedToolSlot { return liveOf(t, opened, root).NestedTools }
	eventually(t, func() bool {
		sleeping := 0
		for _, slot := range nested() {
			if slot.Status == harness.ToolSlotSleeping {
				sleeping++
			}
		}
		return sleeping >= 2
	})
	wake := settledWake(opened)
	var tools []string
	for _, slot := range liveOf(t, opened, root).Tools {
		tools = append(tools, slot.Name+" "+string(slot.Status))
	}
	var nestedSlots []string
	for _, slot := range nested() {
		nestedSlots = append(nestedSlots, slot.CallId+" "+string(slot.Status))
	}
	script.add(fmt.Sprintf("tool: %q nested: %q", tools, nestedSlots))
	script.add("wake while everything is parked: " + wakeText(wake))

	slots := nested()
	if len(slots) != 2 {
		t.Fatalf("nested calls = %+v, want two", slots)
	}
	a, b := slots[0], slots[1]
	script.add("report b: " + string(must(harness.Signal(background, opened, b.TaskId, reportSignal, "report", reportValue{Text: "embedded, single file"}))))
	// The parent wakes for b, runs again, and waits on a.
	eventually(t, func() bool {
		tools := liveOf(t, opened, root).Tools
		return runs.Load() >= 2 && len(tools) > 0 && tools[0].Status == harness.ToolSlotWaiting
	})
	script.add("report a: " + string(must(harness.Signal(background, opened, a.TaskId, reportSignal, "report", reportValue{Text: "one per object, with storage"}))))
	waitSubmission(t, submission)
	script.add("result:\n" + newestToolResult(t, root, 10))

	expectEqual(t, "script", script.all(), []string{
		"  [research_both] run with wake undefined, 0 of 2 done",
		`tool: ["research_both waiting"] nested: ["both/a sleeping" "both/b sleeping"]`,
		`wake while everything is parked: {"active":false}`,
		"report b: delivered",
		fmt.Sprintf(`  [research_both] run with wake {"reason":"tasks","ended":[%d]}, 1 of 2 done`, b.TaskId),
		"report a: delivered",
		fmt.Sprintf(`  [research_both] run with wake {"reason":"tasks","ended":[%d]}, 2 of 2 done`, a.TaskId),
		"result:\ndurable objects: one per object, with storage\nsqlite: embedded, single file",
	})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
