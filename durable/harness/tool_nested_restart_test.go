package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 1.2.0 test/harness-nested-tools-restart.test.ts: nested tool calls across a restart.

// nestedTasksOf are the nested pi.tool tasks of the Harness, in ID order.
func nestedTasksOf(t *testing.T, harness Harness) []anyTaskRecord {
	t.Helper()
	var nested []anyTaskRecord
	for _, task := range tlToolTasks(tlScanTasks(t, harness, durable.ROOT_CONVERSATION_ID)) {
		if input, err := json.Marshal(task.Input); err == nil && strings.Contains(string(input), `"kind":"nested"`) {
			nested = append(nested, task)
		}
	}
	return nested
}

// toolEnds collects the tool_execution_end events of a conversation from now on.
type toolEnds struct {
	mu   sync.Mutex
	ends []ToolExecutionEndEvent
	stop func()
}

func collectToolEnds(t *testing.T, harness Harness, conversationId durable.ConversationId) *toolEnds {
	t.Helper()
	collected := &toolEnds{}
	stream := must(WatchEvents(testContext, harness, conversationId))
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		collected.mu.Lock()
		defer collected.mu.Unlock()
		for _, event := range batch {
			if end, ok := event.(ToolExecutionEndEvent); ok {
				collected.ends = append(collected.ends, end)
			}
		}
		return nil
	})
	collected.stop = func() {
		if _, err := stream.Stop(); err != nil {
			t.Error(err)
		}
	}
	return collected
}

func (collected *toolEnds) result(callId string) *durable.NestedToolExecutionResult {
	collected.mu.Lock()
	defer collected.mu.Unlock()
	for _, end := range collected.ends {
		if end.ToolCallId == callId {
			return end.Result
		}
	}
	return nil
}

