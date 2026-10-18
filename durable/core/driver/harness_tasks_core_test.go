// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Custom tasks run on the native Harness through the phase runtime (driver/taskruntime.go), the counterpart of
// harness-tasks.test.ts through driver.OpenHarness over the real core.

type (
	stepRecord  = durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue]
	stepRuntime = durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, any]
	stepNext    = durable.NextTaskState[stepState, durable.JsonValue]
)

func completedWith(result durable.JsonValue) *stepNext {
	return &stepNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}
}

func running(phase string) *stepNext {
	return &stepNext{Status: durable.TaskRunning, Checkpoint: &stepState{Phase: phase}}
}

func defineSteps(name string, abort func(context.Context, stepRuntime) error, phases map[string]func(context.Context, stepRecord, stepRuntime) error) durable.Task[durable.JsonValue, stepState, durable.JsonValue, any] {
	handlers := map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{}
	for phase, run := range phases {
		handlers[phase] = func(ctx context.Context, task stepRecord, runtime stepRuntime) error { return run(ctx, task, runtime) }
	}
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name: name, Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "start"} },
		Phases:  handlers,
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			if abort == nil {
				return nil
			}
			return abort(ctx, runtime)
		},
	})
}

func openTasksHarness(t *testing.T, tasks ...durable.AnyTask) (harness.Harness, harness.Conversation) {
	t.Helper()
	h, _, root := openTasksRegistry(t, tasks...)
	return h, root
}

// openTasksRegistry also returns the registry, which a test keeps changing while the Harness runs.
func openTasksRegistry(t *testing.T, tasks ...durable.AnyTask) (harness.Harness, harness.Registry, harness.Conversation) {
	t.Helper()
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: tasks}); err != nil {
		t.Fatal(err)
	}
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return h, registry, root
}

func startTask(t *testing.T, conversation harness.Conversation, task durable.AnyTask) durable.TaskId {
	t.Helper()
	id, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (durable.TaskId, error) {
		return tx.CreateTaskErased(task, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitSettled(t *testing.T, h harness.Harness, id durable.TaskId) durable.SettledTask[durable.JsonValue] {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	settled, err := h.WaitForTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

// harness-tasks.test.ts:86 "continues one invocation through checkpoint progress": the phases of one invocation share one runtime
// (one signal), every phase commits a checkpoint and the last completes the task with its result.
func TestCoreTaskRunsPhasesOfOneInvocationOnOneRuntime(t *testing.T) {
	var signals []context.Context
	var records []string
	task := defineSteps("test.phases", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, task stepRecord, rt stepRuntime) error {
			signals = append(signals, rt.Signal())
			records = append(records, task.Kind+":"+string(task.State.Status))
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return running("middle"), nil })
		},
		"middle": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			signals = append(signals, rt.Signal())
			if _, err := rt.MemoCandidate(ctx, "m", "memo"); err != nil {
				return err
			}
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return running("end"), nil })
		},
		"end": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			signals = append(signals, rt.Signal())
			memo, ok, err := rt.Memo(ctx, "m")
			if err != nil || !ok || memo != "memo" {
				return errors.New("memo lost")
			}
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("done"), nil })
		},
	})
	h, root := openTasksHarness(t, task.Erased())
	id := startTask(t, root, task.Erased())
	settled := waitSettled(t, h, id)
	if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeCompleted || settled.State.Outcome.Result == nil || *settled.State.Outcome.Result != "done" {
		t.Fatalf("settled = %+v (outcome %+v)", settled.State, settled.State.Outcome)
	}
	if len(signals) != 3 || signals[0] != signals[1] || signals[1] != signals[2] {
		t.Fatalf("the phases of one invocation share one runtime: %v", signals)
	}
	if len(records) != 1 || records[0] != "test.phases:running" {
		t.Fatalf("the first phase sees the reserved task: %v", records)
	}
}

