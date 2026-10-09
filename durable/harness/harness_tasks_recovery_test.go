// Ports packages/durable/test/harness-tasks-recovery.test.ts: task recovery, crash recovery, blocked tasks,
// definition handover, and Harness open. See harness_tasks_test.go for the Go mappings that apply to every case.

package harness

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

func tkOpenSqlite(t *testing.T, path string) durable.Storage {
	t.Helper()
	store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// crash simulates a crash during the held commit: it never reaches storage, and later commits proceed.
func (s *controlledStorage) crash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitGate = nil
}

// tkCreateIn creates a conversation-owned task in the root conversation of harness.
func tkCreateIn(t *testing.T, harness Harness, task durable.AnyTask) durable.TaskId {
	t.Helper()
	return tkStart(t, mustRoot(t, harness, nil), task)
}

func tkTaskRecord(t *testing.T, harness Harness, id durable.TaskId) durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	t.Helper()
	record, err := harness.GetTask(testContext, id)
	if err != nil || record == nil {
		t.Fatalf("GetTask(%d) %+v %v", id, record, err)
	}
	return *record
}

// tkExpectPending checks a stored task record: its status, abort mark, and (when given) phase.
func tkExpectState(t *testing.T, record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], status durable.TaskStatus, abortRequested bool) {
	t.Helper()
	if record.State.Status != status || record.AbortRequested != abortRequested {
		t.Fatalf("task %d is %s (abortRequested %v), want %s (abortRequested %v)", record.Id, record.State.Status, record.AbortRequested, status, abortRequested)
	}
}

func tkCheckpointPhase(record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) string {
	if record.State.Checkpoint == nil {
		return ""
	}
	return phaseName(*record.State.Checkpoint)
}

// transferService is a fake external service whose operations are idempotent by request key.
type transferService struct {
	mu      sync.Mutex
	applied map[string]int
	calls   int
}

func (service *transferService) apply(key string, amount int) int {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.calls++
	if service.applied == nil {
		service.applied = map[string]int{}
	}
	if _, ok := service.applied[key]; !ok {
		service.applied[key] = amount * 10
	}
	return service.applied[key]
}

func (service *transferService) callCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.calls
}

type transferInput struct {
	Amount int `json:"amount"`
}

type transferState struct {
	Phase string `json:"phase"`
	Key   string `json:"key,omitempty"`
}

type transferResult struct {
	Receipt int `json:"receipt"`
}

