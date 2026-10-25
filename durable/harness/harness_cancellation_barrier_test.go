// Ports packages/durable/test/harness-cancellation-barrier.test.ts: work below a cancelled owner, and the restart mark
// of a task abandoned after a restart.

package harness

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// holdMarks holds every commit that abort-marks its target, while set, until released.
type holdMarks struct {
	*storage.MemoryStorage
	mu      sync.Mutex
	target  durable.TaskId
	held    *deferredGate
	release *deferredGate
}

func (store *holdMarks) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	store.mu.Lock()
	target := store.target
	store.mu.Unlock()
	if target != 0 && slices.ContainsFunc(writes, func(write durable.StorageWrite) bool {
		task, ok := write.(durable.TaskWrite)
		return ok && task.Value.Id == target && task.Value.AbortRequested
	}) {
		store.held.resolve()
		_ = store.release.wait(testContext)
	}
	return store.MemoryStorage.Commit(ctx, writes, adopt)
}

// cbCreate creates tasks in a Harness that never resumes, closes it, and returns the store for a later Harness: the
// tasks are live, and found at open by the next one.
func cbCreate(t *testing.T, fixtures *ownFixtures, create func(root Conversation, tx durable.Tx) error) *storage.MemoryStorage {
	t.Helper()
	store := storage.NewMemoryStorage()
	harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
	root := mustRoot(t, harness, nil)
	if _, err := root.Commit(testContext, func(tx durable.Tx) (any, error) { return nil, create(root, tx) }); err != nil {
		t.Fatal(err)
	}
	mustClose(t, harness)
	store.Reopen()
	return store
}

func cbTask(fixtures *ownFixtures, tx durable.Tx, name string, options durable.TaskOptions) (durable.TaskId, error) {
	return durable.CreateTask(tx, fixtures.hold, holdInput{Name: name}, options)
}

func cbOwnedBy(owner durable.TaskId, abandon bool) durable.TaskOptions {
	return durable.TaskOptions{Ownership: scaleOwnedBy(owner), AbandonOnRestart: &abandon}
}

func cbRecord(t *testing.T, store *storage.MemoryStorage, id durable.TaskId) anyTaskRecord {
	t.Helper()
	record, err := store.Task(testContext, id)
	if err != nil || record == nil {
		t.Fatalf("Task(%d) %v %v", id, record, err)
	}
	return *record
}

func cbReason(t *testing.T, harness Harness, id durable.TaskId) durable.TaskAbortReason {
	t.Helper()
	return tkTaskRecord(t, harness, id).AbortReason
}

