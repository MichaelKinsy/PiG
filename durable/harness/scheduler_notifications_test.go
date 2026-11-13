// Ports packages/durable/test/harness-tasks.test.ts "task scheduling" (Pi e50dc7af9, #11892): a scheduler pass cleaned its
// shared set after awaiting its decision region, erasing waiters and holders a publication had queued meanwhile, so tasks
// stayed waiting or completing.
//
// Go mapping: Pi interposes on `session.touch` to queue a held publication as a microtask that runs between a pass's
// decision and the continuation that applies it. A Go pass decides and applies under one hold of the scheduler's mutex
// (scheduler_region.go) and a publication's observer takes that mutex, so the window does not exist: the observer runs
// before the pass (the decision sees what it queued) or after it (the sets keep it). The Writer publishes parts strictly in
// order and waits for each publication (writer.go publish), so Pi's `storage.publishing` hold cannot reorder one commit's
// publication behind the next one's either. What remains to provoke is a pass planning while the next part is published,
// which the children below do by ending together, many times; the tests pin the outcome and the order, and fail when a pass
// releases the mutex between its decision and its application (the mutation the report records).

package harness

// pi: packages/durable/src/harness/scheduler.ts

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// taskEnded reports whether any write of the commit is a terminal record of a task of kind.
func taskEnded(writes []durable.StorageWrite, kind string) bool {
	for _, write := range writes {
		task, ok := write.(durable.TaskWrite)
		if ok && task.Value.State.Status == durable.TaskTerminal && task.Value.Kind == kind {
			return true
		}
	}
	return false
}

// holdsChildTask creates child below itself, then ends while the child is live: its outcome is held as `completing` until
// a finalize pass ends it.
func holdsChildTask(name string, child stepTask, created ...func(parent, child durable.TaskId)) stepTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    name,
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "create"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"create": func(ctx context.Context, task stepRecord, runtime stepRuntime) error {
				return runtime.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					childId, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: scaleOwnedBy(task.Id)})
					if err != nil {
						return nil, err
					}
					if len(created) > 0 {
						created[0](task.Id, childId)
					}
					return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &stepState{Phase: "end"}}, nil
				})
			},
			"end": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) },
		},
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue]("test"), nil
			})
		},
	})
}

func taskStatusOf(t *testing.T, harness Harness, id durable.TaskId) durable.TaskStatus {
	t.Helper()
	record, err := harness.GetTask(testContext, id)
	if err != nil || record == nil {
		t.Fatalf("task %d = %v, %v", id, record, err)
	}
	return record.State.Status
}

// A finalize pass plans while the publication of another holder's last child is observed: the check that publication asks
// for must stay for the next pass, or the holder stays completing.
func TestFinalizePassKeepsAHolderCheckPublishedWhilePlanning(t *testing.T) {
	for run := range 25 {
		gate := deferred()
		heldChild := tkGated("test.held-child", gate)
		otherChild := tkGated("test.other-child", gate)
		held := holdsChildTask("test.held", heldChild)
		other := holdsChildTask("test.other", otherChild)
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{heldChild.Erased(), otherChild.Erased(), held.Erased(), other.Erased()}, tkOptions{storage: store})
		otherId := tkStart(t, opened.root, other.Erased())
		heldId := tkStart(t, opened.root, held.Erased())
		opened.harness.Resume()
		eventually(t, func() bool {
			return taskStatusOf(t, opened.harness, otherId) == durable.TaskCompleting && taskStatusOf(t, opened.harness, heldId) == durable.TaskCompleting
		})
		flush()
		// Both children end together: the publication of one asks for a finalize pass over its holder while the other's is
		// published, and the held child's holder must end whichever order the pass and the publication take.
		gate.resolve()
		eventually(t, func() bool { return taskStatusOf(t, opened.harness, heldId) == durable.TaskTerminal })
		if status := taskStatusOf(t, opened.harness, otherId); status != durable.TaskTerminal {
			t.Fatalf("run %d: other = %s, want terminal", run, status)
		}
		// Ordering: a holder ends after the child it held, never before.
		for _, pair := range [][2]string{{"test.held-child", "test.held"}, {"test.other-child", "test.other"}} {
			child, holder := -1, -1
			for index := range store.commitCount() {
				writes := store.commitAt(index)
				if child < 0 && taskEnded(writes, pair[0]) {
					child = index
				}
				if holder < 0 && taskEnded(writes, pair[1]) {
					holder = index
				}
			}
			if child < 0 || holder < 0 || holder < child {
				t.Fatalf("run %d: %s ended at commit %d, its child %s at %d", run, pair[1], holder, pair[0], child)
			}
		}
		mustClose(t, opened.harness)
	}
}

// A reservation pass plans while the publication that ended the last task a waiter waits on is observed: the waiter's
// candidacy must stay for the next pass, or the waiter stays waiting.
func TestReservationPassKeepsAWaiterMadeACandidateWhilePlanning(t *testing.T) {
	for range 25 {
		gate := deferred()
		child := tkGated("test.held-child", gate)
		var on []durable.TaskId
		var onMu sync.Mutex
		parent := holdsChildTask("test.held-parent", child, func(parent, child durable.TaskId) {
			onMu.Lock()
			on = []durable.TaskId{parent, child}
			onMu.Unlock()
		})
		waiter := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.waiter",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "wait"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"wait": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						onMu.Lock()
						defer onMu.Unlock()
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskWaiting, Checkpoint: &stepState{Phase: "resume"}, On: on, Policy: durable.JoinAllSettled}, nil
					})
				},
				"resume": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) },
			},
			Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("test"), nil
				})
			},
		})
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{parent.Erased(), child.Erased(), waiter.Erased()}, tkOptions{storage: store})
		parentId := tkStart(t, opened.root, parent.Erased())
		opened.harness.Resume()
		eventually(t, func() bool { return taskStatusOf(t, opened.harness, parentId) == durable.TaskCompleting })
		waiterId := tkStart(t, opened.root, waiter.Erased())
		eventually(t, func() bool { return taskStatusOf(t, opened.harness, waiterId) == durable.TaskWaiting })
		flush()
		// The waiter waits on the parent and its child. The child's publication makes the waiter a candidate and asks for the
		// reconcile pass that finalizes the parent; the parent's terminal record is published as the reservation pass for the
		// waiter plans, and the pass must not drop the waiter the publication made a candidate.
		gate.resolve()
		eventually(t, func() bool { return taskStatusOf(t, opened.harness, waiterId) == durable.TaskTerminal })
		mustClose(t, opened.harness)
	}
}