// tkTransferTask is the intent/effect/outcome task: prepare commits the intent, apply performs the effect and commits the outcome. While interrupt is set, the first apply blocks after the effect until the invocation is signalled.
func tkTransferTask(service *transferService, interrupt *atomic.Bool) durable.Task[transferInput, transferState, transferResult, any] {
	type runtimeType = durable.TaskRuntime[transferInput, transferState, transferResult, any]
	type recordType = durable.RunningTask[transferInput, transferState, transferResult]
	return durable.DefineTask(durable.TaskDefinition[transferInput, transferState, transferResult, any]{
		Name:    "test.transfer",
		Version: 1,
		Initial: func(transferInput) transferState { return transferState{Phase: "prepare"} },
		Phases: map[string]durable.PhaseHandler[transferInput, transferState, transferResult, any]{
			"prepare": func(ctx context.Context, task recordType, runtime runtimeType) error {
				if _, err := runtime.MemoCandidate(ctx, "requested", float64(task.Input.Amount)); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(durable.Tx, recordType) (*durable.NextTaskState[transferState, transferResult], error) {
					return runningState[transferState, transferResult](transferState{Phase: "apply", Key: fmt.Sprintf("transfer-%d", task.Id)}), nil
				})
			},
			"apply": func(ctx context.Context, task recordType, runtime runtimeType) error {
				receipt := service.apply(task.State.Checkpoint.Key, task.Input.Amount)
				if interrupt.CompareAndSwap(true, false) {
					return abortedBy(runtime.Signal())
				}
				return runtime.Commit(ctx, func(durable.Tx, recordType) (*durable.NextTaskState[transferState, transferResult], error) {
					return completed[transferState, transferResult](transferResult{Receipt: receipt}), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ recordType, runtime runtimeType) error {
			return runtime.Commit(ctx, func(durable.Tx, recordType) (*durable.NextTaskState[transferState, transferResult], error) {
				return &durable.NextTaskState[transferState, transferResult]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[transferResult]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

// tkVersioned is a versioned one-phase task that completes with result, optionally migrating older records.
func tkVersioned(version int, result string, migrate ...func(input, checkpoint durable.JsonValue, fromVersion int) (durable.JsonValue, stepState, error)) durable.Task[durable.JsonValue, stepState, durable.JsonValue, any] {
	definition := durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    "test.versioned",
		Version: version,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](result), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue](result), nil
			})
		},
	}
	if len(migrate) > 0 {
		definition.Migrate = func(input, checkpoint durable.JsonValue, fromVersion int) (durable.JsonValue, stepState, error) {
			return migrate[0](input, checkpoint, fromVersion)
		}
	}
	return durable.DefineTask(definition)
}

func TestTaskRecovery(t *testing.T) {
	t.Run("resumes an intent/effect/outcome task interrupted after its intent across close and reopen", func(t *testing.T) {
		path := sqlitePath(t)
		service := &transferService{}
		var interrupt atomic.Bool
		interrupt.Store(true)
		transfer := tkTransferTask(service, &interrupt)

		first, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{transfer})
		root := mustRoot(t, first, nil)
		id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, transfer, transferInput{Amount: 7}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		first.Resume()
		eventually(t, func() bool { return service.callCount() == 1 })
		mustClose(t, first)

		second, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{transfer})
		// Open reconciled running to pending and kept the checkpoint and memos; nothing ran yet.
		record := tkTaskRecord(t, second, id)
		tkExpectState(t, record, durable.TaskPending, false)
		if !reflect.DeepEqual(record.Memos, map[string]durable.JsonValue{"requested": float64(7)}) ||
			!reflect.DeepEqual(plainObject(*record.State.Checkpoint), map[string]any{"phase": "apply", "key": fmt.Sprintf("transfer-%d", id)}) {
			t.Fatalf("record %+v keeps neither memos nor the checkpoint", record)
		}
		if service.callCount() != 1 {
			t.Fatalf("%d calls before resume, want 1", service.callCount())
		}
		second.Resume()
		receipt, err := second.WaitForTask(testContext, id)
		if err != nil {
			t.Fatal(err)
		}
		expectOutcome(t, tkOutcome(t, receipt), outcomeCompleted(map[string]any{"receipt": float64(70)}))
		if service.callCount() != 2 || len(service.applied) != 1 {
			t.Fatalf("%d calls and %d applied, want 2 and 1", service.callCount(), len(service.applied))
		}
		mustClose(t, second)

		third, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{transfer})
		// toEqual: the stored record and the receipt have the same JSON members.
		expectSameJSON(t, tkTaskRecord(t, third, id), receipt)
		mustClose(t, third)
	})

	t.Run("resumes abort work after close at every direct-task abort stage", func(t *testing.T) {
		path := sqlitePath(t)
		log := &syncList[string]{}
		var blockAbort atomic.Bool
		blockAbort.Store(true)
		abortReached, runRelease := deferred(), deferred()
		type abortableState = stepState
		abortable := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, abortableState, durable.JsonValue, any]{
			Name:    "test.abortable",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"run": func(context.Context, stepRecord, stepRuntime) error {
					log.add("run")
					// Ignores the abort signal until released, so the mark is durable while the run is active.
					return runRelease.wait(testContext)
				},
			},
			Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				log.add("abort")
				if blockAbort.Load() {
					abortReached.resolve()
					return abortedBy(runtime.Signal())
				}
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("stop"), nil
				})
			},
		})
		tasks := []durable.AnyTask{abortable}

		// Stage 1: the mark is committed while the run invocation is still active.
		opened, _, _ := openTasks(t, tkOpenSqlite(t, path), tasks)
		id := tkCreateIn(t, opened, abortable)
		opened.Resume()
		eventually(t, func() bool { return log.len() == 1 })
		aborting := tkMarkDurably(t, opened, id)
		closing := make(chan error, 1)
		go func() { closing <- opened.Close(testContext) }()
		// The run must finish only once the close has sealed the scheduler, else the abort dispatches before it.
		eventually(t, func() bool { return opened.(*harnessImpl).tasks.isSealed() })
		runRelease.resolve()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		if result := <-aborting; result.err != nil || result.result != "marked" {
			t.Fatalf("AbortTask %+v", result)
		}
		if !reflect.DeepEqual(log.all(), []string{"run"}) {
			t.Fatalf("log %v, want [run]", log.all())
		}

		// Stage 2: reopen dispatches the abort invocation, never the run; close while it is active.
		opened, _, _ = openTasks(t, tkOpenSqlite(t, path), tasks)
		tkExpectState(t, tkTaskRecord(t, opened, id), durable.TaskPending, true)
		opened.Resume()
		if err := abortReached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened)
		if !reflect.DeepEqual(log.all(), []string{"run", "abort"}) {
			t.Fatalf("log %v, want [run abort]", log.all())
		}

		// Stage 3: a fresh abort invocation settles the task.
		blockAbort.Store(false)
		opened, _, _ = openTasks(t, tkOpenSqlite(t, path), tasks)
		opened.Resume()
		expectOutcome(t, tkWaitOutcome(t, opened, id), outcomeAborted("stop"))
		mustClose(t, opened)
		if !reflect.DeepEqual(log.all(), []string{"run", "abort", "abort"}) {
			t.Fatalf("log %v, want [run abort abort]", log.all())
		}

		// Stage 4: the terminal receipt survives reopen and nothing runs again.
		opened, _, _ = openTasks(t, tkOpenSqlite(t, path), tasks)
		opened.Resume()
		flush()
		if result, err := opened.AbortTask(testContext, id); err != nil || result != "terminal" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		mustClose(t, opened)
		if !reflect.DeepEqual(log.all(), []string{"run", "abort", "abort"}) {
			t.Fatalf("log %v, want [run abort abort]", log.all())
		}
	})
}

// tkCrashTask is a task whose run ignores its signal and whose abort handler blocks while blockAbort is set.
func tkCrashTask(log *syncList[string], blockAbort bool) stepTask {
	forever := func() error {
		select {}
	}
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    "test.crash",
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"run": func(context.Context, stepRecord, stepRuntime) error {
				log.add("run")
				return forever()
			},
		},
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			log.add("abort")
			if blockAbort {
				return forever()
			}
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue]("recovered"), nil
			})
		},
	})
}