// harness-nested-tools-restart.test.ts:93 it("abandons a replay-unsafe caller's unfinished nested calls before they run again"):
// a replay-unsafe caller (the default) that never resumed after a restart has its
// unfinished nested calls abandoned before they run again; a call stopped before its intent, and one stopped after it,
// settle aborted with the codes the caller reads.
func TestAReplayUnsafeCallersUnfinishedNestedCallsAreAbandonedBeforeTheyRunAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	var mu sync.Mutex
	runs := map[string]int{}
	running, gated := deferred(), deferred()
	// Replay-safe, so only the abandonment keeps it from rerunning.
	addTool(t, setup.Registry, tlTool("started", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		mu.Lock()
		runs["started"]++
		mu.Unlock()
		api.Output("partial\n")
		if err := api.Details(ctx, map[string]any{"step": 1.0}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		running.resolve()
		return durable.ToolExecutionResult{}, abortedBy(ctx)
	}, func(registration *durable.ToolRegistration) { registration.Replay = durable.ReplaySafe }))
	addTool(t, setup.Registry, tlTool("gated", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		mu.Lock()
		runs["gated"]++
		mu.Unlock()
		return durable.ToolExecutionResult{}, nil
	}))
	// Holds `gated` in its call phase, before intent, until the Harness closes.
	addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{BeforeTool: func(ctx context.Context, call ToolHookCall, _ HookApi) (*BeforeToolResult, error) {
		if call.Name != "gated" {
			return nil, nil
		}
		gated.resolve()
		return nil, abortedBy(ctx)
	}})
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		// Promise.all of both calls.
		promiseAll(
			func() { _, _ = api.ExecuteTool(ctx, "started", map[string]any{}) },
			func() { _, _ = api.ExecuteTool(ctx, "gated", map[string]any{}) },
		)
		return durable.ToolExecutionResult{}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	for _, gate := range []*deferredGate{running, gated} {
		if err := gate.wait(testContext); err != nil {
			t.Fatal(err)
		}
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}

	harness, root = openChat(t, openSqlite(t, path), setup)
	ends := collectToolEnds(t, harness, root.Id())
	harness.Resume()
	sub := must(harness.Submission(testContext, id))
	if settled := must(sub.Wait(testContext)); settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %s, want done", settled.Status)
	}
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	ends.stop()
	mu.Lock()
	gotRuns := map[string]int{"started": runs["started"], "gated": runs["gated"]}
	mu.Unlock()
	if gotRuns["started"] != 1 || gotRuns["gated"] != 0 {
		t.Fatalf("runs %v, want started 1 and gated 0", gotRuns)
	}
	nested := nestedTasksOf(t, harness)
	if len(nested) != 2 {
		t.Fatalf("%d nested tasks, want 2", len(nested))
	}
	for _, task := range nested {
		if !task.AbandonOnRestart || !task.AbortRequested || task.AbortReason != durable.AbortedByRestart ||
			task.State.Status != durable.TaskTerminal || task.State.Outcome == nil || task.State.Outcome.Status != durable.OutcomeAborted {
			t.Fatalf("nested task %d = %+v, want abandoned on restart, abort requested for it, terminal aborted", task.Id, task)
		}
	}
	started, gate := ends.result("c1/1"), ends.result("c1/2")
	if started == nil || gate == nil {
		t.Fatalf("end results c1/1 %v, c1/2 %v, want both", started, gate)
	}
	if details, _ := json.Marshal(started.Details); !slices.Equal(diagnosticCodes(*started), []string{"interrupted"}) || !started.IsError || started.HasStructuredOutput ||
		!started.HasDetails || string(details) != `{"step":1}` {
		t.Fatalf("c1/1 end result = %+v, want an interrupted error that keeps its details and no structured output", started)
	}
	if codes := diagnosticCodes(*gate); !slices.Equal(codes, []string{"abandoned"}) {
		t.Fatalf("c1/2 codes = %v, want abandoned", codes)
	}
	// The caller reports the interruption.
	results := tlResults(allEntries(t, root))
	if len(results) != 1 || results[0].ToolCallID != "c1" || !results[0].IsError {
		t.Fatalf("results %+v, want one error result for c1", results)
	}
	expectLiveEmpty(t, harness, root.Id())
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// harness-nested-tools-restart.test.ts:363 it("marks nothing when the reopened Harness never starts scheduling"): a reopened
// Harness that never starts scheduling marks nothing, so an inspection-only
// open does not abandon a caller's nested call.
func TestAReopenedHarnessThatNeverStartsSchedulingMarksNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	running := deferred()
	addTool(t, setup.Registry, tlTool("hang", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		running.resolve()
		return durable.ToolExecutionResult{}, abortedBy(ctx)
	}))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		_, err := api.ExecuteTool(ctx, "hang", map[string]any{})
		return durable.ToolExecutionResult{}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	ownSubmit(t, root, ownInput("go"))
	if err := running.wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}

	// An inspection-only open: no Resume, no submission.
	harness, _ = openChat(t, openSqlite(t, path), setup)
	if inspected := must(harness.Inspect(testContext)); inspected.Scheduling != "paused" {
		t.Fatalf("scheduling %q, want paused", inspected.Scheduling)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}

	harness, _ = openChat(t, openSqlite(t, path), setup)
	nested := nestedTasksOf(t, harness)
	if len(nested) != 1 || !nested[0].AbandonOnRestart || nested[0].AbortRequested || nested[0].State.Status != durable.TaskPending {
		t.Fatalf("nested tasks %+v, want one abandonOnRestart task, no abort requested, pending", nested)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// harness-nested-tools-restart.test.ts:400 it("lets a replay-safe caller's unfinished nested calls recover and reattaches to them
// by key"): a replay-safe caller's unfinished nested calls recover with it, and its rerun reattaches to them by their default
// keys: a replay-safe nested call runs again, a replay-unsafe one settles interrupted.
func TestAReplaySafeCallersUnfinishedNestedCallsRecoverAndTheRerunReattachesByKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	var mu sync.Mutex
	runs := map[string]int{}
	count := func(name string) int {
		mu.Lock()
		defer mu.Unlock()
		runs[name]++
		return runs[name]
	}
	safeRunning, unsafeRunning := deferred(), deferred()
	addTool(t, setup.Registry, tlTool("safe", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if n := count("safe"); n == 1 {
			safeRunning.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}
		mu.Lock()
		n := runs["safe"]
		mu.Unlock()
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("safe run %d", n)}}}, nil
	}, func(registration *durable.ToolRegistration) { registration.Replay = durable.ReplaySafe }))
	addTool(t, setup.Registry, tlTool("unsafe", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		count("unsafe")
		unsafeRunning.resolve()
		return durable.ToolExecutionResult{}, abortedBy(ctx)
	}))
	var batchResults [2]durable.NestedToolExecutionResult
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		count("caller")
		// Promise.all of both calls.
		promiseAll(
			func() { batchResults[0], _ = api.ExecuteTool(ctx, "safe", map[string]any{}) },
			func() { batchResults[1], _ = api.ExecuteTool(ctx, "unsafe", map[string]any{}) },
		)
		return durable.ToolExecutionResult{}, nil
	}, func(registration *durable.ToolRegistration) { registration.Replay = durable.ReplaySafe }))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	for _, gate := range []*deferredGate{safeRunning, unsafeRunning} {
		if err := gate.wait(testContext); err != nil {
			t.Fatal(err)
		}
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}

	harness, _ = openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	// A rerun that does not find its calls by key creates new ones and never settles: bound the wait.
	waiting, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	if settled, err := must(harness.Submission(testContext, id)).Wait(waiting); err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %+v, %v, want done", settled, err)
	}
	mu.Lock()
	got := map[string]int{"safe": runs["safe"], "unsafe": runs["unsafe"], "caller": runs["caller"]}
	mu.Unlock()
	if got["safe"] != 2 || got["unsafe"] != 1 || got["caller"] != 2 {
		t.Fatalf("runs %v, want safe 2, unsafe 1, caller 2", got)
	}
	nested := nestedTasksOf(t, harness)
	if len(nested) != 2 {
		t.Fatalf("%d nested tasks, want 2: the rerun found both calls by key", len(nested))
	}
	for _, task := range nested {
		if task.AbandonOnRestart || task.AbortReason != "" {
			t.Fatalf("nested task %d = %+v, want neither abandonOnRestart nor an abort reason (the caller is replay-safe)", task.Id, task)
		}
	}
	if batchResults[0].StructuredOutput != "safe run 2" || !slices.Equal(diagnosticCodes(batchResults[1]), []string{"interrupted"}) {
		t.Fatalf("results %+v, want \"safe run 2\" and an interrupted unsafe call", batchResults)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// harness-nested-tools-restart.test.ts:563 it("reattaches a replay-safe caller by default and explicit keys, and rejects a rerun
// that calls differently"): a replay-safe caller reattaches to its nested calls by default and explicit keys after
// a restart, and a rerun that calls differently throws instead of reusing another call's result.
func TestAReplaySafeCallerReattachesByDefaultAndExplicitKeysAndARerunThatCallsDifferentlyIsRejected(t *testing.T) {
	for _, order := range []string{"same", "swapped"} {
		t.Run(order, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.sqlite")
			setup := chatSetup(t)
			var mu sync.Mutex
			var runs []string
			addTool(t, setup.Registry, tlTool("echo", func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				text, _ := args.(map[string]any)["text"].(string)
				mu.Lock()
				runs = append(runs, text)
				mu.Unlock()
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo " + text}}}, nil
			}))
			blocked := deferred()
			callerRuns := 0
			var seen []any
			addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				callerRuns++
				first := "a"
				if callerRuns == 2 && order == "swapped" {
					first = "b"
				}
				one, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": first})
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				named, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": "named"}, durable.NestedCallOptions{Key: new("named")})
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				if callerRuns == 1 {
					blocked.resolve()
					return durable.ToolExecutionResult{}, abortedBy(ctx)
				}
				seen = []any{one.StructuredOutput, named.StructuredOutput}
				return durable.ToolExecutionResult{}, nil
			}, func(registration *durable.ToolRegistration) { registration.Replay = durable.ReplaySafe }))
			setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
			harness, root := openChat(t, openSqlite(t, path), setup)
			harness.Resume()
			id := ownSubmit(t, root, ownInput("go")).Id()
			if err := blocked.wait(testContext); err != nil {
				t.Fatal(err)
			}
			if err := harness.Close(testContext); err != nil {
				t.Fatal(err)
			}

			harness, root = openChat(t, openSqlite(t, path), setup)
			harness.Resume()
			waiting, cancel := context.WithTimeout(testContext, 30*time.Second)
			defer cancel()
			if settled, err := must(harness.Submission(testContext, id)).Wait(waiting); err != nil || settled.Status != durable.SubmissionDone {
				t.Fatalf("submission %+v, %v, want done", settled, err)
			}
			results := tlResults(allEntries(t, root))
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(runs, []string{"a", "named"}) || len(results) != 1 {
				t.Fatalf("runs %v, results %+v, want a and named run once each and one result", runs, results)
			}
			if order == "same" {
				if !reflect.DeepEqual(seen, []any{"echo a", "echo named"}) || results[0].IsError {
					t.Fatalf("seen %v, result %+v, want both results and no error", seen, results[0])
				}
				return
			}
			// Key 1 was made with other arguments: the rerun throws instead of reusing a different call's result.
			if !results[0].IsError || !strings.Contains(tlResultText(results[0]), "Nested call c1/1 was already made with another tool") {
				t.Fatalf("result %+v, want an error that call c1/1 was made with another tool", results[0])
			}
		})
	}
}

