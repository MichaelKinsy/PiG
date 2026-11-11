// Ports packages/durable/test/harness-park-children.test.ts (Pi 473515f9c), the describes "nested calls without waiting",
// "tasks created under a key" and "children after a restart": work a parked tool owns (spec suspendable §2.5, §4.4, §4.5).
// The describe "submissions that notify a task" is ported with the submissions (harness_submissions_test.go). See
// harness_tasks_test.go for the Go mappings that apply to every case.

package harness

// pi: packages/durable/src/harness/tool.ts

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

var tcTopic = map[string]any{"type": "object", "properties": map[string]any{"topic": map[string]any{"type": "string"}}, "required": []any{"topic"}}

func tcTool(name string, execute tlExecute, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	return ownTool(name, name, tcTopic, execute, edit...)
}

func tcCall(name string) ai.FauxResponseStep {
	return tlCallsStep(tlCall{name, map[string]any{"topic": ""}, "c1"})
}

func tcKey(key string) durable.NestedCallOptions { return durable.NestedCallOptions{Key: &key} }

// tcResearch is research of the Pi file: a nested tool that parks on tpDone under the key done until it holds a value,
// and answers with its text.
var tcResearch = tcTool("research", func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	state, err := durable.ReadSignal(ctx, api, tpDone, "done")
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if state != nil && !state.Expired {
		return ownText(fmt.Sprintf("%v: %s", args.(map[string]any)["topic"], state.Value.Text)), nil
	}
	return tpSleep(tpDone.Key("done")), nil
}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe })

// tcParkedNested is the nested calls of the root conversation that are parked, by call ID, once there are count.
func tcParkedNested(t *testing.T, harness Harness, count int) map[string]durable.TaskId {
	t.Helper()
	parked := map[string]durable.TaskId{}
	waitFor(t, func() bool {
		parked = map[string]durable.TaskId{}
		for _, slot := range tpLive(t, harness, 1).NestedTools {
			if slot.Status == ToolSlotSleeping {
				parked[slot.CallId] = slot.TaskId
			}
		}
		return len(parked) == count
	})
	return parked
}

type tcN struct {
	N int `json:"n"`
}