// A task starts a child through its runtime commit and waits for it: waitForTask carries the waiting task as `by`.
func TestCoreTaskStartsAChildAndWaitsForItsOutcome(t *testing.T) {
	child := defineSteps("test.child", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("child-result"), nil })
		},
	})
	var seen string
	parent := defineSteps("test.parent", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			var childId durable.TaskId
			if err := rt.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*stepNext, error) {
				id, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
				childId = id
				return nil, err
			}); err != nil {
				return err
			}
			settled, err := rt.WaitForTask(ctx, childId)
			if err != nil {
				return err
			}
			outcomes, err := rt.Outcomes(ctx, []durable.TaskId{childId})
			if err != nil || len(outcomes) != 1 || outcomes[0].Result == nil {
				return errors.New("outcomes")
			}
			seen = settled.Kind + "=" + fmt.Sprint(*outcomes[0].Result)
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("parent-done"), nil })
		},
	})
	h, root := openTasksHarness(t, parent.Erased(), child.Erased())
	settled := waitSettled(t, h, startTask(t, root, parent.Erased()))
	if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("parent = %+v", settled.State.Outcome)
	}
	if seen != "test.child=child-result" {
		t.Fatalf("the parent saw %q", seen)
	}
}

// Aborting a task signals its running phase and runs its abort handler; a commit of the ended invocation is refused with
// 'Task N invocation has ended' (scheduler.ts: a Runtime fails once its invocation ended).
func TestCoreAbortingATaskSignalsItsPhaseAndRunsItsAbortHandler(t *testing.T) {
	reached := make(chan struct{})
	signalled := make(chan struct{})
	aborted := make(chan struct{}, 1)
	commitErr := make(chan error, 1)
	task := defineSteps("test.abortable", func(ctx context.Context, rt stepRuntime) error {
		aborted <- struct{}{}
		reason := "stopped"
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) {
			return &stepNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted, Reason: &reason}}, nil
		})
	}, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			close(reached)
			<-rt.Signal().Done()
			close(signalled)
			// The phase's own context is cancelled with the abort, and Commit refuses a cancelled context; a fresh one reaches the
			// invocation check.
			commitErr <- errors.Join(
				rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("late"), nil }),
				rt.Commit(context.Background(), func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("late"), nil }))
			return nil
		},
	})
	h, root := openTasksHarness(t, task.Erased())
	id := startTask(t, root, task.Erased())
	h.Resume()
	<-reached
	if _, err := h.AbortTask(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signalled:
	case <-time.After(15 * time.Second):
		t.Fatal("the running phase was never signalled")
	}
	select {
	case <-aborted:
	case <-time.After(15 * time.Second):
		t.Fatal("the abort handler never ran")
	}
	select {
	case err := <-commitErr:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("a commit on the cancelled phase context = %v", err)
		}
		if !strings.Contains(err.Error(), "abort") && !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("a commit after the abort = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no commit result")
	}
	settled := waitSettled(t, h, id)
	if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeAborted {
		t.Fatalf("outcome = %+v", settled.State.Outcome)
	}
}

func panicText(use func()) (text string) {
	defer func() {
		if r := recover(); r != nil {
			text = fmt.Sprint(r)
		}
	}()
	use()
	return ""
}

func oneStepTask(name string, run func(ctx context.Context, rt stepRuntime) error) durable.Task[durable.JsonValue, stepState, durable.JsonValue, any] {
	return defineSteps(name, nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error { return run(ctx, rt) },
	})
}

// harness-tasks.test.ts:120 "faults a phase without durable progress and a throwing phase".
func TestCoreTaskFaultsAPhaseWithoutDurableProgressAndAThrowingPhase(t *testing.T) {
	idle := oneStepTask("test.idle", func(context.Context, stepRuntime) error { return nil })
	documentOnly := oneStepTask("test.document-only", func(ctx context.Context, rt stepRuntime) error {
		// A commit that returns no state is not progress.
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return nil, nil })
	})
	throws := oneStepTask("test.throws", func(context.Context, stepRuntime) error { return errors.New("boom") })
	h, root := openTasksHarness(t, idle.Erased(), documentOnly.Erased(), throws.Erased())
	ids := []durable.TaskId{startTask(t, root, idle.Erased()), startTask(t, root, documentOnly.Erased()), startTask(t, root, throws.Erased())}
	want := []string{
		"Task test.idle phase start returned without durable progress",
		"Task test.document-only phase start returned without durable progress",
		"boom",
	}
	for i, id := range ids {
		outcome := waitSettled(t, h, id).State.Outcome
		if outcome == nil || outcome.Status != durable.OutcomeFaulted || outcome.Error == nil || outcome.Error.Message != want[i] {
			t.Fatalf("task %d outcome = %+v (error %+v), want faulted %q", i, outcome, outcome.Error, want[i])
		}
	}
}

