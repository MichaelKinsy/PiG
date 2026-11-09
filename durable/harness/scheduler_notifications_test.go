// Ports packages/durable/test/harness-tasks.test.ts "task scheduling" (Pi e50dc7af9, #11892): a scheduler pass cleaned its
// shared set after awaiting its decision region, erasing waiters and holders a publication had queued meanwhile, so tasks
// stayed waiting or completing.
//
// Go mapping: Pi interposes on `session.touch` to queue the held publication as a microtask that runs between a pass's
// decision and the continuation that applies it. A Go pass decides and applies under one hold of the scheduler's mutex
// (scheduler_region.go), and a publication's observer takes that mutex, so the two orders Pi's test provokes are the two
// orders the goroutines below race for: the publication observed before the pass, or after it. Either must leave the
// notification for a pass to act on. The hold is the Storage's own publication scope (durable.PublishingStorage), the Go
// mapping of Pi's `storage.publishing = ...`; the release is a goroutine started at the moment the commit that opens the
// window is published.

package harness

// pi: packages/durable/src/harness/scheduler.ts

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// publishingStorage is controlledStorage with a publication scope a test replaces (Pi's `storage.publishing`).
type publishingStorage struct {
	*controlledStorage
	mu    sync.Mutex
	scope func(ctx context.Context, publish func())
}

func newPublishingStorage() *publishingStorage {
	return &publishingStorage{controlledStorage: newControlledStorage()}
}

func (s *publishingStorage) setScope(scope func(ctx context.Context, publish func())) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scope = scope
}

func (s *publishingStorage) Publishing(ctx context.Context, publish func()) {
	s.mu.Lock()
	scope := s.scope
	s.mu.Unlock()
	if scope == nil {
		publish()
		return
	}
	scope(ctx, publish)
}

// taskEnded reports whether any write of the commit is a terminal record of a task of kind (or of id, when kind is empty).
func taskEnded(writes []durable.StorageWrite, kind string, id durable.TaskId) bool {
	for _, write := range writes {
		task, ok := write.(durable.TaskWrite)
		if ok && task.Value.State.Status == durable.TaskTerminal && (kind == "" || task.Value.Kind == kind) && (id == 0 || task.Value.Id == id) {
			return true
		}
	}
	return false
}

// deferPublication holds the publication of the commit that ends a task of holdKind until the commit that ends a task of
// afterKind is published, then publishes it from a goroutine of its own: the held publication lands while the scheduler
// passes the first one asked for are planning, the order Pi's test provokes by releasing it from the pass's touch. When
// afterKind ended already, it publishes from its own goroutine at once.
func deferPublication(store *publishingStorage, holdKind, afterKind string) {
	var mu sync.Mutex
	var pending func()
	afterDone := false
	store.setScope(func(_ context.Context, publish func()) {
		writes := store.commitAt(store.commitCount() - 1)
		if taskEnded(writes, holdKind, 0) {
			mu.Lock()
			if afterDone {
				mu.Unlock()
				go publish()
				return
			}
			pending = publish
			mu.Unlock()
			return
		}
		publish()
		if taskEnded(writes, afterKind, 0) {
			mu.Lock()
			afterDone = true
			release := pending
			pending = nil
			mu.Unlock()
			if release != nil {
				go release()
			}
		}
	})
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

// A publication held back while a finalize pass plans other holders lands while the pass applies its decision: the check it
// asks for must stay for the next pass.
func TestFinalizePassKeepsAHolderCheckPublishedWhilePlanning(t *testing.T) {
	for run := range 25 {
		gate := deferred()
		heldChild := tkGated("test.held-child", gate)
		otherChild := tkGated("test.other-child", gate)
		held := holdsChildTask("test.held", heldChild)
		other := holdsChildTask("test.other", otherChild)
		store := newPublishingStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{heldChild.Erased(), otherChild.Erased(), held.Erased(), other.Erased()}, tkOptions{storage: store})
		otherId := tkStart(t, opened.root, other.Erased())
		heldId := tkStart(t, opened.root, held.Erased())
		opened.harness.Resume()
		eventually(t, func() bool {
			return taskStatusOf(t, opened.harness, otherId) == durable.TaskCompleting && taskStatusOf(t, opened.harness, heldId) == durable.TaskCompleting
		})
		flush()
		// Both children end together. The held child's publication is held until the other child's is published, which asks
		// for a finalize pass over its holder alone; the held child's terminal record is published as that pass plans.
		deferPublication(store, "test.held-child", "test.other-child")
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
				if child < 0 && taskEnded(writes, pair[0], 0) {
					child = index
				}
				if holder < 0 && taskEnded(writes, pair[1], 0) {
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

// A publication held back while a reservation pass plans lands while the pass applies its decision to drop the waiter: the
// waiter's candidacy, queued by the publication that ended the last task it waited on, must stay for the next pass.
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
		store := newPublishingStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{parent.Erased(), child.Erased(), waiter.Erased()}, tkOptions{storage: store})
		parentId := tkStart(t, opened.root, parent.Erased())
		opened.harness.Resume()
		eventually(t, func() bool { return taskStatusOf(t, opened.harness, parentId) == durable.TaskCompleting })
		waiterId := tkStart(t, opened.root, waiter.Erased())
		eventually(t, func() bool { return taskStatusOf(t, opened.harness, waiterId) == durable.TaskWaiting })
		flush()
		// The waiter waits on the parent and its child. The child's publication makes the waiter a candidate and asks for the
		// reconcile pass that finalizes the parent. The parent's terminal record is held until the child's publication is
		// done, and published as the reservation pass plans.
		deferPublication(store, "test.held-parent", "test.held-child")
		gate.resolve()
		eventually(t, func() bool { return taskStatusOf(t, opened.harness, waiterId) == durable.TaskWaiting })
		flush()
		// The waiter waits on the parent and its child. The child's publication makes the waiter a candidate and asks for the
		// reconcile pass that finalizes the parent. The parent's terminal record is held until the child's publication is
		// done, and published as the reservation pass plans.
		var mu sync.Mutex
		var pending func()
		released := make(chan struct{})
		var releasedOnce sync.Once
		store.setScope(func(_ context.Context, publish func()) {
			writes := store.commitAt(store.commitCount() - 1)
			mu.Lock()
			holdIt := pending == nil && taskEnded(writes, "test.held-parent", 0)
			if holdIt {
				pending = publish
			}
			mu.Unlock()
			if holdIt {
				return
			}
			publish()
			if taskEnded(writes, "test.held-child", 0) {
				mu.Lock()
				release := pending
				mu.Unlock()
				if release != nil {
					go func() {
						release()
						releasedOnce.Do(func() { close(released) })
					}()
				}
			}
		})
		gate.resolve()
		eventually(t, func() bool { return taskStatusOf(t, opened.harness, waiterId) == durable.TaskTerminal })
		mustClose(t, opened.harness)
	}
}
