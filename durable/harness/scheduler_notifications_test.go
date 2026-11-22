// Ports packages/durable/test/harness-tasks.test.ts "task scheduling" (Pi e50dc7af9, #11892): a scheduler pass cleaned its
// shared set after awaiting its decision region, erasing waiters and holders a publication had queued meanwhile, so tasks
// stayed waiting or completing.
//
// Go mapping: Pi interposes on `session.touch` to queue a held publication as a microtask between a pass's decision and the
// continuation that applies it. A Go pass has no such window: its decision region runs the decision again after every wait
// for a gate (`regionLocked`, scheduler_region.go), reading `finalizeChecks` and `runnable` afresh, and it applies the
// decision that stands (clears the checks, deletes the dropped candidates) under the same hold of `s.mu`; a publication's
// observer takes `s.mu`, so what it queues is read by the decision that stands or stays in the set. The Writer publishes
// parts strictly in order and waits for each publication (writer.go publish), so holding storage commits
// (`controlledStorage.holdCommits`) in the finalize test below publishes `held`'s last child while pass 1 waits in its
// region on `other`'s gate: deterministic, one run. The reservation test provokes its case by ending the tasks together,
// many times. Both pin the outcome and the order, and fail when a pass releases the mutex between its decision and its
// application.

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

// holdsChildrenTask creates all children below itself, then ends while they are live: its outcome is held as `completing`
// until a finalize pass ends it.
func holdsChildrenTask(name string, children ...stepTask) stepTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    name,
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "create"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"create": func(ctx context.Context, task stepRecord, runtime stepRuntime) error {
				return runtime.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					for _, child := range children {
						if _, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: scaleOwnedBy(task.Id)}); err != nil {
							return nil, err
						}
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

// A finalize pass plans while the publication of another holder's last child is observed: the check that publication asks
// for must not be lost: the holder ends in this pass or the next.
func TestFinalizePassKeepsAHolderCheckPublishedWhilePlanning(t *testing.T) {
	otherGate1 := deferred()
	otherGate2 := deferred()
	heldGate := deferred()
	otherChild1 := tkGated("test.other-child-1", otherGate1)
	otherChild2 := tkGated("test.other-child-2", otherGate2)
	heldChild := tkGated("test.held-child", heldGate)
	other := holdsChildrenTask("test.other", otherChild1, otherChild2)
	held := holdsChildTask("test.held", heldChild)
	store := newControlledStorage()
	opened := tkOpenRoot(t, []durable.AnyTask{otherChild1.Erased(), otherChild2.Erased(), heldChild.Erased(), other.Erased(), held.Erased()}, tkOptions{storage: store})
	otherId := tkStart(t, opened.root, other.Erased())
	heldId := tkStart(t, opened.root, held.Erased())
	opened.harness.Resume()
	eventually(t, func() bool {
		return taskStatusOf(t, opened.harness, otherId) == durable.TaskCompleting && taskStatusOf(t, opened.harness, heldId) == durable.TaskCompleting
	})
	flush()
	// 1. Hold storage commit 1 and resolve otherChild1 so it is in flight in commit 1.
	held1 := store.holdCommits()
	otherGate1.resolve()
	if err := held1.entered.wait(testContext); err != nil {
		t.Fatalf("waiting for commit 1 to enter storage: %v", err)
	}
	// 2. Resolve heldChild (queued as batch 2 part 0) then otherChild2 (queued as batch 2 part 1, setting unpublished[owned:other]).
	heldGate.resolve()
	flush()
	otherGate2.resolve()
	flush()
	// 3. Hold batch 2 and release commit 1: otherChild1 publishes, adds other to finalizeChecks, and starts pass 1,
	// which touches owned:other and parks in waitGatesLocked (unlocking s.mu) on batch 2 part 1's gate.
	held2 := store.holdCommits()
	held1.release()
	if err := held2.entered.wait(testContext); err != nil {
		t.Fatalf("waiting for batch 2 to enter storage: %v", err)
	}
	flush()
	// 4. Release batch 2: Writer.publish publishes part 0 (heldChild) while pass 1 is parked on part 1's gate,
	// adding held to finalizeChecks, then publishes part 1 (otherChild2), waking pass 1.
	held2.release()
	// When the gate opens the pass runs its decision again and reads held's check, queued while it waited: it ends other
	// and held. (A pass that takes its round before it waits, as Pi does since #11892, leaves held to the next pass.)
	// Either way both holders must end, each after every child it held.
	eventually(t, func() bool { return taskStatusOf(t, opened.harness, otherId) == durable.TaskTerminal })
	for range 200 {
		if taskStatusOf(t, opened.harness, heldId) == durable.TaskTerminal {
			break
		}
		awaitQuiescent(t)
		flush()
	}
	if status := taskStatusOf(t, opened.harness, heldId); status != durable.TaskTerminal {
		t.Fatalf("held = %s, want terminal (finalize pass erased held check queued while planning)", status)
	}
	// Ordering: a holder ends after every child it held, never before.
	for _, pair := range [][2]string{{"test.held-child", "test.held"}, {"test.other-child-1", "test.other"}, {"test.other-child-2", "test.other"}} {
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
			t.Fatalf("%s ended at commit %d, its child %s at %d", pair[1], holder, pair[0], child)
		}
	}
	mustClose(t, opened.harness)
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
