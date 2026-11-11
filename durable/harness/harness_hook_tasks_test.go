// Ports packages/durable/test/harness-hook-tasks.test.ts (Pi 473515f9c): hooks that start tasks (spec suspendable §6):
// `afterTools` tasks that end the run or answer it with a steer, and `onYield` tasks that decide whether an answer
// stands while the run's inputs stay open. See harness_tasks_test.go for the Go mappings that apply to every case.
//
// Go mappings specific to this file. A crash is a killableStorage (harness_after_run_test.go), as there: the first process
// of "asks the afterTools hooks again" is killed while its hook runs, where the TypeScript test leaves it parked on its gate
// and opens the next process next to it. A task or hook that waits on a gate also waits for the signal of its invocation
// and for the end of the tests (arWait). A value of the decisions is a Go map or nil, the JSON the task completes with
// (`{ continue: 5 }` is map{"continue": 5.0}).

package harness

// pi: packages/durable/src/harness/generation.ts

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// hkCall is the tool-calling answer the afterTools tests start their round with.
func hkCall() ai.FauxResponseStep { return tlCallsStep(tlCall{name: "noop", args: map[string]any{}, id: "c1"}) }

// hkPlan is what a hook task completes with, and an optional steer it submits in its terminal commit.
type hkPlan struct {
	result durable.JsonValue
	steer  string
	gate   *deferredGate
}

// hkSeen is what the deciders of one test saw: the answer of `run.yield` at each run, and how often the hook was asked.
type hkSeen struct {
	mu     sync.Mutex
	yields []*durable.EntryId
	asked  atomic.Int32
}

func (seen *hkSeen) addYield(answer *durable.EntryId) {
	seen.mu.Lock()
	defer seen.mu.Unlock()
	seen.yields = append(seen.yields, answer)
}

func (seen *hkSeen) yieldList() []*durable.EntryId {
	seen.mu.Lock()
	defer seen.mu.Unlock()
	return slices.Clone(seen.yields)
}

// hkDeciderTask is a task that waits for its plan's gate, submits its steer, and completes with its plan's result.
func hkDeciderTask(plan hkPlan, seen *hkSeen) stepTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    "test.decider",
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "decide"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"decide": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				if plan.gate != nil {
					if err := arWait(plan.gate, runtime.Signal()); err != nil {
						return err
					}
				}
				// The answer a yield task decides about is in `run.yield`.
				live, err := durable.Snapshot[LiveState](ctx, runtime, LiveDoc, runtime.ConversationId())
				if err != nil {
					return err
				}
				var answer *durable.EntryId
				if live != nil && live.Run != nil && live.Run.Yield != nil {
					answer = &live.Run.Yield.Answer
				}
				seen.addYield(answer)
				handle, err := runtime.Conversation(ctx, runtime.ConversationId())
				if err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					if plan.steer != "" {
						if _, err := handle.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(plan.steer), WhenBusy: durable.WhenBusySteer}); err != nil {
							return nil, err
						}
					}
					return completed[stepState, durable.JsonValue](plan.result), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

// hkNoop is the tool of the rounds: it returns an empty result.
func hkNoop() *durable.ToolRegistration {
	return ownTool("noop", "noop", emptyObjectSchema(), func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{}, nil
	})
}

// hkSetup installs the extension "deciders": the noop tool, the decider task, and a hook (afterTools or onYield) that asks
// for one decider the first time it is asked.
func hkSetup(t *testing.T, hookName string, plan hkPlan) (*chatState, *hkSeen) {
	t.Helper()
	setup := chatSetup(t)
	seen := &hkSeen{}
	decider := hkDeciderTask(plan, seen)
	ask := func() *HookTasks {
		if seen.asked.Add(1) == 1 {
			return &HookTasks{Tasks: []HookTask{HookTaskOf(decider, durable.JsonValue(nil))}}
		}
		return nil
	}
	hooks := &GenerationHooks{}
	switch hookName {
	case "afterTools":
		hooks.AfterTools = func(context.Context, durable.EntryId, []durable.EntryId, HookApi) (*HookTasks, error) { return ask(), nil }
	case "onYield":
		hooks.OnYield = func(context.Context, ai.AssistantMessage, HookApi) (YieldResult, error) {
			if asked := ask(); asked != nil {
				return &YieldTasks{Tasks: asked.Tasks}, nil
			}
			return nil, nil
		}
	default:
		t.Fatalf("unknown hook %q", hookName)
	}
	mustInstall(t, setup.Registry, new(durable.Extension{
		Name:  "deciders",
		Tools: []*durable.ToolRegistration{hkNoop()},
		Tasks: []durable.AnyTask{decider.Erased()},
		Hooks: []durable.HookRegistration{Hook(GenerationTask, hooks)},
	}))
	return setup, seen
}

