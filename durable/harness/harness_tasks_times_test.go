// Ports the Pi 1.1.0 additions to packages/durable/test/harness-tasks.test.ts ("stamps startedAt at the first run and
// endedAt when terminal, keeping startedAt through a wait" and "pages the newest tasks first without scanning older
// ones") and packages/durable/test/harness-tasks-recovery.test.ts ("keeps a task's startedAt across close and reopen
// and stamps endedAt when it settles"). See harness_tasks_test.go for the Go mappings that apply to every case.

package harness

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// clockAt is a settable Harness clock.
type clockAt struct{ value atomic.Uint64 }

func (clock *clockAt) set(milliseconds float64) { clock.value.Store(uint64(milliseconds)) }
func (clock *clockAt) now() float64             { return float64(clock.value.Load()) }

func timesOf(record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) [3]any {
	started, ended := any(nil), any(nil)
	if record.StartedAt != nil {
		started = *record.StartedAt
	}
	if record.EndedAt != nil {
		ended = *record.EndedAt
	}
	return [3]any{record.Id, started, ended}
}

func TestTaskTimes(t *testing.T) {
	t.Run("stamps startedAt at the first run and endedAt when terminal, keeping startedAt through a wait", func(t *testing.T) {
		clock := &clockAt{}
		clock.set(1_000)
		gate := deferred()
		var on atomic.Pointer[[]durable.TaskId]
		type waiterState struct {
			Phase string `json:"phase"`
		}
		type waiterRecord = durable.RunningTask[durable.JsonValue, waiterState, durable.JsonValue]
		type waiterRuntime = durable.TaskRuntime[durable.JsonValue, waiterState, durable.JsonValue, any]
		waiter := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, waiterState, durable.JsonValue, any]{
			Name:    "test.timed-waiter",
			Version: 1,
			Initial: func(durable.JsonValue) waiterState { return waiterState{Phase: "wait"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, waiterState, durable.JsonValue, any]{
				"wait": func(ctx context.Context, _ waiterRecord, runtime waiterRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, waiterRecord) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
						return waitingState[waiterState, durable.JsonValue](waiterState{Phase: "resume"}, *on.Load(), durable.JoinAllSettled), nil
					})
				},
				"resume": func(ctx context.Context, _ waiterRecord, runtime waiterRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, waiterRecord) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
						return completed[waiterState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ waiterRecord, runtime waiterRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, waiterRecord) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
					return abortedWith[waiterState, durable.JsonValue]("test"), nil
				})
			},
		})
		held := tkGated("test.timed-held", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{waiter, held}, tkOptions{now: clock.now})
		heldId := tkStart(t, opened.root, held)
		on.Store(&[]durable.TaskId{heldId})
		waiterId := tkStart(t, opened.root, waiter)
		times := func() [][3]any {
			inspection, err := opened.harness.Inspect(testContext)
			if err != nil {
				t.Fatal(err)
			}
			found := [][3]any{}
			for _, task := range inspection.Tasks {
				found = append(found, timesOf(task.Record))
			}
			return found
		}
		expectTimes := func(want [][3]any) {
			t.Helper()
			if got := times(); !reflect.DeepEqual(got, want) {
				t.Fatalf("times %v, want %v", got, want)
			}
		}
		expectTimes([][3]any{{heldId, nil, nil}, {waiterId, nil, nil}})

		clock.set(2_000)
		opened.harness.Resume()
		eventually(t, func() bool {
			record, err := opened.harness.GetTask(testContext, waiterId)
			return err == nil && record != nil && record.State.Status == durable.TaskWaiting
		})
		expectTimes([][3]any{{heldId, 2_000.0, nil}, {waiterId, 2_000.0, nil}})

		// The waiter runs again at 5_000; its start stays the first run.
		clock.set(5_000)
		gate.resolve()
		settledWaiter, err := opened.harness.WaitForTask(testContext, waiterId)
		if err != nil {
			t.Fatal(err)
		}
		settledHeld := tkTaskRecord(t, opened.harness, heldId)
		if got := timesOf(settledHeld); got != [3]any{heldId, 2_000.0, 5_000.0} {
			t.Fatalf("held times %v", got)
		}
		if settledWaiter.StartedAt == nil || settledWaiter.EndedAt == nil || *settledWaiter.StartedAt != 2_000 || *settledWaiter.EndedAt != 5_000 {
			t.Fatalf("waiter receipt times %v %v", settledWaiter.StartedAt, settledWaiter.EndedAt)
		}
		mustClose(t, opened.harness)
	})

	t.Run("pages the newest tasks first without scanning older ones", func(t *testing.T) {
		idle := tkOneStep("test.idle", func(context.Context, stepRecord, stepRuntime) error { return nil })
		opened := tkOpenRoot(t, []durable.AnyTask{idle})
		ids := []int64{}
		for range 5 {
			ids = append(ids, int64(tkStart(t, opened.root, idle)))
		}
		descending := durable.ScanDescending
		first, err := durable.Commit(testContext, opened.harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return tx.ScanTasks(durable.TaskQuery{Order: &descending}, 2, nil)
		})
		if err != nil {
			t.Fatal(err)
		}
		taskIdsOf := func(items []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) []int64 {
			found := []int64{}
			for _, item := range items {
				found = append(found, int64(item.Id))
			}
			return found
		}
		if got := taskIdsOf(first.Items); !reflect.DeepEqual(got, []int64{ids[4], ids[3]}) {
			t.Fatalf("first page %v", got)
		}
		second, err := durable.Commit(testContext, opened.harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return tx.ScanTasks(durable.TaskQuery{}, 2, *first.Next)
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := taskIdsOf(second.Items); !reflect.DeepEqual(got, []int64{ids[2], ids[1]}) {
			t.Fatalf("second page %v, a cursor continues in its order", got)
		}
		mustClose(t, opened.harness)
	})

	t.Run("keeps a task's startedAt across close and reopen and stamps endedAt when it settles", func(t *testing.T) {
		path := sqlitePath(t)
		service := &transferService{}
		var interrupt atomic.Bool
		interrupt.Store(true)
		transfer := tkTransferTask(service, &interrupt)
		clock := &clockAt{}
		clock.set(2_000)

		first, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{transfer}, openTasksOptions{now: clock.now})
		id, err := durable.Commit(testContext, mustRoot(t, first, nil), func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, transfer, transferInput{Amount: 7}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		first.Resume()
		eventually(t, func() bool { return service.callCount() == 1 })
		clock.set(3_000)
		mustClose(t, first)

		clock.set(9_000)
		second, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{transfer}, openTasksOptions{now: clock.now})
		reopened := tkTaskRecord(t, second, id)
		if got := timesOf(reopened); reopened.State.Status != durable.TaskPending || got != [3]any{id, 2_000.0, nil} {
			t.Fatalf("reopened %s times %v, want pending started at 2000", reopened.State.Status, got)
		}
		second.Resume()
		receipt, err := second.WaitForTask(testContext, id)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.StartedAt == nil || receipt.EndedAt == nil || *receipt.StartedAt != 2_000 || *receipt.EndedAt != 9_000 {
			t.Fatalf("receipt times %v %v", receipt.StartedAt, receipt.EndedAt)
		}
		mustClose(t, second)
	})
}