// tkRecover opens a new Harness over the storage of a crashed one, which is abandoned without close: its held storage commit never lands, and its blocked handlers never return.
func tkRecover(t *testing.T, store *controlledStorage, log *syncList[string]) (Harness, durable.TaskId) {
	t.Helper()
	store.crash()
	harness, _, _ := openTasks(t, store, []durable.AnyTask{tkCrashTask(log, false)})
	kind := "test.crash"
	page, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 1, nil)
	})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("scan %+v %v", page, err)
	}
	return harness, page.Items[0].Id
}

func tkCrashedRun(t *testing.T, log *syncList[string]) (*controlledStorage, Harness, durable.TaskId) {
	t.Helper()
	store := newControlledStorage()
	crash := tkCrashTask(log, true)
	harness, _, _ := openTasks(t, store, []durable.AnyTask{crash})
	id := tkCreateIn(t, harness, crash)
	harness.Resume()
	eventually(t, func() bool { return log.len() == 1 })
	return store, harness, id
}

func TestTaskCrashRecovery(t *testing.T) {
	t.Run("crash while the mark commit is in storage: the run resumes", func(t *testing.T) {
		log := &syncList[string]{}
		store, harness, id := tkCrashedRun(t, log)
		held := store.holdCommits()
		go func() { _, _ = harness.AbortTask(testContext, id) }()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		recovered, _ := tkRecover(t, store, log)
		tkExpectState(t, tkTaskRecord(t, recovered, id), durable.TaskPending, false)
		recovered.Resume()
		eventually(t, func() bool { return log.len() == 2 })
		if !reflect.DeepEqual(log.all(), []string{"run", "run"}) {
			t.Fatalf("log %v, want [run run]", log.all())
		}
	})

	t.Run("crash after the mark before the run joins: only the abort handler runs", func(t *testing.T) {
		log := &syncList[string]{}
		store, harness, id := tkCrashedRun(t, log)
		// The run ignores its signal, so AbortTask never finishes joining it.
		var joined atomic.Bool
		go func() {
			_, _ = harness.AbortTask(testContext, id)
			joined.Store(true)
		}()
		for !tkTaskRecord(t, harness, id).AbortRequested {
			flush()
		}
		flush()
		if joined.Load() {
			t.Fatal("AbortTask joined a run that ignores its signal")
		}
		recovered, _ := tkRecover(t, store, log)
		tkExpectState(t, tkTaskRecord(t, recovered, id), durable.TaskPending, true)
		recovered.Resume()
		expectOutcome(t, tkWaitOutcome(t, recovered, id), outcomeAborted("recovered"))
		if !reflect.DeepEqual(log.all(), []string{"run", "abort"}) {
			t.Fatalf("log %v, want [run abort]", log.all())
		}
		mustClose(t, recovered)
	})

	t.Run("crash while the abort handler runs: a fresh abort invocation settles the task", func(t *testing.T) {
		log := &syncList[string]{}
		store := newControlledStorage()
		crash := tkCrashTask(log, true)
		harness, _, _ := openTasks(t, store, []durable.AnyTask{crash})
		id := tkCreateIn(t, harness, crash)
		if _, err := harness.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		eventually(t, func() bool { return log.len() == 1 })
		if !reflect.DeepEqual(log.all(), []string{"abort"}) {
			t.Fatalf("log %v, want [abort]", log.all())
		}
		if status := tkTaskRecord(t, harness, id).State.Status; status != durable.TaskRunning {
			t.Fatalf("task is %s, want running", status)
		}
		recovered, _ := tkRecover(t, store, log)
		recovered.Resume()
		expectOutcome(t, tkWaitOutcome(t, recovered, id), outcomeAborted("recovered"))
		if !reflect.DeepEqual(log.all(), []string{"abort", "abort"}) {
			t.Fatalf("log %v, want [abort abort]", log.all())
		}
		mustClose(t, recovered)
	})

	t.Run("crash while the abort outcome is in storage: the abort handler runs again", func(t *testing.T) {
		log := &syncList[string]{}
		store := newControlledStorage()
		reached, proceed := deferred(), deferred()
		crash := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.crash",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"run": func(context.Context, stepRecord, stepRuntime) error { return nil },
			},
			Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				log.add("abort")
				reached.resolve()
				if err := proceed.wait(testContext); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("lost"), nil
				})
			},
		})
		harness, _, _ := openTasks(t, store, []durable.AnyTask{crash})
		id := tkCreateIn(t, harness, crash)
		if _, err := harness.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		held := store.holdCommits()
		proceed.resolve()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		recovered, _ := tkRecover(t, store, log)
		recovered.Resume()
		expectOutcome(t, tkWaitOutcome(t, recovered, id), outcomeAborted("recovered"))
		if !reflect.DeepEqual(log.all(), []string{"abort", "abort"}) {
			t.Fatalf("log %v, want [abort abort]", log.all())
		}
		mustClose(t, recovered)
	})

	t.Run("crash after the terminal outcome: nothing runs again", func(t *testing.T) {
		log := &syncList[string]{}
		store := newControlledStorage()
		crash := tkCrashTask(log, false)
		harness, _, _ := openTasks(t, store, []durable.AnyTask{crash})
		id := tkCreateIn(t, harness, crash)
		if _, err := harness.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		tkWaitOutcome(t, harness, id)
		recovered, _ := tkRecover(t, store, log)
		recovered.Resume()
		flush()
		if !reflect.DeepEqual(log.all(), []string{"abort"}) {
			t.Fatalf("log %v, want [abort]", log.all())
		}
		state := tkTaskRecord(t, recovered, id).State
		reason := "recovered"
		if want := (durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted, Reason: &reason}}); !reflect.DeepEqual(state, want) {
			t.Fatalf("state %+v, want %+v", state, want)
		}
		mustClose(t, recovered)
	})

	t.Run("crash while the reservation commit is in storage: the task is still pending", func(t *testing.T) {
		log := &syncList[string]{}
		store := newControlledStorage()
		crash := tkCrashTask(log, false)
		harness, _, _ := openTasks(t, store, []durable.AnyTask{crash})
		id := tkCreateIn(t, harness, crash)
		held := store.holdCommits()
		harness.Resume()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		recovered, _ := tkRecover(t, store, log)
		if status := tkTaskRecord(t, recovered, id).State.Status; status != durable.TaskPending {
			t.Fatalf("task is %s, want pending", status)
		}
		if log.len() != 0 {
			t.Fatalf("log %v, want empty", log.all())
		}
		if _, err := recovered.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		recovered.Resume()
		tkWaitOutcome(t, recovered, id)
		if !reflect.DeepEqual(log.all(), []string{"abort"}) {
			t.Fatalf("log %v, want [abort]", log.all())
		}
		mustClose(t, recovered)
	})
}

