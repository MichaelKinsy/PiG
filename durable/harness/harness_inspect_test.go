// Ports packages/durable/test/harness-inspect.test.ts.

package harness

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type inspectTaskOptions struct {
	gate    *deferredGate
	migrate func() error
}

// inspectTask is a one-phase task that completes once gate resolves; migrate runs when an older stored version migrates.
func inspectTask(name string, version int, options ...inspectTaskOptions) stepTask {
	var option inspectTaskOptions
	if len(options) > 0 {
		option = options[0]
	}
	definition := durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    name,
		Version: version,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				if option.gate != nil {
					if err := option.gate.wait(ctx); err != nil {
						return err
					}
				}
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(context.Context, stepRecord, stepRuntime) error { return nil },
	}
	if migrate := option.migrate; migrate != nil {
		definition.Migrate = func(durable.JsonValue, durable.JsonValue, int) (durable.JsonValue, stepState, error) {
			if err := migrate(); err != nil {
				return nil, stepState{}, err
			}
			return nil, stepState{Phase: "run"}, nil
		}
	}
	return durable.DefineTask(definition)
}

func stateOf(tasks []TaskInspection, id durable.TaskId) *TaskInspectionState {
	for _, entry := range tasks {
		if entry.Record.Id == id {
			state := entry.State
			return &state
		}
	}
	return nil
}