// harness-tasks.test.ts:144 "keeps a committed terminal outcome when the handler throws afterwards".
func TestCoreTaskKeepsACommittedTerminalOutcomeWhenTheHandlerThrowsAfterwards(t *testing.T) {
	late := make(chan error, 1)
	done := oneStepTask("test.done", func(ctx context.Context, rt stepRuntime) error {
		if err := rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("ok"), nil }); err != nil {
			return err
		}
		late <- rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return nil, nil })
		return errors.New("after terminal")
	})
	h, root := openTasksHarness(t, done.Erased())
	id := startTask(t, root, done.Erased())
	outcome := waitSettled(t, h, id).State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeCompleted || outcome.Result == nil || *outcome.Result != "ok" {
		t.Fatalf("outcome = %+v", outcome)
	}
	select {
	case err := <-late:
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("Task %d is terminal", id)) {
			t.Fatalf("a commit after the terminal outcome = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no late commit result")
	}
}

// harness-tasks.test.ts:522 "rejects runtime operations after the invocation ends and stops its watches".
func TestCoreTaskRejectsRuntimeOperationsAfterTheInvocationEndsAndStopsItsWatches(t *testing.T) {
	notes := defineDoc("test.task-notes", 1, func() obj { return delta.JsonObjectOf("text", "") })
	absentDoc := defineDoc("test.task-absent", 1, func() obj { return delta.JsonObjectOf("text", "") })
	captured := make(chan stepRuntime, 1)
	var absent durable.WatchHandle[durable.JsonObject]
	var watch durable.WatchHandle[durable.JsonObject]
	var mu sync.Mutex
	var delivered []string
	watcher := oneStepTask("test.watcher", func(ctx context.Context, rt stepRuntime) error {
		captured <- rt
		var err error
		if absent, err = rt.WatchDocErased(ctx, absentDoc); err != nil {
			return err
		}
		if watch, err = rt.WatchDocErased(ctx, notes); err != nil {
			return err
		}
		watch.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
			mu.Lock()
			defer mu.Unlock()
			delivered = append(delivered, fmt.Sprint(value.Value("text")))
			return nil
		})
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
	})
	h, root := openTasksHarness(t, watcher.Erased())
	setText := func(text string) {
		if _, err := h.Commit(t.Context(), func(tx durable.Tx) (any, error) {
			o, err := tx.Doc(notes)
			if err != nil {
				return nil, err
			}
			return nil, o.Set("text", text)
		}); err != nil {
			t.Fatal(err)
		}
	}
	setText("hello")
	waitSettled(t, h, startTask(t, root, watcher.Erased()))
	if absent != nil {
		t.Fatalf("watching an absent document gives no watch: %v", absent)
	}
	end := awaitClosed(t, watch)
	if end.Reason != "stopped" {
		t.Fatalf("the watch ends stopped with the invocation: %+v", end)
	}
	mu.Lock()
	before := len(delivered)
	mu.Unlock()
	setText("after")
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	if len(delivered) != before {
		t.Fatalf("a stopped watch delivers nothing more: %v", delivered)
	}
	mu.Unlock()
	rt := <-captured
	ended := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("%s after the end = %v", what, err)
		}
	}
	ended("commit", rt.Commit(context.Background(), func(durable.Tx, stepRecord) (*stepNext, error) { return nil, nil }))
	_, _, err := rt.Memo(context.Background(), "x")
	ended("memo", err)
	_, err = rt.MemoCandidate(context.Background(), "x", 1)
	ended("memo write", err)
	ended("sleep", rt.SleepUntil(context.Background(), 0, stepState{Phase: "run"}))
	_, err = rt.WatchDocErased(context.Background(), notes)
	ended("watch", err)
	_, err = rt.Agent(context.Background())
	ended("agent", err)
	if text := panicText(func() { rt.Now() }); !strings.Contains(text, "invocation has ended") {
		t.Fatalf("now after the end panics: %q", text)
	}
	if text := panicText(func() { rt.Report(errors.New("late")) }); !strings.Contains(text, "invocation has ended") {
		t.Fatalf("report after the end panics: %q", text)
	}
}