func hkOpen(t *testing.T, setup *chatState) (Harness, Conversation) {
	t.Helper()
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	harness.Resume()
	return harness, root
}

// hkTexts are the texts of the first model message of every entry that has one, in order.
func hkTexts(t *testing.T, root Conversation) []string {
	t.Helper()
	texts := []string{}
	for _, entry := range allEntries(t, root) {
		if len(entry.Model) == 0 {
			continue
		}
		if text, ok := textOf(entry.Model[0]); ok {
			texts = append(texts, text)
		}
	}
	return texts
}

func hkExpectTexts(t *testing.T, root Conversation, want ...string) {
	t.Helper()
	if got := hkTexts(t, root); !slices.Equal(got, want) {
		t.Fatalf("texts = %q, want %q", got, want)
	}
}

// hkWaitYield waits until the run's inputs wait for their yield tasks.
func hkWaitYield(t *testing.T, harness Harness, root Conversation) {
	t.Helper()
	waitFor(t, func() bool {
		live := liveOf(t, harness, root.Id())
		return live != nil && live.Run != nil && live.Run.Yield != nil
	})
}

func hkEntryIdWithText(t *testing.T, root Conversation, text string) durable.EntryId {
	t.Helper()
	for _, entry := range allEntries(t, root) {
		if len(entry.Model) > 0 {
			if got, ok := textOf(entry.Model[0]); ok && got == text {
				return entry.Id
			}
		}
	}
	t.Fatalf("no entry with text %q", text)
	return 0
}

func hkExpectDone(t *testing.T, settled durable.SettledSubmissionRecord) {
	t.Helper()
	if settled.Status != durable.SubmissionDone {
		t.Fatalf("status = %s, want done", settled.Status)
	}
}

// hkAnswerOf is the answer of a settled input, nil for any other settlement.
func hkAnswerOf(settled durable.SettledSubmissionRecord) *durable.EntryId {
	if settled.Status == durable.SubmissionDone && settled.Type == durable.SubmissionTypeInput {
		return settled.Answer
	}
	return nil
}

func hkExpectAnswer(t *testing.T, settled durable.SettledSubmissionRecord, want durable.EntryId) {
	t.Helper()
	if got := hkAnswerOf(settled); got == nil || *got != want {
		t.Fatalf("answer = %v, want %d", got, want)
	}
}

