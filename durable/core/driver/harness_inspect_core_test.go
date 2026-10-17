// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// harness-inspect.test.ts through driver.OpenHarness over the real core.

type inspectOptions struct {
	gate    chan struct{}
	migrate func() error
}

// inspectTask is a one-phase task that completes once gate closes; migrate runs when an older stored version migrates.
func inspectTask(name string, version int, options ...inspectOptions) durable.Task[durable.JsonValue, stepState, durable.JsonValue, any] {
	var option inspectOptions
	if len(options) > 0 {
		option = options[0]
	}
	definition := durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name: name, Version: version,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
				if option.gate != nil {
					select {
					case <-option.gate:
					case <-ctx.Done():
						return context.Cause(ctx)
					}
				}
				return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
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

func inspectStateOf(tasks []harness.TaskInspection, id durable.TaskId) *harness.TaskInspectionState {
	for _, entry := range tasks {
		if entry.Record.Id == id {
			state := entry.State
			return &state
		}
	}
	return nil
}

func expectInspectState(t *testing.T, got *harness.TaskInspectionState, want harness.TaskInspectionState) {
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

func inspectIds(tasks []harness.TaskInspection) []durable.TaskId {
	var ids []durable.TaskId
	for _, entry := range tasks {
		ids = append(ids, entry.Record.Id)
	}
	return ids
}

func pollUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// harness-inspect.test.ts "derives every live task's state without running task code".
func TestCoreInspectDerivesEveryLiveTasksStateWithoutRunningTaskCode(t *testing.T) {
	gate := make(chan struct{})
	gateTask := inspectTask("test.gate", 1, inspectOptions{gate: gate})
	var gateId atomic.Int64
	dependent := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name: "test.dependent", Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "wait"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"wait": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
				return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) {
					return &stepNext{Status: durable.TaskWaiting, Checkpoint: &stepState{Phase: "done"}, On: []durable.TaskId{durable.TaskId(gateId.Load())}, Policy: durable.JoinAllSettled}, nil
				})
			},
			"done": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
				return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
			},
		},
		Abort: func(context.Context, stepRecord, stepRuntime) error { return nil },
	})
	var migrations atomic.Int32
	h, root := openTasksHarness(t,
		gateTask.Erased(),
		dependent.Erased(),
		inspectTask("test.migrating", 2, inspectOptions{migrate: func() error { migrations.Add(1); return nil }}).Erased(),
		inspectTask("test.no-migration", 2).Erased(),
		inspectTask("test.failing", 2, inspectOptions{migrate: func() error { return errors.New("cannot migrate") }}).Erased(),
		inspectTask("test.too-old", 1).Erased(),
	)
	type taskIds struct{ gate, dependent, migrating, noMigration, failing, tooOld, missing durable.TaskId }
	owned := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}}
	ids, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (taskIds, error) {
		var ids taskIds
		var err error
		create := func(task durable.AnyTask) durable.TaskId {
			if err != nil {
				return 0
			}
			var id durable.TaskId
			id, err = tx.CreateTaskErased(task, nil, owned)
			return id
		}
		ids.gate = create(gateTask.Erased())
		gateId.Store(int64(ids.gate))
		ids.dependent = create(dependent.Erased())
		// Stored by definitions other than the registered ones.
		ids.migrating = create(inspectTask("test.migrating", 1).Erased())
		ids.noMigration = create(inspectTask("test.no-migration", 1).Erased())
		ids.failing = create(inspectTask("test.failing", 1).Erased())
		ids.tooOld = create(inspectTask("test.too-old", 2).Erased())
		ids.missing = create(inspectTask("test.missing", 1).Erased())
		return ids, err
	})
	if err != nil {
		t.Fatal(err)
	}
	inspect := func() harness.HarnessInspection {
		t.Helper()
		inspection, err := h.Inspect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return inspection
	}

	paused := inspect()
	if paused.Scheduling != "paused" {
		t.Fatalf("scheduling %s", paused.Scheduling)
	}
	if got, want := inspectIds(paused.Tasks), []durable.TaskId{ids.gate, ids.dependent, ids.migrating, ids.noMigration, ids.failing, ids.tooOld, ids.missing}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tasks %v, want %v", got, want)
	}
	expectInspectState(t, inspectStateOf(paused.Tasks, ids.gate), harness.TaskInspectionState{Kind: harness.TaskInspectionReady})
	expectInspectState(t, inspectStateOf(paused.Tasks, ids.dependent), harness.TaskInspectionState{Kind: harness.TaskInspectionReady})
	expectInspectState(t, inspectStateOf(paused.Tasks, ids.migrating), harness.TaskInspectionState{Kind: harness.TaskInspectionReady, Migrates: true})
	// A migration that was never tried is not run to find out.
	expectInspectState(t, inspectStateOf(paused.Tasks, ids.failing), harness.TaskInspectionState{Kind: harness.TaskInspectionReady, Migrates: true})
	expectInspectState(t, inspectStateOf(paused.Tasks, ids.noMigration), harness.TaskInspectionState{Kind: harness.TaskInspectionBlocked, Reason: "migration_failed", Error: errors.New("Task test.no-migration version 2 has no migration from 1")})
	expectInspectState(t, inspectStateOf(paused.Tasks, ids.tooOld), harness.TaskInspectionState{Kind: harness.TaskInspectionBlocked, Reason: "task_too_old"})
	expectInspectState(t, inspectStateOf(paused.Tasks, ids.missing), harness.TaskInspectionState{Kind: harness.TaskInspectionBlocked, Reason: "missing_task"})
	if migrations.Load() != 0 {
		t.Fatal("inspect ran a migration")
	}
	if inspect().Scheduling != "paused" {
		t.Fatal("inspect resumed scheduling")
	}

	h.Resume()
	pollUntil(t, "the migration", func() bool { return migrations.Load() == 1 })
	waitSettled(t, h, ids.migrating)
	var running harness.HarnessInspection
	pollUntil(t, "gate running and dependent waiting", func() bool {
		running = inspect()
		gateState, dependentState := inspectStateOf(running.Tasks, ids.gate), inspectStateOf(running.Tasks, ids.dependent)
		return gateState != nil && gateState.Kind == harness.TaskInspectionRunning && dependentState != nil && dependentState.Kind == harness.TaskInspectionWaiting
	})
	expectInspectState(t, inspectStateOf(running.Tasks, ids.dependent), harness.TaskInspectionState{Kind: harness.TaskInspectionWaiting, On: []durable.TaskId{ids.gate}})
	if running.Scheduling != "running" {
		t.Fatalf("scheduling %s", running.Scheduling)
	}
	expectInspectState(t, inspectStateOf(running.Tasks, ids.gate), harness.TaskInspectionState{Kind: harness.TaskInspectionRunning})
	if inspectStateOf(running.Tasks, ids.migrating) != nil {
		t.Fatal("the migrated task is still live")
	}
	expectInspectState(t, inspectStateOf(running.Tasks, ids.failing), harness.TaskInspectionState{Kind: harness.TaskInspectionBlocked, Reason: "migration_failed", Error: errors.New("cannot migrate")})

	close(gate)
	waitSettled(t, h, ids.dependent)
	if got, want := inspectIds(inspect().Tasks), []durable.TaskId{ids.noMigration, ids.failing, ids.tooOld, ids.missing}; !reflect.DeepEqual(got, want) {
		t.Fatalf("settled tasks %v, want %v", got, want)
	}
}