func TestWorkBelowACancelledOwner(t *testing.T) {
	// harness-cancellation-barrier.test.ts:39 it("does not start its next phase while the cascade that marks it has not committed")
	t.Run("does not start its next phase while the cascade that marks it has not committed", func(t *testing.T) {
		store := &holdMarks{MemoryStorage: storage.NewMemoryStorage(), held: deferred(), release: deferred()}
		entered, advance := deferred(), deferred()
		var mu sync.Mutex
		ranNext := false
		type none = struct{}
		child := durable.DefineTask(durable.TaskDefinition[none, stepState, durable.JsonValue, any]{
			Name:    "test.child",
			Version: 1,
			Initial: func(none) stepState { return stepState{Phase: "first"} },
			Phases: map[string]durable.PhaseHandler[none, stepState, durable.JsonValue, any]{
				"first": func(ctx context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
					entered.resolve()
					_ = advance.wait(testContext)
					return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[none, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &stepState{Phase: "next"}}, nil
					})
				},
				"next": func(_ context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
					mu.Lock()
					ranNext = true
					mu.Unlock()
					return abortedBy(runtime.Signal())
				},
			},
			Abort: func(ctx context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[none, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue](""), nil
				})
			},
		})
		fixtures := newOwnFixtures()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased(), child.Erased()})
		root := mustRoot(t, harness, nil)
		parent := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return cbTask(fixtures, tx, "parent", durable.TaskOptions{Ownership: conversationOwned})
		}))
		childId := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, child, none{}, durable.TaskOptions{Ownership: scaleOwnedBy(parent)})
		}))
		store.mu.Lock()
		store.target = childId
		store.mu.Unlock()
		harness.Resume()
		_ = entered.wait(testContext)
		aborting := goCall(func() error { _, err := harness.AbortTask(testContext, parent); return err })
		_ = store.held.wait(testContext)
		// The parent's request is durable, the cascade's mark of the child is not: its phase must not advance.
		advance.resolve()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		advanced := ranNext
		mu.Unlock()
		if advanced {
			t.Fatal("the child ran its next phase before the cascade marked it")
		}
		store.release.resolve()
		ownExpectOutcome(t, harness, childId, durable.OutcomeAborted)
		if err := aborting.wait(t); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		advanced = ranNext
		mu.Unlock()
		if advanced {
			t.Fatal("the child ran its next phase after it was marked")
		}
		mustClose(t, harness)
	})

	// harness-cancellation-barrier.test.ts:94 it("lets a failFast request in the same pass override a restart mark")
	t.Run("lets a failFast request in the same pass override a restart mark", func(t *testing.T) {
		fixtures := newOwnFixtures()
		var parent, failed, child durable.TaskId
		store := cbCreate(t, fixtures, func(root Conversation, tx durable.Tx) (err error) {
			if parent, err = cbTask(fixtures, tx, "parent", durable.TaskOptions{Ownership: conversationOwned}); err != nil {
				return err
			}
			if failed, err = cbTask(fixtures, tx, "failed", cbOwnedBy(parent, false)); err != nil {
				return err
			}
			child, err = cbTask(fixtures, tx, "child", cbOwnedBy(parent, false))
			return err
		})
		// A restart-abandoned owner that waits failFast on a failed child and a live one.
		owner, failedRecord := cbRecord(t, store, parent), cbRecord(t, store, failed)
		reason := durable.AbortedByRestart
		owner.AbortRequested, owner.AbortReason = true, reason
		owner.State = anyTaskState{Status: durable.TaskWaiting, Checkpoint: owner.State.Checkpoint, On: []durable.TaskId{failed, child}, Policy: durable.JoinFailFast}
		failedRecord.State = anyTaskState{Status: durable.TaskTerminal, Outcome: &anyOutcome{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "failed"}}}
		if _, err := store.Commit(testContext, []durable.StorageWrite{durable.TaskWrite{Value: owner}, durable.TaskWrite{Value: failedRecord}}, func(durable.Seq) {}); err != nil {
			t.Fatal(err)
		}
		// The child's definition is missing: a restart mark would keep it waiting, a request orphans it.
		harness, _, _ := openTasks(t, store, nil)
		harness.Resume()
		eventually(t, func() bool { return tkTaskRecord(t, harness, child).State.Status == durable.TaskTerminal })
		state := tkTaskRecord(t, harness, child).State
		if state.Outcome == nil || state.Outcome.Status != durable.OutcomeOrphaned || state.Outcome.Reason == nil || *state.Outcome.Reason != "missing_task" {
			t.Fatalf("child state %+v, want orphaned: missing_task", state)
		}
		mustClose(t, harness)
	})

	// harness-cancellation-barrier.test.ts:146 it("upgrades restart marks down the tree when an abort is requested above them")
	t.Run("upgrades restart marks down the tree when an abort is requested above them", func(t *testing.T) {
		fixtures := newOwnFixtures()
		var top, middle, leaf durable.TaskId
		store := cbCreate(t, fixtures, func(root Conversation, tx durable.Tx) (err error) {
			if top, err = cbTask(fixtures, tx, "top", durable.TaskOptions{Ownership: conversationOwned}); err != nil {
				return err
			}
			if middle, err = cbTask(fixtures, tx, "middle", cbOwnedBy(top, true)); err != nil {
				return err
			}
			leaf, err = durable.CreateTask(tx, ownUnregistered(), holdInput{Name: "leaf"}, cbOwnedBy(middle, false))
			return err
		})
		// Only the hold task is registered: the leaf waits for its definition under the restart cascade.
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		harness.Resume()
		eventually(t, func() bool {
			reason := cbReason(t, harness, leaf)
			return reason == durable.AbortedByRestart
		})
		eventually(t, func() bool {
			inspection, err := harness.Inspect(testContext)
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range inspection.Tasks {
				if task.Record.Id == leaf {
					return task.State.Kind == "blocked"
				}
			}
			return false
		})
		if status := tkTaskRecord(t, harness, leaf).State.Status; status == durable.TaskTerminal {
			t.Fatal("the leaf ended while its definition was missing")
		}
		// A request on the top task reaches the leaf two levels down, which is then orphaned.
		if _, err := harness.AbortTask(testContext, top); err != nil {
			t.Fatal(err)
		}
		ownExpectOutcome(t, harness, leaf, durable.OutcomeOrphaned)
		ownExpectOutcome(t, harness, middle, durable.OutcomeAborted)
		ownExpectOutcome(t, harness, top, durable.OutcomeAborted)
		mustClose(t, harness)
	})

	// harness-cancellation-barrier.test.ts:190 it("marks a flagged task that holds its outcome, aborting its owned work, and keeps the outcome")
	t.Run("marks a flagged task that holds its outcome, aborting its owned work, and keeps the outcome", func(t *testing.T) {
		fixtures := newOwnFixtures()
		var holder, child durable.TaskId
		store := cbCreate(t, fixtures, func(root Conversation, tx durable.Tx) (err error) {
			if holder, err = cbTask(fixtures, tx, "holder", durable.TaskOptions{Ownership: conversationOwned, AbandonOnRestart: new(true)}); err != nil {
				return err
			}
			child, err = cbTask(fixtures, tx, "child", cbOwnedBy(holder, false))
			return err
		})
		record := cbRecord(t, store, holder)
		record.State = anyTaskState{Status: durable.TaskCompleting, Outcome: &anyOutcome{Status: durable.OutcomeCompleted}}
		if _, err := store.Commit(testContext, []durable.StorageWrite{durable.TaskWrite{Value: record}}, func(durable.Seq) {}); err != nil {
			t.Fatal(err)
		}
		harness, _, _ := openTasks(t, store, []durable.AnyTask{fixtures.hold.Erased()})
		harness.Resume()
		ownExpectOutcome(t, harness, child, durable.OutcomeAborted)
		ownExpectOutcome(t, harness, holder, durable.OutcomeCompleted)
		if reason := cbReason(t, harness, holder); reason != durable.AbortedByRestart {
			t.Fatalf("holder abort reason %v, want restart", reason)
		}
		mustClose(t, harness)
	})
}