func TestAfterToolsTasks(t *testing.T) {
	t.Run("end the run with { terminate: true }, as a round whose calls all asked to terminate", func(t *testing.T) {
		setup, _ := hkSetup(t, "afterTools", hkPlan{result: map[string]any{"terminate": true}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{hkCall()})
		harness, root := hkOpen(t, setup)
		settled := must(ownSubmit(t, root, ownInput("go")).Wait(testContext))
		hkExpectDone(t, settled)
		// Answered with the tool-calling answer; no further request.
		first := arKindOf(allEntries(t, root), "pi.assistant")[0]
		hkExpectAnswer(t, settled, first.Id)
		if got := setup.Faux.CallCount(); got != 1 {
			t.Fatalf("calls = %d, want 1", got)
		}
		closeHarness(t, harness)
	})

	t.Run("answer the run with a steer the next request sees", func(t *testing.T) {
		setup, _ := hkSetup(t, "afterTools", hkPlan{result: nil, steer: "also check the tests"})
		setup.Faux.SetResponses([]ai.FauxResponseStep{hkCall(), fauxAnswer("checked")})
		harness, root := hkOpen(t, setup)
		hkExpectDone(t, must(ownSubmit(t, root, ownInput("go")).Wait(testContext)))
		hkExpectTexts(t, root, "go", "also check the tests", "checked")
		closeHarness(t, harness)
	})

	t.Run("report a result that is no AfterToolsDecision and go on", func(t *testing.T) {
		setup, _ := hkSetup(t, "afterTools", hkPlan{result: map[string]any{"continue": "x"}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{hkCall(), fauxAnswer("done")})
		harness, root := hkOpen(t, setup)
		hkExpectDone(t, must(ownSubmit(t, root, ownInput("go")).Wait(testContext)))
		infos := arInfosOf(setup, durable.ReportExtensionError)
		if !slices.ContainsFunc(infos, func(info durable.ReportInfo) bool { return info.Extension == "deciders" }) {
			t.Fatalf("extension_error infos = %+v, want one against deciders", infos)
		}
		if got := setup.Faux.CallCount(); got != 2 {
			t.Fatalf("calls = %d, want 2", got)
		}
		closeHarness(t, harness)
	})
}

func TestOnYieldTasks(t *testing.T) {
	t.Run("keep the inputs open while they decide, and continue the run with { continue }", func(t *testing.T) {
		gate := deferred()
		setup, seen := hkSetup(t, "onYield", hkPlan{result: map[string]any{"continue": "review: add a test"}, gate: gate})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first"), fauxAnswer("second")})
		harness, root := hkOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		hkWaitYield(t, harness, root)
		// The answer is appended, the input not yet answered.
		if status := must(submission.Status(testContext)).Status; status != durable.SubmissionPlaced {
			t.Fatalf("status = %s, want placed", status)
		}
		gate.resolve()
		settled := must(submission.Wait(testContext))
		hkExpectTexts(t, root, "go", "first", "review: add a test", "second")
		entries := allEntries(t, root)
		hkExpectAnswer(t, settled, arLast(entries).Id)
		first := hkEntryIdWithText(t, root, "first")
		yields := seen.yieldList()
		if len(yields) != 1 || yields[0] == nil || *yields[0] != first {
			t.Fatalf("yields = %v, want [%d]", yields, first)
		}
		closeHarness(t, harness)
	})

	t.Run("ignore a continuation that holds no valid user input", func(t *testing.T) {
		for _, input := range []durable.JsonValue{
			float64(5),
			[]any{},
			[]any{map[string]any{"type": "text"}},
			[]any{map[string]any{"type": "image", "data": "x"}},
		} {
			setup, _ := hkSetup(t, "onYield", hkPlan{result: map[string]any{"continue": input}})
			setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first")})
			harness, root := hkOpen(t, setup)
			hkExpectDone(t, must(ownSubmit(t, root, ownInput("go")).Wait(testContext)))
			hkExpectTexts(t, root, "go", "first")
			closeHarness(t, harness)
		}
	})

	t.Run("let an answer that stands run its afterRun tasks next, without run.yield", func(t *testing.T) {
		setup, _ := hkSetup(t, "onYield", hkPlan{result: nil})
		var mu sync.Mutex
		asked := []*LiveRunYield{}
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name: "after-run",
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterRun: func(ctx context.Context, _ RunSummary, api HookApi) (*HookTasks, error) {
				live, err := durable.Snapshot[LiveState](ctx, api, LiveDoc, api.ConversationId())
				if err != nil {
					return nil, err
				}
				var yield *LiveRunYield
				if live != nil && live.Run != nil {
					yield = live.Run.Yield
				}
				mu.Lock()
				defer mu.Unlock()
				asked = append(asked, yield)
				return nil, nil
			}})},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first")})
		harness, root := hkOpen(t, setup)
		hkExpectDone(t, must(ownSubmit(t, root, ownInput("go")).Wait(testContext)))
		arWaitIdle(t, root)
		mu.Lock()
		defer mu.Unlock()
		if len(asked) != 1 || asked[0] != nil {
			t.Fatalf("asked = %+v, want one call that saw no run.yield", asked)
		}
		closeHarness(t, harness)
	})

	t.Run("let the answer stand without a continuation", func(t *testing.T) {
		setup, _ := hkSetup(t, "onYield", hkPlan{result: nil})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first")})
		harness, root := hkOpen(t, setup)
		settled := must(ownSubmit(t, root, ownInput("go")).Wait(testContext))
		hkExpectDone(t, settled)
		hkExpectTexts(t, root, "go", "first")
		if live := liveOf(t, harness, root.Id()); live != nil && live.Run != nil {
			t.Fatalf("run = %+v, want none", live.Run)
		}
		closeHarness(t, harness)
	})

	t.Run("lose their continuation to a user message queued meanwhile", func(t *testing.T) {
		gate := deferred()
		setup, _ := hkSetup(t, "onYield", hkPlan{result: map[string]any{"continue": "review: add a test"}, gate: gate})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first"), fauxAnswer("to you")})
		harness, root := hkOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		hkWaitYield(t, harness, root)
		queued := ownSubmit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("a question"), WhenBusy: durable.WhenBusyFollowUp})
		gate.resolve()
		hkExpectDone(t, must(submission.Wait(testContext)))
		hkExpectDone(t, must(queued.Wait(testContext)))
		hkExpectTexts(t, root, "go", "first", "a question", "to you")
		closeHarness(t, harness)
	})

	t.Run("settle the inputs done with the answer when the run is aborted while they decide", func(t *testing.T) {
		gate := deferred()
		setup, _ := hkSetup(t, "onYield", hkPlan{result: map[string]any{"continue": "never"}, gate: gate})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first")})
		harness, root := hkOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		hkWaitYield(t, harness, root)
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		hkExpectDone(t, must(submission.Wait(testContext)))
		live := liveOf(t, harness, root.Id())
		if live != nil && (live.Run != nil || live.RunFailure != nil) {
			t.Fatalf("run = %+v, runFailure = %+v, want neither", live.Run, live.RunFailure)
		}
		closeHarness(t, harness)
	})
}