func inspectOf(t *testing.T, harness Harness) HarnessInspection {
	t.Helper()
	inspection, err := harness.Inspect(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return inspection
}

func expectState(t *testing.T, got *TaskInspectionState, want TaskInspectionState) {
	t.Helper()
	if got == nil {
		t.Fatalf("no state, want %+v", want)
	}
	gotError, wantError := "", ""
	if got.Error != nil {
		gotError = got.Error.Error()
	}
	if want.Error != nil {
		wantError = want.Error.Error()
	}
	if got.Kind != want.Kind || got.Migrates != want.Migrates || got.Reason != want.Reason || !reflect.DeepEqual(got.On, want.On) || gotError != wantError {
		t.Fatalf("state %+v (error %q), want %+v (error %q)", *got, gotError, want, wantError)
	}
}

func TestHarnessInspect(t *testing.T) {
	t.Run("derives every live task's state without running task code", func(t *testing.T) {
		gate := deferred()
		gateTask := inspectTask("test.gate", 1, inspectTaskOptions{gate: gate})
		var gateId durable.TaskId
		dependent := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.dependent",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "wait"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"wait": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskWaiting, Checkpoint: &stepState{Phase: "done"}, On: []durable.TaskId{gateId}, Policy: durable.JoinAllSettled}, nil
					})
				},
				"done": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(context.Context, stepRecord, stepRuntime) error { return nil },
		})
		var migrations atomic.Int32
		registered := []durable.AnyTask{
			gateTask,
			dependent,
			inspectTask("test.migrating", 2, inspectTaskOptions{migrate: func() error { migrations.Add(1); return nil }}),
			inspectTask("test.no-migration", 2),
			inspectTask("test.failing", 2, inspectTaskOptions{migrate: func() error { return errors.New("cannot migrate") }}),
			inspectTask("test.too-old", 1),
		}
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), registered)
		root, err := harness.Root(testContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		type taskIds struct{ gate, dependent, migrating, noMigration, failing, tooOld, missing durable.TaskId }
		conversationOwned := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}}
		ids, err := durable.Commit(testContext, root, func(tx durable.Tx) (taskIds, error) {
			var ids taskIds
			var err error
			create := func(task stepTask) durable.TaskId {
				if err != nil {
					return 0
				}
				var id durable.TaskId
				id, err = durable.CreateTask(tx, task, nil, conversationOwned)
				return id
			}
			gateId = create(gateTask)
			ids.gate = gateId
			ids.dependent = create(dependent)
			// Stored by definitions other than the registered ones.
			ids.migrating = create(inspectTask("test.migrating", 1))
			ids.noMigration = create(inspectTask("test.no-migration", 1))
			ids.failing = create(inspectTask("test.failing", 1))
			ids.tooOld = create(inspectTask("test.too-old", 2))
			ids.missing = create(inspectTask("test.missing", 1))
			return ids, err
		})
		if err != nil {
			t.Fatal(err)
		}

		paused := inspectOf(t, harness)
		if paused.Scheduling != "paused" {
			t.Fatalf("scheduling %s", paused.Scheduling)
		}
		var listed []durable.TaskId
		for _, entry := range paused.Tasks {
			listed = append(listed, entry.Record.Id)
		}
		expectIds(t, listed, []durable.TaskId{ids.gate, ids.dependent, ids.migrating, ids.noMigration, ids.failing, ids.tooOld, ids.missing})
		expectState(t, stateOf(paused.Tasks, ids.gate), TaskInspectionState{Kind: TaskInspectionReady})
		expectState(t, stateOf(paused.Tasks, ids.dependent), TaskInspectionState{Kind: TaskInspectionReady})
		expectState(t, stateOf(paused.Tasks, ids.migrating), TaskInspectionState{Kind: TaskInspectionReady, Migrates: true})
		// A migration that was never tried is not run to find out.
		expectState(t, stateOf(paused.Tasks, ids.failing), TaskInspectionState{Kind: TaskInspectionReady, Migrates: true})
		expectState(t, stateOf(paused.Tasks, ids.noMigration), TaskInspectionState{Kind: TaskInspectionBlocked, Reason: "migration_failed", Error: errors.New("Task test.no-migration version 2 has no migration from 1")})
		expectState(t, stateOf(paused.Tasks, ids.tooOld), TaskInspectionState{Kind: TaskInspectionBlocked, Reason: "task_too_old"})
		expectState(t, stateOf(paused.Tasks, ids.missing), TaskInspectionState{Kind: TaskInspectionBlocked, Reason: "missing_task"})
		if migrations.Load() != 0 {
			t.Fatal("inspect ran a migration")
		}
		if inspectOf(t, harness).Scheduling != "paused" {
			t.Fatal("inspect resumed scheduling")
		}

		harness.Resume()
		eventually(t, func() bool { return migrations.Load() == 1 })
		if _, err := harness.WaitForTask(testContext, ids.migrating); err != nil {
			t.Fatal(err)
		}
		var running HarnessInspection
		waitFor(t, func() bool {
			running = inspectOf(t, harness)
			gateState, dependentState := stateOf(running.Tasks, ids.gate), stateOf(running.Tasks, ids.dependent)
			return gateState != nil && gateState.Kind == TaskInspectionRunning && dependentState != nil && dependentState.Kind == TaskInspectionWaiting
		})
		expectState(t, stateOf(running.Tasks, ids.dependent), TaskInspectionState{Kind: TaskInspectionWaiting, On: []durable.TaskId{ids.gate}})
		if running.Scheduling != "running" {
			t.Fatalf("scheduling %s", running.Scheduling)
		}
		expectState(t, stateOf(running.Tasks, ids.gate), TaskInspectionState{Kind: TaskInspectionRunning})
		if stateOf(running.Tasks, ids.migrating) != nil {
			t.Fatal("the migrated task is still live")
		}
		expectState(t, stateOf(running.Tasks, ids.failing), TaskInspectionState{Kind: TaskInspectionBlocked, Reason: "migration_failed", Error: errors.New("cannot migrate")})

		gate.resolve()
		if _, err := harness.WaitForTask(testContext, ids.dependent); err != nil {
			t.Fatal(err)
		}
		listed = nil
		for _, entry := range inspectOf(t, harness).Tasks {
			listed = append(listed, entry.Record.Id)
		}
		expectIds(t, listed, []durable.TaskId{ids.noMigration, ids.failing, ids.tooOld, ids.missing})
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("lists unsettled submissions", func(t *testing.T) {
		setup := chatSetup(t)
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		other, err := harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}); err != nil {
			t.Fatal(err)
		}
		input, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")})
		if err != nil {
			t.Fatal(err)
		}
		<-busy.reached

		inspection := inspectOf(t, harness)
		status, err := input.Status(testContext)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(inspection.Submissions, []durable.SubmissionRecord{status}) {
			t.Fatalf("submissions %+v, want %+v", inspection.Submissions, status)
		}
		if len(inspection.Tasks) != 1 || inspection.Tasks[0].Record.Kind != "pi.generation" || inspection.Tasks[0].State.Kind != TaskInspectionRunning {
			t.Fatalf("tasks %+v", inspection.Tasks)
		}
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})
}