// harness-nested-tools-restart.test.ts:621 it("abandons the child tasks a replay-unsafe tool created and awaited in memory"): a
// replay-unsafe tool that created a child task and awaited it in memory has the child
// abandoned after a restart: its work phase never runs again and its abort handler reads the restart reason.
func TestTheChildTasksAReplayUnsafeToolCreatedAndAwaitedInMemoryAreAbandoned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	var mu sync.Mutex
	var phases []string
	record := func(phase string) {
		mu.Lock()
		defer mu.Unlock()
		phases = append(phases, phase)
	}
	type none = struct{}
	started := deferred()
	worker := durable.DefineTask(durable.TaskDefinition[none, stepState, durable.JsonValue, any]{
		Name:    "test.direct-worker",
		Version: 1,
		Initial: func(none) stepState { return stepState{Phase: "work"} },
		Phases: map[string]durable.PhaseHandler[none, stepState, durable.JsonValue, any]{
			"work": func(_ context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
				record("work")
				started.resolve()
				return abortedBy(runtime.Signal())
			},
		},
		Abort: func(ctx context.Context, task durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
			record("abort " + string(task.AbortReason))
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[none, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue](""), nil
			})
		},
	})
	addTask(t, setup.Registry, worker.Erased())
	addTool(t, setup.Registry, tlTool("work", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		id, err := api.CreateTaskErased(ctx, worker.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: api.TaskId()}})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		_, err = api.WaitForTask(ctx, id)
		return durable.ToolExecutionResult{}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"work", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	if err := started.wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}

	harness, _ = openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	waiting, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	if settled, err := must(harness.Submission(testContext, id)).Wait(waiting); err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %+v, %v, want done", settled, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(phases, []string{"work", "abort restart"}) {
		t.Fatalf("phases %v, want work once, then the abort handler with the restart reason", phases)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// The order the test above leaves to the goroutines, forced: the abandoned child's abort invocation is reserved with the