type pingHooks struct{ Ping func() }

// harness-tasks.test.ts:360 "keeps one registry snapshot per phase and refreshes it at the phase boundary".
func TestCoreTaskKeepsOneRegistrySnapshotPerPhase(t *testing.T) {
	type seenPhase struct {
		phase string
		tools string
		same  bool
	}
	var mu sync.Mutex
	var seen []seenPhase
	names := func(snapshot durable.RegistrySnapshot) string {
		var out []string
		for _, tool := range snapshot.Tools() {
			out = append(out, tool.Tool.Name)
		}
		return strings.Join(out, ",")
	}
	entered, gate := make(chan struct{}), make(chan struct{})
	task := defineSteps("test.snapshots", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			before := rt.Registry()
			close(entered)
			<-gate
			mu.Lock()
			seen = append(seen, seenPhase{"start", names(rt.Registry()), rt.Registry() == before})
			mu.Unlock()
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return running("b"), nil })
		},
		"b": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			mu.Lock()
			seen = append(seen, seenPhase{"b", names(rt.Registry()), true})
			mu.Unlock()
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
	h, registry, root := openTasksRegistry(t, task.Erased())
	id := startTask(t, root, task.Erased())
	h.Resume()
	<-entered
	if err := registry.Install(&durable.Extension{Name: "late", Tools: []*durable.ToolRegistration{{ToolSchema: echoToolSchema()}}}); err != nil {
		t.Fatal(err)
	}
	close(gate)
	waitSettled(t, h, id)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != (seenPhase{"start", "", true}) || seen[1] != (seenPhase{"b", "echo", true}) {
		t.Fatalf("seen = %+v", seen)
	}
}

// harness-tasks.test.ts:409 "resolves the agent at first use in a phase from its snapshot, keeps it for the phase, and anew at the next".
func TestCoreTaskResolvesTheAgentAtFirstUseKeepsItForThePhaseAndAnewAtTheNext(t *testing.T) {
	var mu sync.Mutex
	var pings []string
	var seen []string
	ping := func(rt durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, *pingHooks]) []string {
		mu.Lock()
		pings = nil
		mu.Unlock()
		if err := rt.Hooks().Each("ping", func(handlers *pingHooks) error { handlers.Ping(); return nil }); err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), pings...)
	}
	waitingBeforeUse, beforeUse, waitingAfterUse, afterUse := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	task := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, *pingHooks]{
		Name: "test.agent-phases", Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "a"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, *pingHooks]{
			"a": func(ctx context.Context, _ stepRecord, rt durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, *pingHooks]) error {
				close(waitingBeforeUse)
				<-beforeUse
				first, err := rt.Agent(ctx)
				if err != nil {
					return err
				}
				seen = append(seen, fmt.Sprint("a ", ping(rt), " ", first.ThinkingLevel))
				close(waitingAfterUse)
				<-afterUse
				second, err := rt.Agent(ctx)
				if err != nil {
					return err
				}
				seen = append(seen, fmt.Sprint("a ", ping(rt), " ", second.ThinkingLevel))
				return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return running("b"), nil })
			},
			"b": func(ctx context.Context, _ stepRecord, rt durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, *pingHooks]) error {
				agent, err := rt.Agent(ctx)
				if err != nil {
					return err
				}
				seen = append(seen, fmt.Sprint("b ", ping(rt), " ", agent.ThinkingLevel))
				return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
			},
		},
		Abort: func(context.Context, stepRecord, durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, *pingHooks]) error {
			return nil
		},
	})
	h, registry, root := openTasksRegistry(t, task.Erased())
	addPing := func(name, label string) {
		if err := registry.Install(&durable.Extension{Name: name, Hooks: []durable.HookRegistration{{Task: "test.agent-phases", Handlers: &pingHooks{Ping: func() {
			mu.Lock()
			defer mu.Unlock()
			pings = append(pings, label)
		}}}}}); err != nil {
			t.Fatal(err)
		}
	}
	addPing("ping-before", "before")
	id := startTask(t, root, task.Erased())
	h.Resume()
	configure := func(level ai.ModelThinkingLevel) {
		if err := root.Configure(t.Context(), harness.AgentChange{ThinkingLevel: harness.SetTo(level)}); err != nil {
			t.Fatal(err)
		}
	}
	// Configured during the phase but before its first use: the lazy resolution reads it.
	<-waitingBeforeUse
	configure("low")
	close(beforeUse)
	// Configured and installed after the first use: not seen until the next phase.
	<-waitingAfterUse
	addPing("ping-late", "late")
	configure("high")
	close(afterUse)
	waitSettled(t, h, id)
	mu.Lock()
	defer mu.Unlock()
	want := []string{"a [before] low", "a [before] low", "b [before late] high"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
}