// tcOneStep is a task of one phase that completes with null, such as Idle and Child of the Pi file.
func tcOneStep[I any](name string) durable.Task[I, tpPhase, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[I, tpPhase, durable.JsonValue, any]{
		Name: name, Version: 1,
		Initial: func(I) tpPhase { return tpPhase{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[I, tpPhase, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ durable.RunningTask[I, tpPhase, durable.JsonValue], runtime durable.TaskRuntime[I, tpPhase, durable.JsonValue, any]) error {
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[I, tpPhase, durable.JsonValue]) (*durable.NextTaskState[tpPhase, durable.JsonValue], error) {
					return completed[tpPhase, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[I, tpPhase, durable.JsonValue], runtime durable.TaskRuntime[I, tpPhase, durable.JsonValue, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[I, tpPhase, durable.JsonValue]) (*durable.NextTaskState[tpPhase, durable.JsonValue], error) {
				return abortedWith[tpPhase, durable.JsonValue]("test"), nil
			})
		},
	})
}

func TestParkChildrenNestedCallsWithoutWaiting(t *testing.T) {
	// harness-park-children.test.ts:108 it("park on their handles with policy any and collect the results by key across wakes")
	t.Run("park on their handles with policy any and collect the results by key across wakes", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tcResearch)
		wakes := &tpRuns{}
		addTool(t, setup.Registry, tcTool("both", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			wakes.add(tpRun{wake: api.Wake()})
			a, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": "x"}, tcKey("a"))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			b, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": "y"}, tcKey("b"))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			var done []durable.TaskId
			if checkpoint, ok := api.Checkpoint(); ok {
				for _, id := range plainObject(checkpoint)["done"].([]any) {
					done = append(done, durable.TaskId(id.(float64)))
				}
			}
			if wake := api.Wake(); wake != nil && wake.Reason == durable.WakeTasks {
				for _, id := range wake.Ended {
					if !slices.Contains(done, id) {
						done = append(done, id)
					}
				}
			}
			var open []durable.TaskId
			for _, nested := range []durable.NestedToolCall{a, b} {
				if !slices.Contains(done, nested.TaskId) {
					open = append(open, nested.TaskId)
				}
			}
			if len(open) > 0 {
				checkpoint := make([]any, len(done))
				for index, id := range done {
					checkpoint[index] = float64(id)
				}
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Wait: &durable.ToolParkWait{
					Checkpoint: map[string]any{"done": checkpoint}, HasCheckpoint: true, On: open, Policy: durable.JoinAny,
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
			return ownText(fmt.Sprintf("%v + %v", ra.StructuredOutput, rb.StructuredOutput)), nil
		}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe }))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tcCall("both"), tlDone()})
		harness, root := tpOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		parked := tcParkedNested(t, harness, 2)
		tpIdle(t, harness)
		tpSignal(t, harness, parked["c1/b"], "done", "bee")
		// Pi's single thread runs b's wake before the next line. Go runs the two research tasks and the parent on goroutines of
		// their own, and the parent's slot shows waiting from its first park until the wake run starts, so the status alone
		// holds at once and lets a's signal overtake b's wake: the runs then report a's end first, or both ends in one wake.
		// Wait for the wake run, then for the park after it.
		waitFor(t, func() bool { return len(wakes.all()) == 2 })
		waitFor(t, func() bool {
			slot := tpSlot(t, harness)
			return slot != nil && slot.Status == ToolSlotWaiting
		})
		tpSignal(t, harness, parked["c1/a"], "done", "ay")
		if status := tpWait(t, submission); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if got := trText(tlResults(allEntries(t, root))[0]); got != "x: ay + y: bee" {
			t.Fatalf("text %q", got)
		}
		tpExpectRuns(t, wakes.all(),
			tpRun{},
			tpRun{wake: &durable.WakeReason{Reason: durable.WakeTasks, Ended: []durable.TaskId{parked["c1/b"]}}},
			tpRun{wake: &durable.WakeReason{Reason: durable.WakeTasks, Ended: []durable.TaskId{parked["c1/a"]}}},
		)
		mustClose(t, harness)
	})

	// harness-park-children.test.ts:156 it("need explicit keys after a wake, for nested calls and tasks alike")
	t.Run("need explicit keys after a wake, for nested calls and tasks alike", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tcResearch)
		idle := tcOneStep[durable.JsonValue]("test.idle")
		addTask(t, setup.Registry, idle.Erased())
		var errs syncList[string]
		addTool(t, setup.Registry, tcTool("woken", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			if api.Wake() == nil {
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Until: new(0.0)}}}, nil
			}
			if _, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": "x"}); err != nil {
				errs.add(err.Error())
			}
			if _, err := durable.ToolCreateTask(ctx, api, idle, nil, durable.ToolTaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: api.TaskId()}}); err != nil {
				errs.add(err.Error())
			}
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tcCall("woken"), tlDone()})
		harness, root := tpOpen(t, setup)
		tpWait(t, ownSubmit(t, root, ownInput("go")))
		tpExpect(t, "errors", errs.all(), []string{"Tool call c1 needs explicit keys after a wake", "Tool call c1 needs explicit keys after a wake"})
		mustClose(t, harness)
	})
}

func TestParkChildrenTasksCreatedUnderAKey(t *testing.T) {
	// harness-park-children.test.ts:193 it("return the task a key names, and refuse another kind, other input, or a nested call's key")
	t.Run("return the task a key names, and refuse another kind, other input, or a nested call's key", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tcResearch)
		child := tcOneStep[tcN]("test.keyed")
		addTask(t, setup.Registry, child.Erased())
		var seen syncList[any]
		addTool(t, setup.Registry, tcTool("keyed", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			own := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: api.TaskId()}}
			create := func(n int, key string) {
				id, err := durable.ToolCreateTask(ctx, api, child, tcN{N: n}, durable.ToolTaskOptions{TaskOptions: own, Key: &key})
				if err != nil {
					seen.add(err.Error())
					return
				}
				seen.add(id)
			}
			create(1, "k")
			create(1, "k")
			create(2, "k")
			if _, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": "x"}, tcKey("n")); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			create(1, "n")
			if _, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": "x"}, tcKey("k")); err != nil {
				seen.add(err.Error())
			} else {
				seen.add(nil)
			}
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tcCall("keyed"), tlDone()})
		harness, root := tpOpen(t, setup)
		tpWait(t, ownSubmit(t, root, ownInput("go")))
		got := seen.all()
		if len(got) < 2 {
			t.Fatalf("seen %v", got)
		}
		if _, isId := got[0].(durable.TaskId); !isId || got[1] != got[0] {
			t.Fatalf("first %v and again %v, want one task ID", got[0], got[1])
		}
		tpExpect(t, "refused", got[2:], []string{
			"Key k of tool call c1 was already used for another call or task",
			"Key n of tool call c1 was already used for another call or task",
			"Nested call c1/k was already made with another tool or other arguments",
		})
		mustClose(t, harness)
	})
}