// restart mark, and the replay-unsafe parent's failed settle cascades a request onto the child before the invocation's
// goroutine runs. Upstream's #runAbort reads the record in the drain that reserved it, so the handler still reads restart.
func TestAnAbandonedChildsAbortHandlerReadsTheRestartReasonItWasReservedWith(t *testing.T) {
	var once sync.Once
	beforeInvocation = func(s *TaskScheduler, invocation *invocation) {
		if invocation.mode != modeAbort {
			return
		}
		once.Do(func() {
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
				s.mu.Lock()
				record, live := s.live[invocation.taskId]
				s.mu.Unlock()
				if !live || (record.AbortRequested && record.AbortReason == "") {
					return
				}
			}
			t.Error("the failed parent never cascaded a request onto the abandoned child")
		})
	}
	t.Cleanup(func() { beforeInvocation = nil })
	TestTheChildTasksAReplayUnsafeToolCreatedAndAwaitedInMemoryAreAbandoned(t)
}

// harness-nested-tools-restart.test.ts:669 it("leaves background work under an abandoned nested call running, and settles without
// it"): background work in a conversation an abandoned nested call owned keeps running, and the
// run settles without it.
func TestBackgroundWorkUnderAnAbandonedNestedCallKeepsRunningAndTheRunSettlesWithoutIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	type none = struct{}
	started := deferred()
	background := durable.DefineTask(durable.TaskDefinition[none, stepState, durable.JsonValue, any]{
		Name:    "test.background",
		Version: 1,
		Initial: func(none) stepState { return stepState{Phase: "work"} },
		Phases: map[string]durable.PhaseHandler[none, stepState, durable.JsonValue, any]{
			"work": func(_ context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
				started.resolve()
				return abortedBy(runtime.Signal())
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[none, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue](""), nil
			})
		},
	})
	addTask(t, setup.Registry, background.Erased())
	var mu sync.Mutex
	var backgroundId durable.TaskId
	addTool(t, setup.Registry, tlTool("spawn", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		// A conversation the call owns, with background work in it, as a persistent subagent has.
		created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
			if err != nil {
				return nil, err
			}
			return durable.CreateTask(tx, background, none{}, durable.TaskOptions{ConversationId: &child.Id, Ownership: conversationOwned, Background: true})
		})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		mu.Lock()
		backgroundId = created.(durable.TaskId)
		mu.Unlock()
		return durable.ToolExecutionResult{}, abortedBy(ctx)
	}))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		_, err := api.ExecuteTool(ctx, "spawn", map[string]any{})
		return durable.ToolExecutionResult{}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	if err := started.wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}

	harness, _ = openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	waiting, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	if settled, err := must(harness.Submission(testContext, id)).Wait(waiting); err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %+v, %v, want done", settled, err)
	}
	nested := nestedTasksOf(t, harness)
	if len(nested) != 1 || nested[0].AbortReason != durable.AbortedByRestart || nested[0].State.Status != durable.TaskTerminal {
		t.Fatalf("nested tasks %+v, want the spawn call abandoned by a restart and terminal", nested)
	}
	mu.Lock()
	taskId := backgroundId
	mu.Unlock()
	record := must(harness.GetTask(testContext, taskId))
	if record == nil || record.AbortRequested || record.State.Status != durable.TaskRunning {
		t.Fatalf("background task %+v, want running with no abort requested", record)
	}
	if _, err := harness.AbortTask(testContext, taskId); err != nil {
		t.Fatal(err)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// restartWorker is a worker task a nested call spawns and awaits (harness-nested-tools-restart.test.ts :140, :230, :300): it
// holds its work phase until aborted and records the reason its abort handler reads.
type restartWorker struct {
	extension *durable.Extension
	started   *deferredGate
	mu        sync.Mutex
	phases    []string
	aborts    []durable.TaskAbortReason
	id        durable.TaskId
}

func newRestartWorker(t *testing.T, setup *chatState, name string) *restartWorker {
	t.Helper()
	type none = struct{}
	worker := &restartWorker{started: deferred()}
	task := durable.DefineTask(durable.TaskDefinition[none, stepState, durable.JsonValue, any]{
		Name:    name,
		Version: 1,
		Initial: func(none) stepState { return stepState{Phase: "work"} },
		Phases: map[string]durable.PhaseHandler[none, stepState, durable.JsonValue, any]{
			"work": func(_ context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
				worker.mu.Lock()
				worker.phases = append(worker.phases, "work")
				worker.mu.Unlock()
				worker.started.resolve()
				return abortedBy(runtime.Signal())
			},
		},
		Abort: func(ctx context.Context, record durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
			worker.mu.Lock()
			worker.aborts = append(worker.aborts, record.AbortReason)
			worker.mu.Unlock()
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[none, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue](""), nil
			})
		},
	})
	worker.extension = &durable.Extension{Name: name, Tasks: []durable.AnyTask{task.Erased()}}
	if err := setup.Registry.Install(worker.extension); err != nil {
		t.Fatal(err)
	}
	addTool(t, setup.Registry, tlTool("spawn", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		id, err := api.CreateTaskErased(ctx, task.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: api.TaskId()}})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		worker.mu.Lock()
		worker.id = id
		worker.mu.Unlock()
		_, err = api.WaitForTask(ctx, id)
		return durable.ToolExecutionResult{}, err
	}))
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		_, err := api.ExecuteTool(ctx, "spawn", map[string]any{})
		return durable.ToolExecutionResult{}, err
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	return worker
}