func abortedNext(reason string) *stepNext {
	return &stepNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted, Reason: &reason}}
}

func abortingWith(reason string) func(context.Context, stepRuntime) error {
	return func(ctx context.Context, rt stepRuntime) error {
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return abortedNext(reason), nil })
	}
}

func awaitChan[T any](t *testing.T, what string, c <-chan T) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(15 * time.Second):
		t.Fatalf("%s never happened", what)
		panic("unreachable")
	}
}

// harness-tasks.test.ts:917 "rejects the run's commits and memo writes after the mark, and settles through a fresh abort invocation".
func TestCoreTaskRejectsTheRunsCommitsAndMemoWritesAfterTheMark(t *testing.T) {
	reached := make(chan struct{})
	type result struct {
		errs     []string
		memoRead durable.JsonValue
		run      stepRuntime
	}
	runDone := make(chan result, 1)
	abortRuntimes := make(chan stepRuntime, 4)
	currents := make(chan stepRecord, 1)
	task := defineSteps("test.marked", func(ctx context.Context, rt stepRuntime) error {
		abortRuntimes <- rt
		return rt.Commit(ctx, func(_ durable.Tx, current stepRecord) (*stepNext, error) {
			currents <- current
			return abortedNext("mark"), nil
		})
	}, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			var out result
			out.run = rt
			if _, err := rt.MemoCandidate(ctx, "kept", 1); err != nil {
				return err
			}
			close(reached)
			// Keep working after the signal: every later write of this run must reject.
			<-rt.Signal().Done()
			bg := context.Background()
			out.memoRead, _, _ = rt.Memo(bg, "kept")
			if _, err := rt.MemoCandidate(bg, "late", 2); err != nil {
				out.errs = append(out.errs, err.Error())
			}
			if err := rt.Commit(bg, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil }); err != nil {
				out.errs = append(out.errs, err.Error())
			}
			runDone <- out
			return nil
		},
	})
	h, root := openTasksHarness(t, task.Erased())
	id := startTask(t, root, task.Erased())
	h.Resume()
	<-reached
	if result, err := h.AbortTask(t.Context(), id); err != nil || result != "marked" {
		t.Fatalf("abortTask = %q, %v", result, err)
	}
	outcome := waitSettled(t, h, id).State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeAborted || outcome.Reason == nil || *outcome.Reason != "mark" {
		t.Fatalf("outcome = %+v", outcome)
	}
	out := awaitChan(t, "the run's result", runDone)
	want := fmt.Sprintf("Task %d has a durable abort mark", id)
	if len(out.errs) != 2 || out.errs[0] != want || out.errs[1] != want {
		t.Fatalf("errors = %v, want two of %q", out.errs, want)
	}
	if fmt.Sprint(out.memoRead) != "1" {
		t.Fatalf("a memo read after the mark = %v", out.memoRead)
	}
	current := awaitChan(t, "the abort commit's task", currents)
	if !current.AbortRequested || fmt.Sprint(current.Memos) != "map[kept:1]" {
		t.Fatalf("the abort invocation reads abortRequested=%v memos=%v", current.AbortRequested, current.Memos)
	}
	if abortRuntime := awaitChan(t, "the abort runtime", abortRuntimes); abortRuntime == out.run {
		t.Fatal("the abort handler runs on a fresh runtime")
	}
}