// hkGo is the signal the parking decider sleeps on.
var hkGo = durable.DefineSignal(durable.SignalDefinition[map[string]any]{Name: "test.go", Version: 1, Schema: emptyObjectSchema()})

// hkParkingDecider is a task that parks on hkGo until a delivery, then completes with result.
func hkParkingDecider(result durable.JsonValue) stepTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    "test.parking-decider",
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "park"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"park": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "done"}, Signals: []durable.SignalKey{hkGo.Key("go")}}, nil
				})
			},
			"done": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](result), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

func TestHookTasksRecovery(t *testing.T) {
	t.Run("asks the afterTools hooks again after a crash before their tasks exist, not after", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		decider := hkParkingDecider(nil)
		gate := deferred()
		var asked atomic.Int32
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "deciders",
			Tools: []*durable.ToolRegistration{hkNoop()},
			Tasks: []durable.AnyTask{decider.Erased()},
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{AfterTools: func(ctx context.Context, _ durable.EntryId, _ []durable.EntryId, _ HookApi) (*HookTasks, error) {
				// The first process dies while its hook runs.
				if asked.Add(1) == 1 {
					if err := arWait(gate, ctx); err != nil {
						return nil, err
					}
				}
				return &HookTasks{Tasks: []HookTask{HookTaskOf(decider, durable.JsonValue(nil))}}, nil
			}})},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{hkCall(), fauxAnswer("done")})
		crashed := arOpenKillable(t, path, setup)
		crashed.harness.Resume()
		id := ownSubmit(t, crashed.root, ownInput("go")).Id()
		waitFor(t, func() bool { return asked.Load() == 1 })
		crashed.kill()
		next := arOpenKillable(t, path, setup)
		next.harness.Resume()
		// Asked again: nothing was created before the crash.
		waitFor(t, func() bool { return asked.Load() == 2 })
		var deciderId durable.TaskId
		waitFor(t, func() bool {
			for _, task := range must(next.harness.Inspect(testContext)).Tasks {
				if task.Record.Kind == decider.AnyDefinition().Name {
					deciderId = task.Record.Id
				}
			}
			if deciderId == 0 {
				return false
			}
			record := must(next.harness.GetTask(testContext, deciderId))
			return record != nil && record.State.Status == durable.TaskSleeping
		})
		// With the task waiting, the next process resumes the wait without asking.
		closeHarness(t, next.harness)
		third := arOpenKillable(t, path, setup)
		third.harness.Resume()
		if result := must(Signal(testContext, third.harness, deciderId, hkGo, "go", map[string]any{})); result != durable.SignalDelivered {
			t.Fatalf("signal result = %s, want delivered", result)
		}
		hkExpectDone(t, must(must(third.harness.Submission(testContext, id)).Wait(testContext)))
		if got := asked.Load(); got != 2 {
			t.Fatalf("asked %d times, want 2", got)
		}
		closeHarness(t, third.harness)
		gate.resolve()
	})

	t.Run("settles a yield's inputs done with its answer at the restart limit, without a runFailure", func(t *testing.T) {
		path := arSqlitePath(t)
		setup := chatSetup(t)
		decider := hkParkingDecider(map[string]any{"continue": "never"})
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "deciders",
			Tasks: []durable.AnyTask{decider.Erased()},
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (YieldResult, error) {
				return &YieldTasks{Tasks: []HookTask{HookTaskOf(decider, durable.JsonValue(nil))}}, nil
			}})},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first")})
		opened := arOpenKillable(t, path, setup)
		opened.harness.Resume()
		id := ownSubmit(t, opened.root, ownInput("go")).Id()
		hkWaitYield(t, opened.harness, opened.root)
		live := liveOf(t, opened.harness, opened.root.Id())
		answer, generation := live.Run.Yield.Answer, live.Run.TaskId
		closeHarness(t, opened.harness)
		// Cut off in `yield` as often as the limit allows: as a dead process leaves it, `running` with its count.
		store := openSqlite(t, path)
		t.Cleanup(func() { _ = store.Close(testContext) })
		record := must(store.Task(testContext, generation))
		running := *record
		running.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: record.State.Checkpoint}
		running.Restarts = 5
		if _, err := durabletest.CommitWrites(testContext, store, []durable.StorageWrite{durable.TaskWrite{Value: running}}); err != nil {
			t.Fatal(err)
		}
		harness, root := openChat(t, store, setup)
		harness.Resume()
		settled := must(must(harness.Submission(testContext, id)).Wait(testContext))
		hkExpectAnswer(t, settled, answer)
		after := liveOf(t, harness, root.Id())
		if after != nil && (after.Run != nil || after.RunFailure != nil) {
			t.Fatalf("run = %+v, runFailure = %+v, want neither", after.Run, after.RunFailure)
		}
		closeHarness(t, harness)
	})

	t.Run("settles a yield's inputs done with its answer when the scheduler ends the generation", func(t *testing.T) {
		setup := chatSetup(t)
		decider := hkParkingDecider(map[string]any{"continue": "never"})
		mustInstall(t, setup.Registry, new(durable.Extension{
			Name:  "deciders",
			Tasks: []durable.AnyTask{decider.Erased()},
			Hooks: []durable.HookRegistration{Hook(GenerationTask, &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (YieldResult, error) {
				return &YieldTasks{Tasks: []HookTask{HookTaskOf(decider, durable.JsonValue(nil))}}, nil
			}})},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first")})
		harness, root := hkOpen(t, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		hkWaitYield(t, harness, root)
		live := liveOf(t, harness, root.Id())
		generation := must(harness.GetTask(testContext, live.Run.TaskId))
		// The Harness cleanup the scheduler stages with a `faulted` outcome it writes.
		outcome := durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFaulted, Error: &durable.TaskOutcomeError{Message: "broken"}}
		if _, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
			return nil, SettleSchedulerOutcome(tx, *generation, outcome)
		}); err != nil {
			t.Fatal(err)
		}
		settled := must(submission.Wait(testContext))
		hkExpectAnswer(t, settled, live.Run.Yield.Answer)
		if after := liveOf(t, harness, root.Id()); after != nil && after.RunFailure != nil {
			t.Fatalf("runFailure = %+v, want none", after.RunFailure)
		}
		closeHarness(t, harness)
	})
}