func TestBlockedTasks(t *testing.T) {
	t.Run("keeps a task with a missing definition pending, and live for idle waits, until registration", func(t *testing.T) {
		v1 := tkVersioned(1, "v1")
		harness, registry, _ := openTasks(t, storage.NewMemoryStorage(), nil)
		id := tkCreateIn(t, harness, v1)
		harness.Resume()
		flush()
		if status := tkTaskRecord(t, harness, id).State.Status; status != durable.TaskPending {
			t.Fatalf("task is %s, want pending", status)
		}
		idle := make(chan error, 1)
		go func() { idle <- harness.WaitForIdle(testContext) }()
		flush()
		addTask(t, registry, v1)
		if err := tkWaitErr(t, idle); err != nil {
			t.Fatal(err)
		}
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted("v1"))
		mustClose(t, harness)
	})

	t.Run("keeps a task stored by a newer version pending until a fitting definition is registered", func(t *testing.T) {
		registry := CreateRegistry()
		addTask(t, registry, tkVersioned(1, "old"))
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), nil, openTasksOptions{registry: registry})
		id := tkCreateIn(t, harness, tkVersioned(2, "new"))
		harness.Resume()
		flush()
		if status := tkTaskRecord(t, harness, id).State.Status; status != durable.TaskPending {
			t.Fatalf("task is %s, want pending", status)
		}
		// The same extension name replaces the old one in place.
		addTask(t, registry, tkVersioned(2, "new"))
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted("new"))
		mustClose(t, harness)
	})

	t.Run("migrates at reservation, leaves the record unchanged when migration fails, and retries only for a new definition", func(t *testing.T) {
		path := sqlitePath(t)
		opened, _, _ := openTasks(t, tkOpenSqlite(t, path), nil)
		id := tkCreateIn(t, opened, tkVersioned(1, "v1"))
		mustClose(t, opened)

		registry := CreateRegistry()
		var failures atomic.Int32
		addTask(t, registry, tkVersioned(2, "v2", func(durable.JsonValue, durable.JsonValue, int) (durable.JsonValue, stepState, error) {
			failures.Add(1)
			return nil, stepState{}, errors.New("cannot migrate")
		}))
		harness, _, reports := openTasks(t, tkOpenSqlite(t, path), nil, openTasksOptions{registry: registry})
		harness.Resume()
		eventually(t, func() bool { return len(reports.all()) == 1 })
		// An unrelated registry change wakes the scheduler without retrying the same failed definition.
		addTool(t, registry, supportTool("unrelated"))
		flush()
		if failures.Load() != 1 || len(reports.all()) != 1 || !strings.Contains(reports.all()[0].Error(), "cannot migrate") {
			t.Fatalf("%d migration attempts and reports %v, want one of each", failures.Load(), reports.all())
		}
		record := tkTaskRecord(t, harness, id)
		tkExpectState(t, record, durable.TaskPending, false)
		if record.Version != 1 || tkCheckpointPhase(record) != "run" {
			t.Fatalf("record %+v, want version 1 at phase run", record)
		}

		migrations := &syncList[int]{}
		// The same extension name replaces the old one in place.
		addTask(t, registry, tkVersioned(2, "v2", func(_, checkpoint durable.JsonValue, fromVersion int) (durable.JsonValue, stepState, error) {
			migrations.add(fromVersion)
			return nil, stepState{Phase: phaseName(checkpoint)}, nil
		}))
		receipt, err := harness.WaitForTask(testContext, id)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Version != 2 {
			t.Fatalf("receipt version %d, want 2", receipt.Version)
		}
		expectOutcome(t, tkOutcome(t, receipt), outcomeCompleted("v2"))
		if !reflect.DeepEqual(migrations.all(), []int{1}) {
			t.Fatalf("migrations %v, want [1]", migrations.all())
		}
		mustClose(t, harness)
	})

	t.Run("blocks an older record whose newer definition has no migration", func(t *testing.T) {
		path := sqlitePath(t)
		opened, _, _ := openTasks(t, tkOpenSqlite(t, path), nil)
		id := tkCreateIn(t, opened, tkVersioned(1, "v1"))
		mustClose(t, opened)
		harness, _, reports := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{tkVersioned(2, "v2")})
		harness.Resume()
		eventually(t, func() bool { return len(reports.all()) == 1 })
		if !strings.Contains(reports.all()[0].Error(), "has no migration from 1") {
			t.Fatalf("report %v", reports.all()[0])
		}
		record := tkTaskRecord(t, harness, id)
		tkExpectState(t, record, durable.TaskPending, false)
		if record.Version != 1 {
			t.Fatalf("record version %d, want 1", record.Version)
		}
		mustClose(t, harness)
	})

	t.Run("settles an aborted blocked task as orphaned and retires its documents", func(t *testing.T) {
		type scratchState struct {
			N int `json:"n"`
		}
		scratch := durable.DefineDoc(durable.DocDefinition[scratchState]{
			CommonDocDefinition: durable.CommonDocDefinition[scratchState]{Kind: "test.orphan-scratch", Version: 1, Initial: func() scratchState { return scratchState{} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeTask},
		})
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), nil)
		root := mustRoot(t, harness, nil)
		id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			created, err := tx.CreateTaskErased(tkVersioned(1, "x"), nil, durable.TaskOptions{Ownership: conversationOwned})
			if err != nil {
				return 0, err
			}
			draft, err := durable.TxDoc[scratchState](tx, scratch, created)
			if err != nil {
				return 0, err
			}
			return created, draft.Set("n", float64(1))
		})
		if err != nil {
			t.Fatal(err)
		}
		// Before resume: the marking commit settles the blocked task directly.
		if result, err := harness.AbortTask(testContext, id); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		reason := "missing_task"
		want := durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeOrphaned, Reason: &reason}}
		if state := tkTaskRecord(t, harness, id).State; !reflect.DeepEqual(state, want) {
			t.Fatalf("state %+v, want %+v", state, want)
		}
		if retired, err := harness.SnapshotErased(testContext, scratch, id); err != nil || retired != nil {
			t.Fatalf("scratch document %v %v, want retired", retired, err)
		}
		mustClose(t, harness)
	})

	t.Run("orphans a marked task whose definition disappeared while its run was active", func(t *testing.T) {
		reached := deferred()
		running := tkOneStep("test.vanishing", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			reached.resolve()
			return abortedBy(runtime.Signal())
		}, func(context.Context, stepRuntime) error { return errors.New("must not run") })
		registry := CreateRegistry()
		registration := addTask(t, registry, running)
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), nil, openTasksOptions{registry: registry})
		id := tkCreateIn(t, harness, running)
		harness.Resume()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		registration.dispose()
		// An active run means the mark does not orphan directly; the scheduler orphans once the run has ended.
		if result, err := harness.AbortTask(testContext, id); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		reason := "missing_task"
		expectOutcome(t, tkWaitOutcome(t, harness, id), durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeOrphaned, Reason: &reason})
		mustClose(t, harness)
	})

	t.Run("orphans a reopened abort-marked task without a definition once scheduling resumes", func(t *testing.T) {
		path := sqlitePath(t)
		opened, _, _ := openTasks(t, tkOpenSqlite(t, path), []durable.AnyTask{tkVersioned(1, "x")})
		id := tkCreateIn(t, opened, tkVersioned(1, "x"))
		if result, err := opened.AbortTask(testContext, id); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		mustClose(t, opened)

		reopened, _, _ := openTasks(t, tkOpenSqlite(t, path), nil)
		tkExpectState(t, tkTaskRecord(t, reopened, id), durable.TaskPending, true)
		idle := make(chan error, 1)
		go func() { idle <- reopened.WaitForIdle(testContext) }()
		reopened.Resume()
		if err := tkWaitErr(t, idle); err != nil {
			t.Fatal(err)
		}
		reason := "missing_task"
		want := durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeOrphaned, Reason: &reason}}
		if state := tkTaskRecord(t, reopened, id).State; !reflect.DeepEqual(state, want) {
			t.Fatalf("state %+v, want %+v", state, want)
		}
		mustClose(t, reopened)
	})
}

