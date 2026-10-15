package driver_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// scheduler.ts TaskRuntime.getTask: a task reads another task's record through its runtime, by id, within the same conversation;
// an id that names no task reads as undefined.
func TestCoreTaskRuntimeGetTaskReadsAChildRecordAndNothingForAnUnknownID(t *testing.T) {
	child := defineSteps("test.getchild", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("child-result"), nil })
		},
	})
	var kind string
	var gotID, childID durable.TaskId
	var unknownNil bool
	parent := defineSteps("test.getparent", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			if err := rt.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*stepNext, error) {
				id, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
				childID = id
				return nil, err
			}); err != nil {
				return err
			}
			record, err := rt.GetTask(ctx, childID)
			if err != nil || record == nil {
				return errors.New("child record unreadable")
			}
			kind, gotID = record.Kind, record.Id
			unknown, err := rt.GetTask(ctx, childID+1000)
			if err != nil {
				return err
			}
			unknownNil = unknown == nil
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("parent-done"), nil })
		},
	})
	h, root := openTasksHarness(t, parent.Erased(), child.Erased())
	settled := waitSettled(t, h, startTask(t, root, parent.Erased()))
	if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("parent = %+v", settled.State.Outcome)
	}
	if kind != "test.getchild" || gotID != childID || childID == 0 {
		t.Fatalf("the runtime read kind %q id %d, want the child's (%d)", kind, gotID, childID)
	}
	if !unknownNil {
		t.Fatal("an unknown task id must read as undefined (nil)")
	}
}