func (worker *restartWorker) taskId() durable.TaskId {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.id
}

// runUntilStarted opens a Harness at path, submits, waits for the worker to start and closes it, returning the submission ID.
func (worker *restartWorker) runUntilStarted(t *testing.T, setup *chatState, path string) durable.SubmissionId {
	t.Helper()
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	if err := worker.started.wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
	return id
}

func waitSubmissionDone(t *testing.T, harness Harness, id durable.SubmissionId) {
	t.Helper()
	waiting, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	if settled, err := must(harness.Submission(testContext, id)).Wait(waiting); err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %+v, %v, want done", settled, err)
	}
}

func expectTerminalAborted(t *testing.T, harness Harness, id durable.TaskId) {
	t.Helper()
	record := must(harness.GetTask(testContext, id))
	if record == nil || record.State.Status != durable.TaskTerminal || record.State.Outcome == nil || record.State.Outcome.Status != durable.OutcomeAborted {
		t.Fatalf("task %d = %+v, want terminal aborted", id, record)
	}
}

// harness-nested-tools-restart.test.ts:168 it("abandons the work an abandoned nested call owns, which never runs a phase again"):
// the work an abandoned nested call owns is abandoned too and never runs a phase again;
// the cascade passes the restart reason on to the abort handler.
func TestTheWorkAnAbandonedNestedCallOwnsIsAbandonedAndNeverRunsAPhaseAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	worker := newRestartWorker(t, setup, "test.worker")
	id := worker.runUntilStarted(t, setup, path)

	harness, _ := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	waitSubmissionDone(t, harness, id)
	worker.mu.Lock()
	phases, aborts := slices.Clone(worker.phases), slices.Clone(worker.aborts)
	worker.mu.Unlock()
	if !slices.Equal(phases, []string{"work"}) || !slices.Equal(aborts, []durable.TaskAbortReason{durable.AbortedByRestart}) {
		t.Fatalf("phases %v, aborts %v, want one work phase and one abort with the restart reason", phases, aborts)
	}
	expectTerminalAborted(t, harness, worker.taskId())
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// harness-nested-tools-restart.test.ts:232 it("keeps an abandoned task whose definition is missing waiting until it is installed,
// then runs its abort"): an abandoned task whose definition is missing keeps waiting until the definition is
// installed, then runs its abort.
func TestAnAbandonedTaskWhoseDefinitionIsMissingWaitsUntilItIsInstalledThenRunsItsAbort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	worker := newRestartWorker(t, setup, "test.late")
	id := worker.runUntilStarted(t, setup, path)

	setup.Registry.Uninstall(worker.extension)
	harness, _ := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	eventually(t, func() bool {
		for _, task := range must(harness.Inspect(testContext)).Tasks {
			if task.Record.Id == worker.taskId() {
				return task.State.Kind == TaskInspectionBlocked
			}
		}
		return false
	})
	if record := must(harness.GetTask(testContext, worker.taskId())); record == nil || record.State.Status == durable.TaskTerminal {
		t.Fatalf("task %+v, want it kept waiting while its definition is missing", record)
	}
	worker.mu.Lock()
	early := len(worker.aborts)
	worker.mu.Unlock()
	if early != 0 {
		t.Fatalf("%d aborts before the definition is installed, want none", early)
	}
	if err := setup.Registry.Install(worker.extension); err != nil {
		t.Fatal(err)
	}
	waitSubmissionDone(t, harness, id)
	worker.mu.Lock()
	aborts := slices.Clone(worker.aborts)
	worker.mu.Unlock()
	if !slices.Equal(aborts, []durable.TaskAbortReason{durable.AbortedByRestart}) {
		t.Fatalf("aborts %v, want the restart reason once", aborts)
	}
	expectTerminalAborted(t, harness, worker.taskId())
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// harness-nested-tools-restart.test.ts:300 it("orphans an abandoned task whose definition is missing once an abort is requested")
func TestAnAbandonedTaskWhoseDefinitionIsMissingIsOrphanedOnceAnAbortIsRequested(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	worker := newRestartWorker(t, setup, "test.gone")
	id := worker.runUntilStarted(t, setup, path)

	setup.Registry.Uninstall(worker.extension)
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	eventually(t, func() bool {
		record := must(harness.GetTask(testContext, worker.taskId()))
		return record != nil && record.AbortReason == durable.AbortedByRestart
	})
	if err := root.Abort(testContext, nil); err != nil {
		t.Fatal(err)
	}
	record := must(harness.GetTask(testContext, worker.taskId()))
	if record == nil || record.State.Status != durable.TaskTerminal || record.State.Outcome == nil || record.State.Outcome.Status != durable.OutcomeOrphaned ||
		record.State.Outcome.Reason == nil || *record.State.Outcome.Reason != "missing_task" {
		t.Fatalf("task %+v, want terminal orphaned with reason missing_task", record)
	}
	if settled := must(must(harness.Submission(testContext, id)).Wait(testContext)); settled.Status != durable.SubmissionUnanswered {
		t.Fatalf("submission %s, want unanswered", settled.Status)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// harness-nested-tools-restart.test.ts:465 it("clears the slots of nested calls an earlier attempt finished when the rerun makes
// none"): the slots of nested calls an earlier attempt finished are cleared by the
// caller's terminal commit even when the rerun makes none (the memo marks a tool that made a nested call in any attempt).
func TestTheSlotsOfNestedCallsAnEarlierAttemptFinishedAreClearedWhenTheRerunMakesNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	finished := deferred()
	attempts := 0
	addTool(t, setup.Registry, tlTool("quick", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
	}))
	addTool(t, setup.Registry, tlTool("caller", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		attempts++
		if attempts == 1 {
			if _, err := api.ExecuteTool(ctx, "quick", map[string]any{}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			finished.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}, nil
	}, func(registration *durable.ToolRegistration) { registration.Replay = durable.ReplaySafe }))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"caller", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	if err := finished.wait(testContext); err != nil {
		t.Fatal(err)
	}
	// The finished nested call's slot is still listed, below its running caller.
	if live := must(durable.Snapshot(testContext, harness, LiveDoc, root.Id())); len(live.NestedTools) != 1 {
		t.Fatalf("nested slots %+v, want the finished call's slot", live.NestedTools)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}

	harness, _ = openChat(t, openSqlite(t, path), setup)
	// The nested slots pi.live holds in the commit where the model-issued call's task ends.
	slotsAfterCaller := -1
	var slotsMu sync.Mutex
	harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		ended := false
		slots := 0
		for _, change := range publication.Changes {
			switch typed := change.(type) {
			case durable.TaskWrite:
				input, err := durable.FromJsonValue[ToolTaskInput](typed.Value.Input)
				ended = ended || typed.Value.Kind == "pi.tool" && err == nil && input.Kind == ToolTaskModel && typed.Value.State.Status == durable.TaskTerminal
			case durable.DocumentChange:
				if typed.Record.Kind == "pi.live" && typed.Value != nil {
					if list, ok := typed.Value.Value("nestedTools").([]any); ok {
						slots = len(list)
					}
				}
			}
		}
		if ended {
			slotsMu.Lock()
			slotsAfterCaller = slots
			slotsMu.Unlock()
		}
	})
	harness.Resume()
	waiting, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	if settled, err := must(harness.Submission(testContext, id)).Wait(waiting); err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %+v, %v, want done", settled, err)
	}
	if attempts != 2 {
		t.Fatalf("attempts %d, want 2", attempts)
	}
	// The caller's terminal commit removed the slot, though the rerun made no nested call.
	slotsMu.Lock()
	defer slotsMu.Unlock()
	if slotsAfterCaller != 0 {
		t.Fatalf("nested slots in the caller's terminal commit = %d, want 0", slotsAfterCaller)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

// harness-nested-tools-restart.test.ts:518 it("aborts a nested call an earlier attempt of a replay-safe caller left running and the
// rerun never made again"): a nested call an earlier attempt of a replay-safe caller left running, and
// the rerun never made again, is found among the tasks the caller owns and aborted when the rerun settles.
func TestANestedCallAnEarlierAttemptLeftRunningAndTheRerunNeverMadeAgainIsAborted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	setup := chatSetup(t)
	running := deferred()
	attempts := 0
	addTool(t, setup.Registry, tlTool("slow", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		running.resolve()
		return durable.ToolExecutionResult{}, abortedBy(ctx)
	}, func(registration *durable.ToolRegistration) { registration.Replay = durable.ReplaySafe }))
	addTool(t, setup.Registry, tlTool("caller", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		attempts++
		// Only the first attempt calls `slow`; the rerun after the restart returns at once.
		if attempts == 1 {
			if _, err := api.ExecuteTool(ctx, "slow", map[string]any{}, durable.NestedCallOptions{Key: new("slow")}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
		}
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}, nil
	}, func(registration *durable.ToolRegistration) { registration.Replay = durable.ReplaySafe }))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"caller", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	id := ownSubmit(t, root, ownInput("go")).Id()
	if err := running.wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}

	harness, _ = openChat(t, openSqlite(t, path), setup)
	harness.Resume()
	waiting, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	if settled, err := must(harness.Submission(testContext, id)).Wait(waiting); err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %+v, %v, want done", settled, err)
	}
	if attempts != 2 {
		t.Fatalf("attempts %d, want 2", attempts)
	}
	// The rerun's settlement found the leftover call among the tasks it owns and aborted it.
	nested := nestedTasksOf(t, harness)
	if len(nested) != 1 || nested[0].State.Status != durable.TaskTerminal || nested[0].State.Outcome == nil || nested[0].State.Outcome.Status != durable.OutcomeAborted {
		t.Fatalf("nested tasks %+v, want one terminal aborted", nested)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}