type handoverState struct {
	Phase string `json:"phase"`
}

type handoverGates struct {
	a, b  *deferredGate
	onEnd func()
}

type handoverOptions struct {
	gates   handoverGates
	migrate func(input, checkpoint durable.JsonValue, fromVersion int) (durable.JsonValue, handoverState, error)
}

type (
	handoverRuntime = durable.TaskRuntime[durable.JsonValue, handoverState, durable.JsonValue, any]
	handoverRecord  = durable.RunningTask[durable.JsonValue, handoverState, durable.JsonValue]
	handoverTaskT   = durable.Task[durable.JsonValue, handoverState, durable.JsonValue, any]
)

func tkHandoverTask(label string, version int, log *syncList[string], options ...handoverOptions) handoverTaskT {
	var option handoverOptions
	if len(options) > 0 {
		option = options[0]
	}
	advance := func(phase string, gate *deferredGate, next string) durable.PhaseHandler[durable.JsonValue, handoverState, durable.JsonValue, any] {
		return func(ctx context.Context, _ handoverRecord, runtime handoverRuntime) error {
			log.add(label + ":" + phase + " start")
			if gate != nil {
				if err := gate.wait(testContext); err != nil {
					return err
				}
			}
			if err := runtime.Commit(ctx, func(durable.Tx, handoverRecord) (*durable.NextTaskState[handoverState, durable.JsonValue], error) {
				return runningState[handoverState, durable.JsonValue](handoverState{Phase: next}), nil
			}); err != nil {
				return err
			}
			// Leave room for a wrongly dispatched successor before this invocation ends.
			time.Sleep(10 * time.Millisecond)
			if option.gates.onEnd != nil {
				option.gates.onEnd()
			}
			log.add(label + ":" + phase + " end")
			return nil
		}
	}
	definition := durable.TaskDefinition[durable.JsonValue, handoverState, durable.JsonValue, any]{
		Name:    "test.handover",
		Version: version,
		Initial: func(durable.JsonValue) handoverState { return handoverState{Phase: "a"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, handoverState, durable.JsonValue, any]{
			"a": advance("a", option.gates.a, "b"),
			"b": advance("b", option.gates.b, "c"),
			"c": func(ctx context.Context, _ handoverRecord, runtime handoverRuntime) error {
				log.add(label + ":c")
				return runtime.Commit(ctx, func(durable.Tx, handoverRecord) (*durable.NextTaskState[handoverState, durable.JsonValue], error) {
					return completed[handoverState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ handoverRecord, runtime handoverRuntime) error {
			log.add(label + ":abort")
			return runtime.Commit(ctx, func(durable.Tx, handoverRecord) (*durable.NextTaskState[handoverState, durable.JsonValue], error) {
				return abortedWith[handoverState, durable.JsonValue](label), nil
			})
		},
	}
	if option.migrate != nil {
		definition.Migrate = option.migrate
	}
	return durable.DefineTask(definition)
}

type startedHandover struct {
	harness  Harness
	registry Registry
	reports  *reportLog
	old      installed
	id       durable.TaskId
}

func tkStartHandover(t *testing.T, log *syncList[string], gates handoverGates, store ...durable.Storage) startedHandover {
	t.Helper()
	registry := CreateRegistry()
	old := addTask(t, registry, tkHandoverTask("old", 1, log, handoverOptions{gates: gates}))
	var backing durable.Storage = storage.NewMemoryStorage()
	if len(store) > 0 {
		backing = store[0]
	}
	harness, _, reports := openTasks(t, backing, nil, openTasksOptions{registry: registry})
	id := tkCreateIn(t, harness, tkHandoverTask("old", 1, log))
	harness.Resume()
	eventually(t, func() bool { return log.len() == 1 })
	return startedHandover{harness: harness, registry: registry, reports: reports, old: old, id: id}
}

func TestDefinitionHandover(t *testing.T) {
	t.Run("hands over at the next phase boundary to a same-version replacement without overlap", func(t *testing.T) {
		log := &syncList[string]{}
		gate := deferred()
		started := tkStartHandover(t, log, handoverGates{a: gate})
		// The same extension name replaces the old one in place.
		addTask(t, started.registry, tkHandoverTask("new", 1, log))
		gate.resolve()
		tkWaitOutcome(t, started.harness, started.id)
		want := []string{"old:a start", "old:a end", "new:b start", "new:b end", "new:c"}
		if !reflect.DeepEqual(log.all(), want) {
			t.Fatalf("log %v, want %v", log.all(), want)
		}
		mustClose(t, started.harness)
	})

	t.Run("keeps the memos across a handover", func(t *testing.T) {
		log := &syncList[string]{}
		gate, reached := deferred(), deferred()
		type memoState struct {
			Phase string `json:"phase"`
		}
		type memoRuntime = durable.TaskRuntime[durable.JsonValue, memoState, string, any]
		type memoRecord = durable.RunningTask[durable.JsonValue, memoState, string]
		memoTask := func(label string) durable.Task[durable.JsonValue, memoState, string, any] {
			return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, memoState, string, any]{
				Name:    "test.handover-memo",
				Version: 1,
				Initial: func(durable.JsonValue) memoState { return memoState{Phase: "a"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, memoState, string, any]{
					"a": func(ctx context.Context, _ memoRecord, runtime memoRuntime) error {
						if _, err := runtime.MemoCandidate(ctx, "picked", label); err != nil {
							return err
						}
						reached.resolve()
						if err := gate.wait(testContext); err != nil {
							return err
						}
						return runtime.Commit(ctx, func(durable.Tx, memoRecord) (*durable.NextTaskState[memoState, string], error) {
							return runningState[memoState, string](memoState{Phase: "b"}), nil
						})
					},
					"b": func(ctx context.Context, _ memoRecord, runtime memoRuntime) error {
						// The candidate loses to the memo the old definition stored.
						picked, err := runtime.MemoCandidate(ctx, "picked", label)
						if err != nil {
							return err
						}
						log.add(fmt.Sprintf("%s:b %v", label, picked))
						return runtime.Commit(ctx, func(durable.Tx, memoRecord) (*durable.NextTaskState[memoState, string], error) {
							return completed[memoState, string](picked.(string)), nil
						})
					},
				},
				Abort: tkNoopAbort[durable.JsonValue, memoState, string, any],
			})
		}
		registry := CreateRegistry()
		addTask(t, registry, memoTask("old"))
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), nil, openTasksOptions{registry: registry})
		id := tkCreateIn(t, harness, memoTask("old"))
		harness.Resume()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		addTask(t, registry, memoTask("new"))
		gate.resolve()
		expectOutcome(t, tkWaitOutcome(t, harness, id), outcomeCompleted("old"))
		if !reflect.DeepEqual(log.all(), []string{"new:b old"}) {
			t.Fatalf("log %v, want [new:b old]", log.all())
		}
		mustClose(t, harness)
	})

	t.Run("hands over to a newer version with a migration", func(t *testing.T) {
		log := &syncList[string]{}
		gate := deferred()
		started := tkStartHandover(t, log, handoverGates{a: gate})
		// The same extension name replaces the old one in place.
		addTask(t, started.registry, tkHandoverTask("v2", 2, log, handoverOptions{migrate: func(durable.JsonValue, durable.JsonValue, int) (durable.JsonValue, handoverState, error) {
			return nil, handoverState{Phase: "c"}, nil
		}}))
		gate.resolve()
		receipt, err := started.harness.WaitForTask(testContext, started.id)
		if err != nil || receipt.Version != 2 {
			t.Fatalf("receipt %+v %v, want version 2", receipt, err)
		}
		want := []string{"old:a start", "old:a end", "v2:c"}
		if !reflect.DeepEqual(log.all(), want) {
			t.Fatalf("log %v, want %v", log.all(), want)
		}
		mustClose(t, started.harness)
	})

	t.Run("hands over to a newer version whose migration fails and leaves the task blocked", func(t *testing.T) {
		log := &syncList[string]{}
		gate := deferred()
		started := tkStartHandover(t, log, handoverGates{a: gate})
		// The same extension name replaces the old one in place.
		addTask(t, started.registry, tkHandoverTask("broken", 2, log, handoverOptions{migrate: func(durable.JsonValue, durable.JsonValue, int) (durable.JsonValue, handoverState, error) {
			return nil, handoverState{}, errors.New("broken migration")
		}}))
		gate.resolve()
		eventually(t, func() bool { return len(started.reports.all()) == 1 })
		flush()
		record := tkTaskRecord(t, started.harness, started.id)
		tkExpectState(t, record, durable.TaskPending, false)
		if record.Version != 1 || tkCheckpointPhase(record) != "b" {
			t.Fatalf("record %+v, want version 1 at phase b", record)
		}
		if want := []string{"old:a start", "old:a end"}; !reflect.DeepEqual(log.all(), want) {
			t.Fatalf("log %v, want %v", log.all(), want)
		}
		mustClose(t, started.harness)
	})

	t.Run("keeps running under the old definition when the replacement is missing or cannot take the task", func(t *testing.T) {
		log := &syncList[string]{}
		gateA, gateB := deferred(), deferred()
		started := tkStartHandover(t, log, handoverGates{a: gateA, b: gateB})
		started.old.dispose()
		gateA.resolve()
		eventually(t, func() bool { return slicesContains(log.all(), "old:b start") })
		// A newer definition without a migration cannot take the task either.
		addTask(t, started.registry, tkHandoverTask("incompatible", 2, log))
		gateB.resolve()
		tkWaitOutcome(t, started.harness, started.id)
		want := []string{"old:a start", "old:a end", "old:b start", "old:b end", "old:c"}
		if !reflect.DeepEqual(log.all(), want) {
			t.Fatalf("log %v, want %v", log.all(), want)
		}
		causes := []string{}
		for _, report := range started.reports.all() {
			withCause, ok := report.(interface{ Cause() string })
			if !ok {
				t.Fatalf("report %v carries no cause", report)
			}
			causes = append(causes, withCause.Cause())
		}
		if !reflect.DeepEqual(causes, []string{"missing_task", "incompatible_task"}) {
			t.Fatalf("report causes %v, want [missing_task incompatible_task]", causes)
		}
		mustClose(t, started.harness)
	})

	t.Run("rejects a runtime commit of the old invocation queued behind its handover commit", func(t *testing.T) {
		log := &syncList[string]{}
		gate := deferred()
		store := newControlledStorage()
		heldGate := &syncValue[gate2]{}
		oldRuntime := &syncValue[handoverRuntime]{}
		var harnessRef atomic.Pointer[Harness]
		registry := CreateRegistry()
		old := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, handoverState, durable.JsonValue, any]{
			Name:    "test.handover",
			Version: 1,
			Initial: func(durable.JsonValue) handoverState { return handoverState{Phase: "a"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, handoverState, durable.JsonValue, any]{
				"a": func(ctx context.Context, task handoverRecord, runtime handoverRuntime) error {
					oldRuntime.put(runtime)
					if err := gate.wait(testContext); err != nil {
						return err
					}
					if err := runtime.Commit(ctx, func(durable.Tx, handoverRecord) (*durable.NextTaskState[handoverState, durable.JsonValue], error) {
						return runningState[handoverState, durable.JsonValue](handoverState{Phase: "b"}), nil
					}); err != nil {
						return err
					}
					// Hold the line with an unrelated commit so the handover commit queues behind it.
					held := store.holdCommits()
					heldGate.put(held)
					tkAppendBlocker(*harnessRef.Load(), task.ConversationId)
					return held.entered.wait(testContext)
				},
				"b": func(context.Context, handoverRecord, handoverRuntime) error { return nil },
				"c": func(context.Context, handoverRecord, handoverRuntime) error { return nil },
			},
			Abort: tkNoopAbort[durable.JsonValue, handoverState, durable.JsonValue, any],
		})
		addTask(t, registry, old)
		harness, _, _ := openTasks(t, store, nil, openTasksOptions{registry: registry})
		harnessRef.Store(&harness)
		id := tkCreateIn(t, harness, old)
		harness.Resume()
		eventually(t, oldRuntime.isSet)
		// The same extension name replaces the old one in place.
		addTask(t, registry, tkHandoverTask("new", 1, log))
		gate.resolve()
		eventually(t, heldGate.isSet)
		held, _ := heldGate.get()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		flush()
		flush()
		// Queued behind the handover commit while the invocation has not ended yet.
		runtime, _ := oldRuntime.get()
		late := make(chan error, 1)
		go func() {
			late <- runtime.Commit(testContext, func(durable.Tx, handoverRecord) (*durable.NextTaskState[handoverState, durable.JsonValue], error) {
				return completed[handoverState, durable.JsonValue](nil), nil
			})
		}()
		flush()
		held.release()
		tkContainsError(t, tkWaitErr(t, late), "invocation has ended")
		tkWaitOutcome(t, harness, id)
		want := []string{"new:b start", "new:b end", "new:c"}
		if !reflect.DeepEqual(log.all(), want) {
			t.Fatalf("log %v, want %v", log.all(), want)
		}
		mustClose(t, harness)
	})

	t.Run("preserves an abort mark that races the handover commit; the new definition aborts", func(t *testing.T) {
		log := &syncList[string]{}
		gate := deferred()
		store := newControlledStorage()
		heldGate := &syncValue[gate2]{}
		// The progress commit landed; hold the next commit, the handover, and queue the abort mark behind it.
		gates := handoverGates{a: gate, onEnd: func() {
			if !heldGate.isSet() {
				heldGate.put(store.holdCommits())
			}
		}}
		started := tkStartHandover(t, log, gates, store)
		// The same extension name replaces the old one in place.
		addTask(t, started.registry, tkHandoverTask("new", 1, log))
		gate.resolve()
		eventually(t, heldGate.isSet)
		held, _ := heldGate.get()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		aborting := make(chan tkAbortResult, 1)
		go func() {
			result, err := started.harness.AbortTask(testContext, started.id)
			aborting <- tkAbortResult{result: result, err: err}
		}()
		flush()
		held.release()
		if result := <-aborting; result.err != nil || result.result != "marked" {
			t.Fatalf("AbortTask %+v", result)
		}
		expectOutcome(t, tkWaitOutcome(t, started.harness, started.id), outcomeAborted("new"))
		states := []string{}
		for index := range store.commitCount() {
			for _, write := range store.commitAt(index) {
				if task, ok := write.(durable.TaskWrite); ok && task.Value.Id == started.id {
					entry := string(task.Value.State.Status)
					if task.Value.AbortRequested {
						entry += "+mark"
					}
					states = append(states, entry)
				}
			}
		}
		// created, reserved, progress, handover, mark, abort reservation, aborted
		want := []string{"pending", "running", "running", "pending", "pending+mark", "running+mark", "terminal+mark"}
		if !reflect.DeepEqual(states, want) {
			t.Fatalf("task writes %v, want %v", states, want)
		}
		if want := []string{"old:a start", "old:a end", "new:abort"}; !reflect.DeepEqual(log.all(), want) {
			t.Fatalf("log %v, want %v", log.all(), want)
		}
		mustClose(t, started.harness)
	})
}

// gate2 is the held-call gate of controlledStorage, named for syncValue instantiations.
type gate2 = gate

func slicesContains(items []string, item string) bool {
	return slices.Contains(items, item)
}

// Pi source: packages/durable/src/storage/memory.ts
// mutation-checked: zeroing the results of MemoryStorage.Task fails it
func TestTaskRecoveryHarnessOpen(t *testing.T) {
	t.Run("releases its registry subscription and closes the Session when open fails", func(t *testing.T) {
		store := newControlledStorage()
		log := &syncList[string]{}
		stuck := tkOneStep("test.stuck", func(context.Context, stepRecord, stepRuntime) error {
			log.add("run")
			select {}
		})
		// Leave a running task behind so open has a reconciliation commit to fail.
		first, _, _ := openTasks(t, store, []durable.AnyTask{stuck})
		tkCreateIn(t, first, stuck)
		first.Resume()
		eventually(t, func() bool { return log.len() == 1 })

		registry := CreateRegistry()
		reader := countingReader(registry)
		store.failNextCommit(errors.New("disk full"))
		_, err := OpenHarness(testContext, store, HarnessOptions{Models: ai.CreateModels(), Registry: reader})
		tkContainsError(t, err, "disk full")
		if reader.subscriptions() != 0 {
			t.Fatalf("%d registry subscriptions after a failed open, want 0", reader.subscriptions())
		}
		if _, err := store.Task(testContext, 1); err == nil {
			t.Fatal("storage still open after a failed open")
		}
	})
}
