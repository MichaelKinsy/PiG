// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// TaskRuntime.AbortOwned over the real core: it aborts a task the running task owns and returns once it is terminal, returns for
// a task that already ended, and fails for a task another owns (Pi scheduler.ts abortOwned, types.ts TaskRuntime.abortOwned).
func TestTaskRuntimeAbortOwnedOverTheCore(t *testing.T) {
	fixtures := newOwnFixtures()
	type outcome struct {
		abortOwned    error
		childStatus   durable.TaskStatus
		childOutcome  durable.TaskOutcomeStatus
		abortTerminal error
		abortForeign  error
	}
	var got outcome
	var foreign durable.TaskId
	spawner := durable.DefineTask(durable.TaskDefinition[holdInput, stepState, durable.JsonValue, any]{
		Name:    "test.abort-owned",
		Version: 1,
		Initial: func(holdInput) stepState { return stepState{Phase: "work"} },
		Phases: map[string]durable.PhaseHandler[holdInput, stepState, durable.JsonValue, any]{
			"work": func(ctx context.Context, task holdRecord, runtime holdRuntime) error {
				var child durable.TaskId
				if err := runtime.Commit(ctx, func(tx durable.Tx, _ holdRecord) (*holdNext, error) {
					var err error
					child, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "owned"}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: runtime.TaskId()}})
					return nil, err
				}); err != nil {
					return err
				}
				got.abortOwned = runtime.AbortOwned(ctx, child)
				record, err := runtime.GetTask(ctx, child)
				if err != nil {
					return err
				}
				got.childStatus = record.State.Status
				if record.State.Outcome != nil {
					got.childOutcome = record.State.Outcome.Status
				}
				got.abortTerminal = runtime.AbortOwned(ctx, child)
				got.abortForeign = runtime.AbortOwned(ctx, foreign)
				return runtime.Commit(ctx, func(durable.Tx, holdRecord) (*holdNext, error) {
					return &holdNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted}}, nil
				})
			},
		},
	})
	h, root := openTasksHarness(t, fixtures.hold.Erased(), spawner.Erased())
	h.Resume()
	foreign, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.TaskId, error) {
		other, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "other"}, durable.TaskOptions{Ownership: ownedByConversation})
		if err != nil {
			return 0, err
		}
		return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "foreign"}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: other}})
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, spawner, holdInput{Name: "spawner"}, durable.TaskOptions{Ownership: ownedByConversation})
	})
	if err != nil {
		t.Fatal(err)
	}
	o := ownOpened{h: h, root: root}
	o.expectOutcome(t, id, durable.OutcomeCompleted)
	if got.abortOwned != nil || got.childStatus != durable.TaskTerminal || got.childOutcome != durable.OutcomeAborted {
		t.Fatalf("AbortOwned %v, child %s/%s, want nil and terminal aborted", got.abortOwned, got.childStatus, got.childOutcome)
	}
	if got.abortTerminal != nil {
		t.Fatalf("AbortOwned of a terminal task: %v, want nil", got.abortTerminal)
	}
	if got.abortForeign == nil || !strings.Contains(got.abortForeign.Error(), "is not owned by task") {
		t.Fatalf("AbortOwned of another's task: %v", got.abortForeign)
	}
	if o.status(t, foreign) == durable.TaskTerminal {
		t.Fatal("the foreign task was aborted")
	}
}