func TestParkChildrenAfterARestart(t *testing.T) {
	// harness-park-children.test.ts:239 it("keeps the children of a parked replay-unsafe call")
	t.Run("keeps the children of a parked replay-unsafe call", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		addTool(t, setup.Registry, tcResearch)
		addTool(t, setup.Registry, tcTool("unsafe", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			nested, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": "x"}, tcKey("a"))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if api.Wake() == nil {
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Wait: &durable.ToolParkWait{On: []durable.TaskId{nested.TaskId}, Policy: durable.JoinAllSettled}}}, nil
			}
			result, err := nested.Result(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return ownText(fmt.Sprint(result.StructuredOutput)), nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tcCall("unsafe"), tlDone()})
		harness, root := trOpen(t, path, setup)
		submissionId := ownSubmit(t, root, ownInput("go")).Id()
		parked := tcParkedNested(t, harness, 1)
		mustClose(t, harness)
		harness, root = trOpen(t, path, setup)
		child := parked["c1/a"]
		tpIdle(t, harness)
		// Not abandoned: its owner was parked, not cut off while running.
		if tkTaskRecord(t, harness, child).AbortRequested {
			t.Fatal("the child was abandoned")
		}
		tpSignal(t, harness, child, "done", "kept")
		if status := tpWait(t, tpStoredSubmission(t, harness, submissionId)); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		if got := trText(tlResults(allEntries(t, root))[0]); got != "x: kept" {
			t.Fatalf("text %q", got)
		}
		mustClose(t, harness)
	})

	// harness-park-children.test.ts:267 it("abandons the parked children of a replay-unsafe call cut off while it ran")
	t.Run("abandons the parked children of a replay-unsafe call cut off while it ran", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		addTool(t, setup.Registry, tcResearch)
		var nestedResults syncList[string]
		addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{
			AfterTool: func(_ context.Context, hooked ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
				if hooked.Parent != nil {
					for _, diagnostic := range result.Diagnostics {
						nestedResults.add(diagnostic.Message)
					}
				}
				return nil, nil
			},
		})
		started := deferred()
		addTool(t, setup.Registry, tcTool("unsafe", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			if _, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": "x"}, tcKey("a")); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			started.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{tcCall("unsafe"), tlDone()})
		crashed := thOpen(t, path, setup)
		submissionId := ownSubmit(t, crashed.root, ownInput("go")).Id()
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		parked := tcParkedNested(t, crashed.harness, 1)
		thStored()
		// The process dies here: another one opens the same database.
		crashed.kill()
		next := thOpen(t, path, setup)
		if status := tpWait(t, tpStoredSubmission(t, next.harness, submissionId)); status != durable.SubmissionDone {
			t.Fatalf("submission %q, want done", status)
		}
		child := tpWaitTask(t, next.harness, parked["c1/a"])
		if child.AbortReason != durable.AbortedByRestart || child.State.Outcome.Status != durable.OutcomeAborted {
			t.Fatalf("child abortReason %q outcome %q, want restart and aborted", child.AbortReason, child.State.Outcome.Status)
		}
		tpExpect(t, "nested results", nestedResults.all(), []string{"Tool research was parked: its caller ended with a restart"})
		if stored := tpLive(t, next.harness, next.root.Id()).NestedTools; len(stored) != 0 {
			t.Fatalf("nested slots %v, want none", stored)
		}
		mustClose(t, next.harness)
	})
}