// harness-tasks.test.ts:961 "starts no further phase after a mark that lands during a phase with progress".
func TestCoreTaskStartsNoFurtherPhaseAfterAMarkDuringAPhaseWithProgress(t *testing.T) {
	reached, proceed := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var phases []string
	note := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		phases = append(phases, name)
	}
	task := defineSteps("test.mark-boundary", func(ctx context.Context, rt stepRuntime) error {
		note("abort")
		return abortingWith("boundary")(ctx, rt)
	}, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			note("one")
			if err := rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return running("two"), nil }); err != nil {
				return err
			}
			close(reached)
			// Ignores the signal and returns normally after the mark.
			<-proceed
			return nil
		},
		"two": func(context.Context, stepRecord, stepRuntime) error { note("two"); return nil },
	})
	h, root := openTasksHarness(t, task.Erased())
	id := startTask(t, root, task.Erased())
	h.Resume()
	<-reached
	aborting := make(chan string, 1)
	go func() {
		result, err := h.AbortTask(t.Context(), id)
		if err != nil {
			result = err.Error()
		}
		aborting <- result
	}()
	// The abort joins the run: it waits for the handler that ignores its signal.
	select {
	case result := <-aborting:
		t.Fatalf("abortTask returned %q before the run it joins ended", result)
	case <-time.After(300 * time.Millisecond):
	}
	close(proceed)
	if result := awaitChan(t, "abortTask", aborting); result != "marked" {
		t.Fatalf("abortTask = %q", result)
	}
	outcome := waitSettled(t, h, id).State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeAborted || outcome.Reason == nil || *outcome.Reason != "boundary" {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(phases) != "[one abort]" {
		t.Fatalf("phases = %v, want [one abort]", phases)
	}
}

// harness-tasks.test.ts:998 "signals and joins the run before returning, then runs the abort handler".
func TestCoreTaskAbortSignalsAndJoinsTheRunBeforeReturning(t *testing.T) {
	reached := make(chan struct{})
	var runEnded atomic.Bool
	// Pi's second abortTask answers "marked" because the abort handler's commit has not reached the line yet: a property of
	// microtask order that goroutines cannot reproduce. The abort handler waits here until the second call answered, which pins
	// the same rule (a task whose abort invocation is live and uncommitted is "marked") without a race.
	secondAnswered := make(chan struct{})
	abort := abortingWith("test")
	task := defineSteps("test.signalled", func(ctx context.Context, rt stepRuntime) error {
		<-secondAnswered
		return abort(ctx, rt)
	}, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(_ context.Context, _ stepRecord, rt stepRuntime) error {
			close(reached)
			defer runEnded.Store(true)
			<-rt.Signal().Done()
			return nil
		},
	})
	h, root := openTasksHarness(t, task.Erased())
	id := startTask(t, root, task.Erased())
	h.Resume()
	<-reached
	if result, err := h.AbortTask(t.Context(), id); err != nil || result != "marked" {
		t.Fatalf("abortTask = %q, %v", result, err)
	}
	if !runEnded.Load() {
		t.Fatal("abortTask returned before the signalled run ended")
	}
	if result, err := h.AbortTask(t.Context(), id); err != nil || result != "marked" {
		t.Fatalf("a second abortTask = %q, %v", result, err)
	}
	close(secondAnswered)
	outcome := waitSettled(t, h, id).State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeAborted || outcome.Reason == nil || *outcome.Reason != "test" {
		t.Fatalf("outcome = %+v", outcome)
	}
	// Once the abort invocation committed the task is terminal (harness-tasks.test.ts:869).
	if result, err := h.AbortTask(t.Context(), id); err != nil || result != "terminal" {
		t.Fatalf("an abortTask after the outcome = %q, %v", result, err)
	}
}