// markRecorder records, in commit order, the abort reasons written with the abort mark of its target.
type markRecorder struct {
	durable.Storage
	mu      sync.Mutex
	target  durable.TaskId
	reasons []durable.TaskAbortReason
}

func (store *markRecorder) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	store.mu.Lock()
	for _, write := range writes {
		if task, ok := write.(durable.TaskWrite); ok && task.Value.Id == store.target && task.Value.AbortRequested {
			store.reasons = append(store.reasons, task.Value.AbortReason)
		}
	}
	store.mu.Unlock()
	return store.Storage.Commit(ctx, writes, adopt)
}

// harness-scheduler-indexes.test.ts:82 it("takes a cascade's reason from the nearest cancelling owner")
func TestACascadeTakesItsReasonFromTheNearestCancellingOwner(t *testing.T) {
	// top (abort requested) -> middle (restart-marked) -> leaf: the leaf gets middle's restart first, as the nearest
	// cancelling owner, and the request only once middle carries it.
	fixtures := newOwnFixtures()
	path := filepath.Join(t.TempDir(), "session.sqlite")
	kernel := session.CreateSession(openSqlite(t, path))
	type chain struct{ top, middle, leaf durable.TaskId }
	ids, err := durable.Commit(testContext, kernel, func(tx durable.Tx) (ids chain, err error) {
		conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			return ids, err
		}
		if ids.top, err = cbTask(fixtures, tx, "top", durable.TaskOptions{Ownership: conversationOwned, ConversationId: &conversation.Id}); err != nil {
			return ids, err
		}
		if ids.middle, err = cbTask(fixtures, tx, "middle", durable.TaskOptions{Ownership: scaleOwnedBy(ids.top)}); err != nil {
			return ids, err
		}
		ids.leaf, err = cbTask(fixtures, tx, "leaf", durable.TaskOptions{Ownership: scaleOwnedBy(ids.middle)})
		return ids, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Commit(testContext, func(tx durable.Tx) (any, error) {
		top, err := tx.Task(ids.top)
		if err != nil {
			return nil, err
		}
		middle, err := tx.Task(ids.middle)
		if err != nil {
			return nil, err
		}
		setTask := tx.(*session.Transaction).SetTask
		marked := *top
		marked.AbortRequested = true
		if err := setTask(marked); err != nil {
			return nil, err
		}
		marked = *middle
		marked.AbortRequested, marked.AbortReason = true, durable.AbortedByRestart
		return nil, setTask(marked)
	}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.Close(testContext); err != nil {
		t.Fatal(err)
	}
	// Record every mark of the leaf, in commit order.
	recorder := &markRecorder{Storage: openSqlite(t, path), target: ids.leaf}
	harness, _, _ := openTasks(t, recorder, []durable.AnyTask{fixtures.hold.Erased()})
	harness.Resume()
	for _, id := range []durable.TaskId{ids.leaf, ids.middle, ids.top} {
		tkWaitOutcome(t, harness, id)
	}
	recorder.mu.Lock()
	reasons := slices.Clone(recorder.reasons)
	recorder.mu.Unlock()
	if len(reasons) == 0 || reasons[0] != durable.AbortedByRestart {
		t.Fatalf("the leaf's first mark carries %v, want restart", reasons)
	}
	mustClose(t, harness)
}
