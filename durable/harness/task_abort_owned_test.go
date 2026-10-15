package harness

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// TaskRuntime.AbortOwned aborts a task the running task owns and returns once it is terminal; it fails for a task another
// owns (Pi scheduler.ts abortOwned, types.ts TaskRuntime.abortOwned).
func TestTaskRuntimeAbortOwned(t *testing.T) {
	fixtures := newOwnFixtures()
	type outcome struct {
		abortOwned    error
		childStatus   durable.TaskStatus
		abortForeign  error
		abortTerminal error
		childOutcome  durable.TaskOutcomeStatus
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
				if err := runtime.Commit(ctx, func(tx durable.Tx, _ holdRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
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
				return runtime.Commit(ctx, func(durable.Tx, holdRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](nil), nil
				})
			},
		},
	})
	harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{fixtures.hold.Erased(), spawner.Erased()})
	opened := ownOpened{harness: harness, root: mustRoot(t, harness, nil)}
	harness.Resume()
	var err error
	foreign, err = durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
		other, err := durable.CreateTask(tx, fixtures.hold, holdInput{Name: "other"}, durable.TaskOptions{Ownership: conversationOwned})
		if err != nil {
			return 0, err
		}
		return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "foreign"}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: other}})
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, spawner, holdInput{Name: "spawner"}, durable.TaskOptions{Ownership: conversationOwned})
	})
	if err != nil {
		t.Fatal(err)
	}
	ownExpectOutcome(t, opened.harness, id, durable.OutcomeCompleted)
	if got.abortOwned != nil || got.childStatus != durable.TaskTerminal || got.childOutcome != durable.OutcomeAborted {
		t.Fatalf("AbortOwned %v, child %s/%s, want nil and terminal aborted", got.abortOwned, got.childStatus, got.childOutcome)
	}
	if got.abortTerminal != nil {
		t.Fatalf("AbortOwned of a terminal task: %v, want nil", got.abortTerminal)
	}
	if got.abortForeign == nil || !strings.Contains(got.abortForeign.Error(), "is not owned by task") {
		t.Fatalf("AbortOwned of another's task: %v", got.abortForeign)
	}
	if ownStatus(t, opened.harness, foreign).Status == durable.TaskTerminal {
		t.Fatal("the foreign task was aborted")
	}
	fixtures.open("foreign", "completed")
	fixtures.open("other", "completed")
	mustClose(t, opened.harness)
}