// harness-tasks.test.ts:1020 "aborts waiting work before its wait ends and faults abort handlers that throw or settle nothing".
func TestCoreTaskAbortsWaitingWorkAndFaultsAbortHandlersThatThrowOrSettleNothing(t *testing.T) {
	gate := make(chan struct{})
	dependency := defineSteps("test.dependency", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			<-gate
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
	var first durable.TaskId
	wait := func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) {
			return &stepNext{Status: durable.TaskWaiting, Checkpoint: &stepState{Phase: "start"}, On: []durable.TaskId{first}, Policy: "allSettled"}, nil
		})
	}
	lazy := defineSteps("test.lazy-abort", func(context.Context, stepRuntime) error { return nil }, map[string]func(context.Context, stepRecord, stepRuntime) error{"start": wait})
	throwing := defineSteps("test.throwing-abort", func(context.Context, stepRuntime) error { return errors.New("abort failed") }, map[string]func(context.Context, stepRecord, stepRuntime) error{"start": wait})
	h, root := openTasksHarness(t, dependency.Erased(), lazy.Erased(), throwing.Erased())
	first = startTask(t, root, dependency.Erased())
	lazyId, throwingId := startTask(t, root, lazy.Erased()), startTask(t, root, throwing.Erased())
	h.Resume()
	for _, id := range []durable.TaskId{lazyId, throwingId} {
		deadline := time.Now().Add(15 * time.Second)
		for {
			record, err := h.GetTask(t.Context(), id)
			if err == nil && record != nil && record.State.Status == durable.TaskWaiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("task %d never waited: %+v %v", id, record, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, id := range []durable.TaskId{lazyId, throwingId} {
		if _, err := h.AbortTask(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[durable.TaskId]string{
		lazyId:     fmt.Sprintf("Abort handler of task %d returned without a terminal outcome", lazyId),
		throwingId: "abort failed",
	} {
		outcome := waitSettled(t, h, id).State.Outcome
		if outcome == nil || outcome.Status != durable.OutcomeFaulted || outcome.Error == nil || outcome.Error.Message != want {
			t.Fatalf("task %d outcome = %+v, want faulted %q", id, outcome, want)
		}
	}
	if record, err := h.GetTask(t.Context(), first); err != nil || record == nil || record.State.Status != durable.TaskRunning {
		t.Fatalf("the dependency keeps running: %+v %v", record, err)
	}
	close(gate)
	waitSettled(t, h, first)
}

// harness-tasks.test.ts:360 RegistrySnapshot.tools: every installed tool paired with its own extension, in install order across extensions and within each.
func TestCoreRegistrySnapshotToolsPairEachToolWithItsExtensionInInstallOrder(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	task := defineSteps("test.tools", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			tools := durable.RegistrySnapshot(rt.Registry())
			mu.Lock()
			for _, entry := range tools.Tools() {
				seen = append(seen, entry.Extension.Name+":"+entry.Tool.Name)
			}
			mu.Unlock()
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
	h, registry, root := openTasksRegistry(t, task.Erased())
	schema := func(name string) *durable.ToolRegistration {
		return &durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}
	}
	for _, extension := range []*durable.Extension{
		{Name: "first", Tools: []*durable.ToolRegistration{schema("a"), schema("b")}},
		{Name: "second", Tools: []*durable.ToolRegistration{schema("c")}},
	} {
		if err := registry.Install(extension); err != nil {
			t.Fatal(err)
		}
	}
	id := startTask(t, root, task.Erased())
	h.Resume()
	waitSettled(t, h, id)
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(seen, ","); got != "first:a,first:b,second:c" {
		t.Fatalf("tools = %s, want first:a,first:b,second:c", got)
	}
}
