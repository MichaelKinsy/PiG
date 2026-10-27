// Ports packages/durable/test/harness-tasks.test.ts: task phases, runtime, scheduling, abort, and close.
//
// Go mappings forced by language mechanics. A TypeScript test that runs two calls "at once" with Promise.all or
// void-calls relies on the event loop's FIFO queueing; Go goroutines have no such order, so the tests either wait for
// the first call to reach storage before starting the second, or assert only what the order cannot change. A call
// whose synchronous part upstream relies on (a waitForTask() that has taken its place on the Session line before the
// test resolves a gate) runs through callAsync. Each such site says so. Abort reasons are context causes: the
// scheduler's signal is context.Canceled where upstream's is an AbortError.

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

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// syncList is a slice that handlers on several goroutines append to.
type syncList[T any] struct {
	mu    sync.Mutex
	items []T
}

func (list *syncList[T]) add(item T) {
	list.mu.Lock()
	defer list.mu.Unlock()
	list.items = append(list.items, item)
}

func (list *syncList[T]) all() []T {
	list.mu.Lock()
	defer list.mu.Unlock()
	return append([]T(nil), list.items...)
}

func (list *syncList[T]) len() int {
	list.mu.Lock()
	defer list.mu.Unlock()
	return len(list.items)
}

// syncValue is a value that a handler sets and the test reads.
type syncValue[T any] struct {
	mu    sync.Mutex
	value T
	set   bool
}

func (cell *syncValue[T]) put(value T) {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	cell.value, cell.set = value, true
}

func (cell *syncValue[T]) get() (T, bool) {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	return cell.value, cell.set
}

func (cell *syncValue[T]) isSet() bool {
	_, ok := cell.get()
	return ok
}

type stepRuntimeOf[R any] = durable.TaskRuntime[durable.JsonValue, stepState, R, any]
type stepRecordOf[R any] = durable.RunningTask[durable.JsonValue, stepState, R]

// tkOneStep is a one-phase task; the default abort handler settles aborted with reason "test".
func tkOneStep[R any](name string, run func(ctx context.Context, task stepRecordOf[R], runtime stepRuntimeOf[R]) error, abort ...func(ctx context.Context, runtime stepRuntimeOf[R]) error) durable.Task[durable.JsonValue, stepState, R, any] {
	abortHandler := func(ctx context.Context, runtime stepRuntimeOf[R]) error {
		return runtime.Commit(ctx, func(durable.Tx, stepRecordOf[R]) (*durable.NextTaskState[stepState, R], error) {
			return abortedWith[stepState, R]("test"), nil
		})
	}
	if len(abort) > 0 {
		abortHandler = abort[0]
	}
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, R, any]{
		Name:    name,
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, R, any]{
			"run": func(ctx context.Context, task stepRecordOf[R], runtime stepRuntimeOf[R]) error {
				return run(ctx, task, runtime)
			},
		},
		Abort: func(ctx context.Context, _ stepRecordOf[R], runtime stepRuntimeOf[R]) error {
			return abortHandler(ctx, runtime)
		},
	})
}

// tkGated is a one-phase task that waits for gate and completes with null.
func tkGated(name string, gate *deferredGate) stepTask {
	return tkOneStep(name, func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		if err := gate.wait(ctx); err != nil {
			return err
		}
		return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
			return completed[stepState, durable.JsonValue](nil), nil
		})
	})
}

func tkComplete(ctx context.Context, runtime stepRuntime) error {
	return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
		return completed[stepState, durable.JsonValue](nil), nil
	})
}

var conversationOwned = durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}

// tkStart creates a conversation-owned task of task in conversation.
func tkStart(t *testing.T, conversation Conversation, task durable.AnyTask, background ...bool) durable.TaskId {
	t.Helper()
	id, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.TaskId, error) {
		return tx.CreateTaskErased(task, nil, durable.TaskOptions{Ownership: conversationOwned, Background: len(background) > 0 && background[0]})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type tkOptions struct {
	storage  durable.Storage
	now      func() float64
	settings func() *HarnessSettings
	onWake   func(Wake)
	setTimer durable.SetTimer
	// telemetry is HarnessOptions.Telemetry.
	telemetry telemetry.TelemetryContext
}

type tkOpened struct {
	harness  Harness
	registry Registry
	reports  *reportLog
	root     Conversation
}

func tkOpenRoot(t *testing.T, tasks []durable.AnyTask, options ...tkOptions) tkOpened {
	t.Helper()
	var option tkOptions
	if len(options) > 0 {
		option = options[0]
	}
	store := option.storage
	if store == nil {
		store = storage.NewMemoryStorage()
	}
	harness, registry, reports := openTasks(t, store, tasks, openTasksOptions{now: option.now, settings: option.settings, onWake: option.onWake, setTimer: option.setTimer, telemetry: option.telemetry})
	return tkOpened{harness: harness, registry: registry, reports: reports, root: mustRoot(t, harness, nil)}
}

// tkMarkDurably starts AbortTask and returns once its mark is durable, before it has joined the run.
func tkMarkDurably(t *testing.T, harness Harness, id durable.TaskId) <-chan tkAbortResult {
	t.Helper()
	aborting := make(chan tkAbortResult, 1)
	go func() {
		result, err := harness.AbortTask(testContext, id)
		aborting <- tkAbortResult{result: result, err: err}
	}()
	for {
		record, err := harness.GetTask(testContext, id)
		if err != nil {
			t.Fatal(err)
		}
		if record != nil && record.AbortRequested {
			return aborting
		}
		flush()
	}
}

type tkAbortResult struct {
	result string
	err    error
}

func tkOutcome(t *testing.T, receipt durable.SettledTask[durable.JsonValue]) durable.TaskOutcome[durable.JsonValue] {
	t.Helper()
	if receipt.State.Status != durable.TaskTerminal || receipt.State.Outcome == nil {
		t.Fatalf("task %d is %s, not terminal", receipt.Id, receipt.State.Status)
	}
	return *receipt.State.Outcome
}

func tkWaitOutcome(t *testing.T, harness Harness, id durable.TaskId) durable.TaskOutcome[durable.JsonValue] {
	t.Helper()
	receipt, err := harness.WaitForTask(testContext, id)
	if err != nil {
		t.Fatal(err)
	}
	return tkOutcome(t, receipt)
}

func outcomeCompleted(result durable.JsonValue) durable.TaskOutcome[durable.JsonValue] {
	return durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}
}

func outcomeFaulted(message string) durable.TaskOutcome[durable.JsonValue] {
	return durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFaulted, Error: &durable.TaskOutcomeError{Message: message}}
}

func outcomeAborted(reason string) durable.TaskOutcome[durable.JsonValue] {
	return durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted, Reason: &reason}
}

func expectOutcome(t *testing.T, got, want durable.TaskOutcome[durable.JsonValue]) {
	t.Helper()
	// toEqual: the outcomes have the same JSON members, whatever form (map or ordered object) a result's objects take.
	if !reflect.DeepEqual(jsonOf(t, got), jsonOf(t, want)) {
		t.Fatalf("outcome %s, want %s", describeOutcome(got), describeOutcome(want))
	}
}

func describeOutcome(outcome durable.TaskOutcome[durable.JsonValue]) string {
	text := string(outcome.Status)
	if outcome.Result != nil {
		text += fmt.Sprintf(" result=%v", *outcome.Result)
	}
	if outcome.Error != nil {
		text += fmt.Sprintf(" error=%q", outcome.Error.Message)
	}
	if outcome.Reason != nil {
		text += fmt.Sprintf(" reason=%q", *outcome.Reason)
	}
	return text
}

func tkContainsError(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error %v does not contain %q", err, fragment)
	}
}

// tkRuntimeIdentity returns the scheduler's runtime behind a typed task runtime, whose wrapper is rebuilt per phase.
func tkRuntimeIdentity(runtime any) any {
	value := reflect.ValueOf(runtime)
	if value.Kind() == reflect.Struct {
		if inner := value.FieldByName("ErasedTaskRuntime"); inner.IsValid() {
			return inner.Interface()
		}
	}
	return runtime
}

func tkExpectPanic(t *testing.T, fragment string, call func()) {
	t.Helper()
	defer func() {
		t.Helper()
		recovered := recover()
		if recovered == nil {
			t.Fatalf("no panic, want %q", fragment)
		}
		if !strings.Contains(fmt.Sprint(recovered), fragment) {
			t.Fatalf("panic %v does not contain %q", recovered, fragment)
		}
	}()
	call()
}

type tkCounterInput struct {
	To int `json:"to"`
}

type tkCounterState struct {
	Phase string `json:"phase"`
	N     int    `json:"n"`
}

func tkNoopAbort[I, S, R, H any](context.Context, durable.RunningTask[I, S, R], durable.TaskRuntime[I, S, R, H]) error {
	return nil
}

// Pi TaskRuntime.memo: packages/durable/src/types.ts:204.
// Pi TaskRuntime.outcomes: packages/durable/src/types.ts:212.
func TestTaskPhases(t *testing.T) {
	// harness-tasks.test.ts:86 it("continues one invocation through checkpoint progress and completes with a typed result")
	t.Run("continues one invocation through checkpoint progress and completes with a typed result", func(t *testing.T) {
		seen := &syncList[int]{}
		runtimes := &syncList[any]{}
		counter := durable.DefineTask(durable.TaskDefinition[tkCounterInput, tkCounterState, int, any]{
			Name:    "test.counter",
			Version: 1,
			Initial: func(tkCounterInput) tkCounterState { return tkCounterState{Phase: "count"} },
			Phases: map[string]durable.PhaseHandler[tkCounterInput, tkCounterState, int, any]{
				"count": func(ctx context.Context, task durable.RunningTask[tkCounterInput, tkCounterState, int], runtime durable.TaskRuntime[tkCounterInput, tkCounterState, int, any]) error {
					seen.add(task.State.Checkpoint.N)
					runtimes.add(tkRuntimeIdentity(runtime))
					return runtime.Commit(ctx, func(_ durable.Tx, current durable.RunningTask[tkCounterInput, tkCounterState, int]) (*durable.NextTaskState[tkCounterState, int], error) {
						n := current.State.Checkpoint.N + 1
						if n == task.Input.To {
							return completed[tkCounterState, int](n), nil
						}
						return runningState[tkCounterState, int](tkCounterState{Phase: "count", N: n}), nil
					})
				},
			},
			Abort: tkNoopAbort[tkCounterInput, tkCounterState, int, any],
		})
		opened := tkOpenRoot(t, []durable.AnyTask{counter.Erased()})
		id, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, counter, tkCounterInput{To: 3}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		opened.harness.Resume()
		receipt, err := opened.harness.WaitForTask(testContext, id)
		if err != nil {
			t.Fatal(err)
		}
		outcome := tkOutcome(t, receipt)
		if outcome.Status != durable.OutcomeCompleted || outcome.Result == nil || (*outcome.Result).(float64) != 3 {
			t.Fatalf("outcome %s, want completed 3", describeOutcome(outcome))
		}
		if !reflect.DeepEqual(seen.all(), []int{0, 1, 2}) {
			t.Fatalf("seen %v, want [0 1 2]", seen.all())
		}
		distinct := map[any]struct{}{}
		for _, runtime := range runtimes.all() {
			distinct[runtime] = struct{}{}
		}
		if len(distinct) != 1 {
			t.Fatalf("%d runtimes, want one per invocation", len(distinct))
		}
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:120 it("faults a phase without durable progress and a throwing phase")
	t.Run("faults a phase without durable progress and a throwing phase", func(t *testing.T) {
		idle := tkOneStep("test.idle", func(context.Context, stepRecord, stepRuntime) error { return nil })
		documentOnly := tkOneStep("test.document-only", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			// A commit that returns no state is not progress.
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return nil, nil
			})
		})
		throws := tkOneStep("test.throws", func(context.Context, stepRecord, stepRuntime) error { return errors.New("boom") })
		opened := tkOpenRoot(t, []durable.AnyTask{idle.Erased(), documentOnly.Erased(), throws.Erased()})
		ids := []durable.TaskId{tkStart(t, opened.root, idle.Erased()), tkStart(t, opened.root, documentOnly.Erased()), tkStart(t, opened.root, throws.Erased())}
		opened.harness.Resume()
		want := []durable.TaskOutcome[durable.JsonValue]{
			outcomeFaulted("Task test.idle phase run returned without durable progress"),
			outcomeFaulted("Task test.document-only phase run returned without durable progress"),
			outcomeFaulted("boom"),
		}
		for index, id := range ids {
			expectOutcome(t, tkWaitOutcome(t, opened.harness, id), want[index])
		}
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:144 it("keeps a committed terminal outcome when the handler throws afterwards")
	t.Run("keeps a committed terminal outcome when the handler throws afterwards", func(t *testing.T) {
		lateCommit := &syncValue[error]{}
		done := tkOneStep("test.done", func(ctx context.Context, _ stepRecordOf[string], runtime stepRuntimeOf[string]) error {
			if err := runtime.Commit(ctx, func(durable.Tx, stepRecordOf[string]) (*durable.NextTaskState[stepState, string], error) {
				return completed[stepState, string]("ok"), nil
			}); err != nil {
				return err
			}
			lateCommit.put(runtime.Commit(ctx, func(durable.Tx, stepRecordOf[string]) (*durable.NextTaskState[stepState, string], error) {
				return nil, nil
			}))
			return errors.New("after terminal")
		})
		opened := tkOpenRoot(t, []durable.AnyTask{done.Erased()})
		id := tkStart(t, opened.root, done.Erased())
		opened.harness.Resume()
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeCompleted("ok"))
		eventually(t, lateCommit.isSet)
		late, _ := lateCommit.get()
		tkContainsError(t, late, fmt.Sprintf("Task %d is terminal", id))
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:160 it("compares checkpoints by value, including arrays")
	t.Run("compares checkpoints by value, including arrays", func(t *testing.T) {
		type collectState struct {
			Phase string   `json:"phase"`
			Items []string `json:"items"`
		}
		collect := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, collectState, durable.JsonValue, any]{
			Name:    "test.collect",
			Version: 1,
			Initial: func(durable.JsonValue) collectState { return collectState{Phase: "collect", Items: []string{}} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, collectState, durable.JsonValue, any]{
				"collect": func(ctx context.Context, task durable.RunningTask[durable.JsonValue, collectState, durable.JsonValue], runtime durable.TaskRuntime[durable.JsonValue, collectState, durable.JsonValue, any]) error {
					items := task.State.Checkpoint.Items
					// Two rounds of progress, then an equal copy of the checkpoint, which is no progress.
					next := append([]string{}, items...)
					if len(items) < 2 {
						next = append(next, fmt.Sprintf("item%d", len(items)))
					}
					return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, collectState, durable.JsonValue]) (*durable.NextTaskState[collectState, durable.JsonValue], error) {
						return runningState[collectState, durable.JsonValue](collectState{Phase: "collect", Items: next}), nil
					})
				},
			},
			Abort: tkNoopAbort[durable.JsonValue, collectState, durable.JsonValue, any],
		})
		opened := tkOpenRoot(t, []durable.AnyTask{collect.Erased()})
		id := tkStart(t, opened.root, collect.Erased())
		opened.harness.Resume()
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeFaulted("Task test.collect phase collect returned without durable progress"))
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:188 it("commits results with entries atomically, keeps memos until terminal, and retires task documents")
	t.Run("commits results with entries atomically, keeps memos until terminal, and retires task documents", func(t *testing.T) {
		type progressState struct {
			Lines []string `json:"lines"`
		}
		progress := durable.DefineDoc(durable.DocDefinition[progressState]{
			CommonDocDefinition: durable.CommonDocDefinition[progressState]{Kind: "test.task-progress", Version: 1, Initial: func() progressState { return progressState{Lines: []string{}} }, CheckpointWhen: func(progressState, []durable.Op, durable.CheckpointInfo) bool { return false }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeTask},
		})
		answer := durable.DefineEntry[durable.Never]("answer")
		child := tkOneStep("test.child", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) })
		var childId atomic.Int64
		progressSeen := &syncValue[[]any]{}
		winner := &syncValue[any]{}
		type writerState struct {
			Phase string `json:"phase"`
		}
		type writerResult struct {
			EntryId durable.EntryId `json:"entryId"`
		}
		type writerRuntime = durable.TaskRuntime[durable.JsonValue, writerState, writerResult, any]
		type writerRecord = durable.RunningTask[durable.JsonValue, writerState, writerResult]
		writer := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, writerState, writerResult, any]{
			Name:    "test.writer",
			Version: 1,
			Initial: func(durable.JsonValue) writerState { return writerState{Phase: "write"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, writerState, writerResult, any]{
				"write": func(ctx context.Context, task writerRecord, runtime writerRuntime) error {
					if _, ok, err := runtime.Memo(ctx, "choice"); err != nil || ok {
						return fmt.Errorf("memo before write: %v: %w", ok, err)
					}
					// Upstream races two candidates through Promise.all, where the first call queues first. Goroutines
					// have no such order, so either candidate may win; both callers must see the one durable winner.
					results := make([]durable.JsonValue, 2)
					errs := make([]error, 2)
					var group sync.WaitGroup
					for index, candidate := range []string{"a", "b"} {
						group.Go(func() { results[index], errs[index] = runtime.MemoCandidate(ctx, "choice", candidate) })
					}
					group.Wait()
					if errs[0] != nil || errs[1] != nil || results[0] != results[1] || (results[0] != "a" && results[0] != "b") {
						return fmt.Errorf("memo winners %v %v: %w", results[0], results[1], errors.Join(errs...))
					}
					winner.put(results[0])
					if value, _, _ := runtime.Memo(ctx, "choice"); value != results[0] {
						return fmt.Errorf("memo read %v, want %v", value, results[0])
					}
					// Memo names never resolve to inherited object properties.
					if _, ok, _ := runtime.Memo(ctx, "toString"); ok {
						return errors.New("memo toString is set")
					}
					if value, err := runtime.MemoCandidate(ctx, "toString", "own"); err != nil || value != "own" {
						return fmt.Errorf("memo toString %v: %w", value, err)
					}
					return runtime.Commit(ctx, func(tx durable.Tx, _ writerRecord) (*durable.NextTaskState[writerState, writerResult], error) {
						lines, err := durable.TxDoc[progressState](tx, progress, task.Id)
						if err != nil {
							return nil, err
						}
						if err := pushJSON(lines.Array("lines"), "wrote"); err != nil {
							return nil, err
						}
						// Task creation defaults to the task's own conversation.
						created, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: conversationOwned})
						if err != nil {
							return nil, err
						}
						childId.Store(int64(created))
						return runningState[writerState, writerResult](writerState{Phase: "answer"}), nil
					})
				},
				"answer": func(ctx context.Context, task writerRecord, runtime writerRuntime) error {
					want, _ := winner.get()
					if !reflect.DeepEqual(task.Memos, map[string]durable.JsonValue{"choice": want, "toString": "own"}) {
						return fmt.Errorf("memos %v", task.Memos)
					}
					if err := runtime.Commit(ctx, func(tx durable.Tx, _ writerRecord) (*durable.NextTaskState[writerState, writerResult], error) {
						lines, err := durable.TxDoc[progressState](tx, progress, task.Id)
						if err != nil {
							return nil, err
						}
						progressSeen.put(lines.Array("lines").Snapshot())
						return nil, nil
					}); err != nil {
						return err
					}
					return runtime.Commit(ctx, func(tx durable.Tx, current writerRecord) (*durable.NextTaskState[writerState, writerResult], error) {
						entry, err := tx.AppendEntry(current.ConversationId, durable.EntryDraft{Kind: "answer", Model: []ai.Message{user("done")}})
						if err != nil {
							return nil, err
						}
						return completed[writerState, writerResult](writerResult{EntryId: entry.Id}), nil
					})
				},
			},
			Abort: tkNoopAbort[durable.JsonValue, writerState, writerResult, any],
		})
		opened := tkOpenRoot(t, []durable.AnyTask{writer.Erased(), child.Erased()})
		conversation, err := opened.harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
		if err != nil {
			t.Fatal(err)
		}
		id := tkStart(t, conversation, writer.Erased())
		opened.harness.Resume()
		receipt, err := opened.harness.WaitForTask(testContext, id)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.State.Checkpoint != nil || receipt.Memos != nil {
			t.Fatalf("receipt keeps checkpoint %v or memos %v", receipt.State.Checkpoint, receipt.Memos)
		}
		outcome := tkOutcome(t, receipt)
		if outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("expected completion, got %s", describeOutcome(outcome))
		}
		result, err := durable.FromJsonValue[writerResult](*outcome.Result)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := durable.Commit(testContext, opened.harness, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(result.EntryId) })
		if err != nil {
			t.Fatal(err)
		}
		if !answer.Is(entry) || entry.ConversationId != conversation.Id() {
			t.Fatalf("entry %+v is not an answer of conversation %d", entry, conversation.Id())
		}
		if seen, _ := progressSeen.get(); !reflect.DeepEqual(seen, []any{"wrote"}) {
			t.Fatalf("progress seen %v", seen)
		}
		if retired, err := opened.harness.SnapshotErased(testContext, progress, id); err != nil || retired != nil {
			t.Fatalf("task document %v %v, want retired", retired, err)
		}
		if stored, err := opened.harness.GetTask(testContext, id); err != nil || !reflect.DeepEqual(*stored, receipt) {
			t.Fatalf("stored task %+v %v differs from receipt %+v", stored, err, receipt)
		}
		childReceipt, err := opened.harness.WaitForTask(testContext, durable.TaskId(childId.Load()))
		if err != nil || childReceipt.ConversationId != conversation.Id() {
			t.Fatalf("child receipt %+v %v, want conversation %d", childReceipt, err, conversation.Id())
		}
		if opened.root.Id() == conversation.Id() {
			t.Fatal("root and created conversation share an ID")
		}
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:257 it("resumes a waiting task once every task in `on` is terminal, whatever the outcome")
	t.Run("resumes a waiting task once every task in `on` is terminal, whatever the outcome", func(t *testing.T) {
		gate := deferred()
		order := &syncList[string]{}
		var on atomic.Pointer[[]durable.TaskId]
		outcomes := &syncValue[[]string]{}
		type waiterState struct {
			Phase string `json:"phase"`
		}
		waiter := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, waiterState, durable.JsonValue, any]{
			Name:    "test.waiter",
			Version: 1,
			Initial: func(durable.JsonValue) waiterState { return waiterState{Phase: "wait"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, waiterState, durable.JsonValue, any]{
				"wait": func(ctx context.Context, _ durable.RunningTask[durable.JsonValue, waiterState, durable.JsonValue], runtime durable.TaskRuntime[durable.JsonValue, waiterState, durable.JsonValue, any]) error {
					order.add("wait")
					return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, waiterState, durable.JsonValue]) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
						return waitingState[waiterState, durable.JsonValue](waiterState{Phase: "resume"}, *on.Load(), durable.JoinAllSettled), nil
					})
				},
				"resume": func(ctx context.Context, _ durable.RunningTask[durable.JsonValue, waiterState, durable.JsonValue], runtime durable.TaskRuntime[durable.JsonValue, waiterState, durable.JsonValue, any]) error {
					order.add("resume")
					results, err := runtime.Outcomes(ctx, *on.Load())
					if err != nil {
						return err
					}
					statuses := []string{}
					for _, outcome := range results {
						statuses = append(statuses, string(outcome.Status))
					}
					outcomes.put(statuses)
					return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, waiterState, durable.JsonValue]) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
						return completed[waiterState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ durable.RunningTask[durable.JsonValue, waiterState, durable.JsonValue], runtime durable.TaskRuntime[durable.JsonValue, waiterState, durable.JsonValue, any]) error {
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, waiterState, durable.JsonValue]) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
					return abortedWith[waiterState, durable.JsonValue]("test"), nil
				})
			},
		})
		first := tkOneStep("test.first", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			order.add("first")
			if err := gate.wait(ctx); err != nil {
				return err
			}
			return tkComplete(ctx, runtime)
		})
		faulting := tkOneStep("test.faulting", func(context.Context, stepRecord, stepRuntime) error {
			order.add("faulting")
			return errors.New("fails")
		})
		opened := tkOpenRoot(t, []durable.AnyTask{first.Erased(), faulting.Erased(), waiter.Erased()})
		firstId := tkStart(t, opened.root, first.Erased())
		faultingId := tkStart(t, opened.root, faulting.Erased())
		waits := []durable.TaskId{firstId, faultingId}
		on.Store(&waits)
		waiterId := tkStart(t, opened.root, waiter.Erased())
		opened.harness.Resume()
		eventually(t, func() bool { return order.len() == 3 })
		flush()
		sorted := order.all()
		if !reflect.DeepEqual(sortedStrings(sorted), []string{"faulting", "first", "wait"}) {
			t.Fatalf("order %v", sorted)
		}
		record, err := opened.harness.GetTask(testContext, waiterId)
		if err != nil || record.State.Status != durable.TaskWaiting || !reflect.DeepEqual(record.State.On, waits) {
			t.Fatalf("waiter %+v %v, want waiting on %v", record, err, waits)
		}
		gate.resolve()
		tkWaitOutcome(t, opened.harness, waiterId)
		if all := order.all(); all[len(all)-1] != "resume" {
			t.Fatalf("order %v does not end with resume", all)
		}
		if got, _ := outcomes.get(); !reflect.DeepEqual(got, []string{"completed", "faulted"}) {
			t.Fatalf("outcomes %v", got)
		}
		mustClose(t, opened.harness)
	})

	// #10549
	// harness-tasks.test.ts:310 it("stamps startedAt at the first run and endedAt when terminal, keeping startedAt through a wait")
	t.Run("stamps startedAt at the first run and endedAt when terminal, keeping startedAt through a wait", func(t *testing.T) {
		var clock atomic.Int64
		clock.Store(1_000)
		gate := deferred()
		var on atomic.Pointer[[]durable.TaskId]
		type waiterState struct {
			Phase string `json:"phase"`
		}
		type waiterDefinition = durable.TaskDefinition[durable.JsonValue, waiterState, durable.JsonValue, any]
		type waiterRunning = durable.RunningTask[durable.JsonValue, waiterState, durable.JsonValue]
		type waiterRuntime = durable.TaskRuntime[durable.JsonValue, waiterState, durable.JsonValue, any]
		waiter := durable.DefineTask(waiterDefinition{
			Name:    "test.timed-waiter",
			Version: 1,
			Initial: func(durable.JsonValue) waiterState { return waiterState{Phase: "wait"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, waiterState, durable.JsonValue, any]{
				"wait": func(ctx context.Context, _ waiterRunning, runtime waiterRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, waiterRunning) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
						return waitingState[waiterState, durable.JsonValue](waiterState{Phase: "resume"}, *on.Load(), durable.JoinAllSettled), nil
					})
				},
				"resume": func(ctx context.Context, _ waiterRunning, runtime waiterRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, waiterRunning) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
						return completed[waiterState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ waiterRunning, runtime waiterRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, waiterRunning) (*durable.NextTaskState[waiterState, durable.JsonValue], error) {
					return abortedWith[waiterState, durable.JsonValue]("test"), nil
				})
			},
		})
		held := tkOneStep("test.timed-held", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			if err := gate.wait(ctx); err != nil {
				return err
			}
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{waiter.Erased(), held.Erased()}, tkOptions{now: func() float64 { return float64(clock.Load()) }})
		heldId := tkStart(t, opened.root, held.Erased())
		waits := []durable.TaskId{heldId}
		on.Store(&waits)
		waiterId := tkStart(t, opened.root, waiter.Erased())
		type taskTimes struct {
			id      durable.TaskId
			started *float64
			ended   *float64
		}
		at := func(value float64) *float64 { return &value }
		times := func() []taskTimes {
			inspection, err := opened.harness.Inspect(testContext)
			if err != nil {
				t.Fatal(err)
			}
			var out []taskTimes
			for _, task := range inspection.Tasks {
				out = append(out, taskTimes{task.Record.Id, task.Record.StartedAt, task.Record.EndedAt})
			}
			return out
		}
		if got := times(); !reflect.DeepEqual(got, []taskTimes{{heldId, nil, nil}, {waiterId, nil, nil}}) {
			t.Fatalf("before the first run: %+v", got)
		}

		clock.Store(2_000)
		opened.harness.Resume()
		eventually(t, func() bool {
			record, err := opened.harness.GetTask(testContext, waiterId)
			return err == nil && record != nil && record.State.Status == durable.TaskWaiting
		})
		if got := times(); !reflect.DeepEqual(got, []taskTimes{{heldId, at(2_000), nil}, {waiterId, at(2_000), nil}}) {
			t.Fatalf("while waiting: %+v", got)
		}

		// The waiter runs again at 5_000; its start stays the first run.
		clock.Store(5_000)
		gate.resolve()
		settledWaiter, err := opened.harness.WaitForTask(testContext, waiterId)
		if err != nil {
			t.Fatalf("waiter: %v", err)
		}
		settledHeld, err := opened.harness.GetTask(testContext, heldId)
		if err != nil || settledHeld == nil {
			t.Fatalf("held task: %v %v", settledHeld, err)
		}
		if !reflect.DeepEqual([]*float64{settledHeld.StartedAt, settledHeld.EndedAt}, []*float64{at(2_000), at(5_000)}) {
			t.Fatalf("held times %v %v", settledHeld.StartedAt, settledHeld.EndedAt)
		}
		if !reflect.DeepEqual([]*float64{settledWaiter.StartedAt, settledWaiter.EndedAt}, []*float64{at(2_000), at(5_000)}) {
			t.Fatalf("waiter times %v %v", settledWaiter.StartedAt, settledWaiter.EndedAt)
		}
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:360 it("keeps one registry snapshot per phase and refreshes it at the phase boundary")
	t.Run("keeps one registry snapshot per phase and refreshes it at the phase boundary", func(t *testing.T) {
		type seenPhase struct {
			phase string
			tools []string
			same  bool
		}
		seen := &syncList[seenPhase]{}
		phaseGate := deferred()
		entered := deferred()
		toolNames := func(snapshot durable.RegistrySnapshot) []string {
			names := []string{}
			for _, entry := range snapshot.Tools() {
				names = append(names, entry.Tool.Name)
			}
			return names
		}
		type snapshotState struct {
			Phase string `json:"phase"`
		}
		type snapshotRuntime = durable.TaskRuntime[durable.JsonValue, snapshotState, durable.JsonValue, any]
		type snapshotRecord = durable.RunningTask[durable.JsonValue, snapshotState, durable.JsonValue]
		snapshots := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, snapshotState, durable.JsonValue, any]{
			Name:    "test.snapshots",
			Version: 1,
			Initial: func(durable.JsonValue) snapshotState { return snapshotState{Phase: "a"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, snapshotState, durable.JsonValue, any]{
				"a": func(ctx context.Context, _ snapshotRecord, runtime snapshotRuntime) error {
					before := runtime.Registry()
					entered.resolve()
					if err := phaseGate.wait(ctx); err != nil {
						return err
					}
					seen.add(seenPhase{"a", toolNames(runtime.Registry()), runtime.Registry() == before})
					return runtime.Commit(ctx, func(durable.Tx, snapshotRecord) (*durable.NextTaskState[snapshotState, durable.JsonValue], error) {
						return runningState[snapshotState, durable.JsonValue](snapshotState{Phase: "b"}), nil
					})
				},
				"b": func(ctx context.Context, _ snapshotRecord, runtime snapshotRuntime) error {
					seen.add(seenPhase{"b", toolNames(runtime.Registry()), true})
					return runtime.Commit(ctx, func(durable.Tx, snapshotRecord) (*durable.NextTaskState[snapshotState, durable.JsonValue], error) {
						return completed[snapshotState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: tkNoopAbort[durable.JsonValue, snapshotState, durable.JsonValue, any],
		})
		opened := tkOpenRoot(t, []durable.AnyTask{snapshots.Erased()})
		id := tkStart(t, opened.root, snapshots.Erased())
		opened.harness.Resume()
		if err := entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		addTool(t, opened.registry, supportTool("late"))
		phaseGate.resolve()
		tkWaitOutcome(t, opened.harness, id)
		want := []seenPhase{{"a", []string{}, true}, {"b", []string{"late"}, true}}
		if !reflect.DeepEqual(seen.all(), want) {
			t.Fatalf("seen %+v, want %+v", seen.all(), want)
		}
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:409 it("resolves the agent at first use in a phase from its snapshot, keeps it for the phase, and anew at the next")
	t.Run("resolves the agent at first use in a phase from its snapshot, keeps it for the phase, and anew at the next", func(t *testing.T) {
		type pingHooks struct{ Ping func() }
		pings := &syncList[string]{}
		type seenAgent struct {
			phase    string
			pings    []string
			thinking ai.ModelThinkingLevel
			same     bool
		}
		seen := &syncList[seenAgent]{}
		beforeUse, afterUse := deferred(), deferred()
		waitingBeforeUse, waitingAfterUse := deferred(), deferred()
		type agentState struct {
			Phase string `json:"phase"`
		}
		type agentRuntime = durable.TaskRuntime[durable.JsonValue, agentState, durable.JsonValue, *pingHooks]
		type agentRecord = durable.RunningTask[durable.JsonValue, agentState, durable.JsonValue]
		ping := func(runtime agentRuntime) ([]string, error) {
			pings.mu.Lock()
			pings.items = nil
			pings.mu.Unlock()
			err := runtime.Hooks().Each("ping", func(hooks *pingHooks) error {
				if hooks != nil && hooks.Ping != nil {
					hooks.Ping()
				}
				return nil
			})
			return pings.all(), err
		}
		phases := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, agentState, durable.JsonValue, *pingHooks]{
			Name:    "test.agent-phases",
			Version: 1,
			Initial: func(durable.JsonValue) agentState { return agentState{Phase: "a"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, agentState, durable.JsonValue, *pingHooks]{
				"a": func(ctx context.Context, _ agentRecord, runtime agentRuntime) error {
					waitingBeforeUse.resolve()
					if err := beforeUse.wait(ctx); err != nil {
						return err
					}
					first, err := runtime.Agent(ctx)
					if err != nil {
						return err
					}
					fired, err := ping(runtime)
					if err != nil {
						return err
					}
					seen.add(seenAgent{phase: "a", pings: fired, thinking: first.ThinkingLevel})
					waitingAfterUse.resolve()
					if err := afterUse.wait(ctx); err != nil {
						return err
					}
					second, err := runtime.Agent(ctx)
					if err != nil {
						return err
					}
					fired, err = ping(runtime)
					if err != nil {
						return err
					}
					seen.add(seenAgent{phase: "a", pings: fired, thinking: second.ThinkingLevel, same: reflect.DeepEqual(second, first)})
					return runtime.Commit(ctx, func(durable.Tx, agentRecord) (*durable.NextTaskState[agentState, durable.JsonValue], error) {
						return runningState[agentState, durable.JsonValue](agentState{Phase: "b"}), nil
					})
				},
				"b": func(ctx context.Context, _ agentRecord, runtime agentRuntime) error {
					agent, err := runtime.Agent(ctx)
					if err != nil {
						return err
					}
					fired, err := ping(runtime)
					if err != nil {
						return err
					}
					seen.add(seenAgent{phase: "b", pings: fired, thinking: agent.ThinkingLevel})
					return runtime.Commit(ctx, func(durable.Tx, agentRecord) (*durable.NextTaskState[agentState, durable.JsonValue], error) {
						return completed[agentState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: tkNoopAbort[durable.JsonValue, agentState, durable.JsonValue, *pingHooks],
		})
		opened := tkOpenRoot(t, []durable.AnyTask{phases.Erased()})
		addHooks(t, opened.registry, phases.Erased(), &pingHooks{Ping: func() { pings.add("before") }})
		id := tkStart(t, opened.root, phases.Erased())
		opened.harness.Resume()

		// Configured during the phase but before its first use: the lazy resolution reads it.
		if err := waitingBeforeUse.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustConfigure(t, opened.root, AgentChange{ThinkingLevel: SetTo(ai.ModelThinkingLevel(ai.ThinkingLow))})
		beforeUse.resolve()
		// Configured and installed after the first use: not seen until the next phase.
		if err := waitingAfterUse.wait(testContext); err != nil {
			t.Fatal(err)
		}
		addHooks(t, opened.registry, phases.Erased(), &pingHooks{Ping: func() { pings.add("late") }})
		mustConfigure(t, opened.root, AgentChange{ThinkingLevel: SetTo(ai.ModelThinkingLevel(ai.ThinkingHigh))})
		afterUse.resolve()

		tkWaitOutcome(t, opened.harness, id)
		want := []seenAgent{
			{phase: "a", pings: []string{"before"}, thinking: ai.ThinkingLow},
			{phase: "a", pings: []string{"before"}, thinking: ai.ThinkingLow, same: true},
			{phase: "b", pings: []string{"before", "late"}, thinking: ai.ThinkingHigh},
		}
		if !reflect.DeepEqual(seen.all(), want) {
			t.Fatalf("seen %+v, want %+v", seen.all(), want)
		}
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:478 it("rejects an agent() wait whose caller context is cancelled, without affecting the phase's resolution")
	t.Run("rejects an agent() wait whose caller context is cancelled, without affecting the phase's resolution", func(t *testing.T) {
		cancelled := &syncValue[error]{}
		resolved := &syncValue[ai.ModelThinkingLevel]{}
		agent := tkOneStep("test.agent-cancel", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			caller, cancel := context.WithCancelCause(ctx)
			cancel(errors.New("caller gone"))
			_, err := runtime.Agent(caller)
			cancelled.put(err)
			resolvedAgent, err := runtime.Agent(ctx)
			if err != nil {
				return err
			}
			resolved.put(resolvedAgent.ThinkingLevel)
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{agent.Erased()})
		tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, agent.Erased()))
		if err, _ := cancelled.get(); err == nil || err.Error() != "caller gone" {
			t.Fatalf("agent wait %v, want caller gone", err)
		}
		if level, _ := resolved.get(); level != ai.ThinkingOff {
			t.Fatalf("thinking level %q, want off", level)
		}
		mustClose(t, opened.harness)
	})

	// harness-tasks.test.ts:495 it("observes a failed agent resolution whose only caller stopped waiting")
	t.Run("observes a failed agent resolution whose only caller stopped waiting", func(t *testing.T) {
		var fail atomic.Bool
		settings := func() *HarnessSettings {
			if fail.Load() {
				panic(errors.New("settings broke"))
			}
			return nil
		}
		resolved := &syncValue[error]{}
		agent := tkOneStep("test.agent-failure", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			caller, cancel := context.WithCancelCause(ctx)
			cancel(errors.New("caller gone"))
			fail.Store(true)
			_, _ = runtime.Agent(caller)
			_, err := runtime.Agent(ctx)
			resolved.put(err)
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{agent.Erased()}, tkOptions{settings: settings})
		tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, agent.Erased()))
		if err, _ := resolved.get(); err == nil || err.Error() != "settings broke" {
			t.Fatalf("agent resolution %v, want settings broke", err)
		}
		mustClose(t, opened.harness)
	})
}

func sortedStrings(values []string) []string {
	sorted := append([]string(nil), values...)
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

// atomicCounter counts events from handlers on several goroutines.
type atomicCounter struct{ value atomic.Int64 }

// add increments the counter and returns the new count.
func (counter *atomicCounter) add() int64 { return counter.value.Add(1) }

func (counter *atomicCounter) get() int64 { return counter.value.Load() }

// publicationTerminal reports whether publication makes task id terminal.
func publicationTerminal(publication durable.CommitPublication, id durable.TaskId) bool {
	for _, change := range publication.Changes {
		if write, ok := change.(durable.TaskWrite); ok && write.Value.Id == id && write.Value.State.Status == durable.TaskTerminal {
			return true
		}
	}
	return false
}

// writesEndTask reports whether writes make a task terminal.
func writesEndTask(writes []durable.StorageWrite) bool {
	for _, write := range writes {
		if task, ok := write.(durable.TaskWrite); ok && task.Value.State.Status == durable.TaskTerminal {
			return true
		}
	}
	return false
}

// activeCounts are the values recorded for pi.durable.tasks.active, in order.
func activeCounts(recorder *telemetry.InMemoryTelemetryContext) []float64 {
	var values []float64
	for _, measurement := range measurements(recorder, "pi.durable.tasks.active") {
		values = append(values, measurement.Value)
	}
	return values
}

// harness-tasks.test.ts "publication at adoption".
func TestPublicationAtAdoption(t *testing.T) {
	// Both storages: one that resolves right after adopting, one that still works after it (JSONL's sidecar reclaim).
	type storageCase struct {
		name      string
		open      func() durable.Storage
		adopting  func(durable.Storage) bool
		committed func(durable.Storage) bool
	}
	cases := []storageCase{
		{"memory", func() durable.Storage { return newControlledStorage() }, func(s durable.Storage) bool { return s.(*controlledStorage).adopting.Load() }, nil},
		{"work after adopt", func() durable.Storage { return newLateResolvingStorage() }, func(s durable.Storage) bool { return s.(*lateResolvingStorage).adopting.Load() }, nil},
	}

	t.Run("resumes task and idle waits only once subscribeCommits() listeners saw the terminal commit", func(t *testing.T) {
		gate := deferred()
		gated := tkGated("test.wake-gated", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()}, tkOptions{storage: newLateResolvingStorage()})
		var seen syncList[durable.TaskId]
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range publication.Changes {
				if write, ok := change.(durable.TaskWrite); ok && write.Value.State.Status == durable.TaskTerminal {
					seen.add(write.Value.Id)
				}
			}
		})
		id := tkStart(t, opened.root, gated.Erased())
		opened.harness.Resume()
		sawIt := func() bool { return slices.Contains(seen.all(), id) }
		type resumed struct {
			wait string
			saw  bool
		}
		results := make(chan resumed, 3)
		// Pi calls the three waits, each of which registers in its synchronous part, before it resolves the gate; here each
		// goroutine has entered before the gate resolves, so the terminal commit cannot overtake a wait.
		callAsync(func() { _, _ = opened.harness.WaitForTask(testContext, id); results <- resumed{"WaitForTask", sawIt()} })
		callAsync(func() {
			_ = opened.harness.WaitForIdle(testContext)
			results <- resumed{"Harness.WaitForIdle", sawIt()}
		})
		callAsync(func() {
			_ = opened.root.WaitForIdle(testContext)
			results <- resumed{"Conversation.WaitForIdle", sawIt()}
		})
		gate.resolve()
		for range 3 {
			if got := <-results; !got.saw {
				t.Fatalf("%s resumed before the subscribeCommits() listeners saw the terminal commit", got.wait)
			}
		}
		mustClose(t, opened.harness)
	})

	for _, c := range cases {
		t.Run("runs host listeners before the waits a terminal commit resolves ("+c.name+")", func(t *testing.T) {
			store := c.open()
			gate := deferred()
			gated := tkGated("test.order-gated", gate)
			opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()}, tkOptions{storage: store})
			var order syncList[string]
			id := tkStart(t, opened.root, gated.Erased())
			opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
				if publicationTerminal(publication, id) {
					order.add(fmt.Sprintf("host adopting=%v", c.adopting(store)))
				}
			})
			opened.harness.Resume()
			waited := make(chan struct{})
			callAsync(func() {
				_, _ = opened.harness.WaitForTask(testContext, id)
				order.add("wait")
				close(waited)
			})
			gate.resolve()
			<-waited
			if got := order.all(); !reflect.DeepEqual(got, []string{"host adopting=false", "wait"}) {
				t.Fatalf("order %v", got)
			}
			mustClose(t, opened.harness)
		})

		// Upstream checks the order in the microtask after `await abortTask()`, when only the mark has been published, and its
		// abort listener runs inside controller.abort(). Here context.AfterFunc runs the observer on a goroutine of its own, so
		// it can follow AbortTask's return, and by then the aborted task's terminal commit, which carries the same mark, can be
		// published too. The host listener therefore counts only the commit that marks the task, and the test waits for the
		// observer, not for a count of entries.
		t.Run("signals a newly marked run after host listeners saw the mark, outside the storage's adopt ("+c.name+")", func(t *testing.T) {
			store := c.open()
			reached := deferred()
			signalled := deferred()
			var order syncList[string]
			task := tkOneStep("test.order-signalled", func(ctx context.Context, _ stepRecord, _ stepRuntime) error {
				context.AfterFunc(ctx, func() {
					order.add(fmt.Sprintf("abort adopting=%v", c.adopting(store)))
					signalled.resolve()
				})
				reached.resolve()
				return abortedBy(ctx)
			})
			opened := tkOpenRoot(t, []durable.AnyTask{task.Erased()}, tkOptions{storage: store})
			id := tkStart(t, opened.root, task.Erased())
			opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
				for _, change := range publication.Changes {
					if write, ok := change.(durable.TaskWrite); ok && write.Value.Id == id && write.Value.AbortRequested && write.Value.State.Status != durable.TaskTerminal {
						order.add("host")
					}
				}
			})
			opened.harness.Resume()
			_ = reached.wait(testContext)
			if result := must(opened.harness.AbortTask(testContext, id)); result != "marked" {
				t.Fatalf("abort result %q", result)
			}
			if err := signalled.wait(testContext); err != nil {
				t.Fatal(err)
			}
			if got := order.all(); !reflect.DeepEqual(got, []string{"host", "abort adopting=false"}) {
				t.Fatalf("order %v", got)
			}
			mustClose(t, opened.harness)
		})
	}

	t.Run("resolves a waitForIdle() called after adoption, before the storage resolved, after host listeners", func(t *testing.T) {
		store := newLateResolvingStorage()
		gate := deferred()
		gated := tkGated("test.gap-idle", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()}, tkOptions{storage: store})
		var order syncList[string]
		id := tkStart(t, opened.root, gated.Erased())
		hosted := deferred()
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			if publicationTerminal(publication, id) {
				order.add("host")
				hosted.resolve()
			}
		})
		idle := make(chan struct{})
		store.setGap(func(writes []durable.StorageWrite) bool {
			if !writesEndTask(writes) {
				return false
			}
			// Upstream runs the gap a macrotask after adoption, when the microtask that dispatched the host listeners has run,
			// and what the gap started runs before the commit resolves. Here the Session publishes once the storage's goroutine
			// is parked in a wait, which under load can take longer than the storage's own work, so the gap waits for the
			// listeners' effect and for the continuation it started.
			_ = hosted.wait(testContext)
			go func() {
				_ = opened.harness.WaitForIdle(testContext)
				order.add(fmt.Sprintf("idle committing=%v", store.committing.Load()))
				close(idle)
			}()
			<-idle
			return true
		})
		opened.harness.Resume()
		gate.resolve()
		<-idle
		if got := order.all(); !reflect.DeepEqual(got, []string{"host", "idle committing=true"}) {
			t.Fatalf("order %v", got)
		}
		mustClose(t, opened.harness)
	})

	t.Run("publishes a commit the storage rejects after adopting it, resolves its waits, then fails the Session", func(t *testing.T) {
		store := newLateResolvingStorage()
		recorder := telemetry.NewInMemoryTelemetryContext()
		gate := deferred()
		gated := tkGated("test.reject-after-adopt", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()}, tkOptions{storage: store, telemetry: recorder})
		id := tkStart(t, opened.root, gated.Erased())
		var seen syncList[string]
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			if publicationTerminal(publication, id) {
				seen.add("terminal")
			}
		})
		store.setRejectAfterAdopt(writesEndTask)
		opened.harness.Resume()
		waited := make(chan durable.SettledTask[durable.JsonValue], 1)
		callAsync(func() {
			receipt, _ := opened.harness.WaitForTask(testContext, id)
			waited <- receipt
		})
		gate.resolve()
		receipt := <-waited
		if receipt.State.Outcome == nil || receipt.State.Outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("receipt %+v", receipt.State)
		}
		if got := seen.all(); !reflect.DeepEqual(got, []string{"terminal"}) {
			t.Fatalf("seen %v", got)
		}
		<-opened.harness.Closed()
		if end := opened.harness.End(); end.Reason != durable.SessionEndFailed || !errors.Is(end.Error, errReclaimFailed) {
			t.Fatalf("end %+v", end)
		}
		if !slices.Contains(opened.reports.allInfos(), durable.ReportInfo{Type: durable.ReportSessionFailed}) {
			t.Fatalf("infos %v", opened.reports.allInfos())
		}
		if got := activeCounts(recorder); !reflect.DeepEqual(got, []float64{1, -1}) {
			t.Fatalf("active %v", got)
		}
	})

	t.Run("counts a task created by a commit the storage rejects after adopting it, and its close balances it", func(t *testing.T) {
		store := newLateResolvingStorage()
		recorder := telemetry.NewInMemoryTelemetryContext()
		idle := tkOneStep("test.reject-created", func(context.Context, stepRecord, stepRuntime) error { return nil })
		opened := tkOpenRoot(t, []durable.AnyTask{idle.Erased()}, tkOptions{storage: store, telemetry: recorder})
		store.setRejectAfterAdopt(func(writes []durable.StorageWrite) bool {
			for _, write := range writes {
				if _, ok := write.(durable.TaskWrite); ok {
					return true
				}
			}
			return false
		})
		_, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
			return tx.CreateTaskErased(idle.Erased(), nil, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err == nil || !strings.Contains(err.Error(), "reclaim failed") {
			t.Fatalf("error %v", err)
		}
		<-opened.harness.Closed()
		if end := opened.harness.End(); end.Reason != durable.SessionEndFailed {
			t.Fatalf("end %+v", end)
		}
		if got := activeCounts(recorder); !reflect.DeepEqual(got, []float64{1, -1}) {
			t.Fatalf("active %v", got)
		}
	})

	t.Run("keeps a task wait resolved when a host listener closes the Harness on the terminal commit", func(t *testing.T) {
		gate := deferred()
		gated := tkGated("test.close-in-listener", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()})
		id := tkStart(t, opened.root, gated.Erased())
		closing := make(chan struct{})
		var once sync.Once
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			if publicationTerminal(publication, id) {
				once.Do(func() { go func() { _ = opened.harness.Close(testContext); close(closing) }() })
			}
		})
		opened.harness.Resume()
		waited := make(chan durable.SettledTask[durable.JsonValue], 1)
		callAsync(func() {
			receipt, err := opened.harness.WaitForTask(testContext, id)
			if err != nil {
				t.Error(err)
			}
			waited <- receipt
		})
		gate.resolve()
		receipt := <-waited
		if receipt.State.Outcome == nil || receipt.State.Outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("receipt %+v", receipt.State)
		}
		<-closing
	})

	// Upstream's listener calls close(), whose seal runs inside the listener, before the publication wakes the waits: the wait
	// the terminal commit settled keeps its receipt. A Go listener closes on a goroutine of its own, so this one waits until the
	// seal has rejected what it rejects, which it learns from a Harness wait under a key no settle resolves (the seal rejects
	// the Harness waits last), and only then returns for the publication to wake the waits.
	t.Run("keeps a task wait resolved when a host listener's close has sealed the Harness before the publication wakes the waits", func(t *testing.T) {
		gate := deferred()
		gated := tkGated("test.seal-in-listener", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()})
		id := tkStart(t, opened.root, gated.Erased())
		sealed := opened.harness.(*harnessImpl).tasks.idleHarness.Add(testContext, false)
		closing := make(chan struct{})
		var once sync.Once
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			if publicationTerminal(publication, id) {
				once.Do(func() {
					go func() { _ = opened.harness.Close(testContext); close(closing) }()
					<-sealed.Done()
				})
			}
		})
		opened.harness.Resume()
		waited := make(chan durable.SettledTask[durable.JsonValue], 1)
		callAsync(func() {
			receipt, err := opened.harness.WaitForTask(testContext, id)
			if err != nil {
				t.Error(err)
			}
			waited <- receipt
		})
		gate.resolve()
		receipt := <-waited
		if receipt.State.Outcome == nil || receipt.State.Outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("receipt %+v", receipt.State)
		}
		<-closing
	})

	// A reconcile pass ends with an idle settle, on its own goroutine, after it left the Session line. Upstream's continuation
	// there runs before the next job on the line, so it never sees a commit adopted and not yet published to the host
	// listeners; this goroutine can run while such a commit is published. The listener plays that settle: it runs one on the
	// state its commit changed, and the idle waits registered before the commit must not be awake until the listener returned.
	t.Run("keeps an idle wait asleep when an idle settle outside the publication runs on a commit before its host listeners returned", func(t *testing.T) {
		gate := deferred()
		gated := tkGated("test.settle-in-listener", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()})
		id := tkStart(t, opened.root, gated.Erased())
		tasks := opened.harness.(*harnessImpl).tasks
		conversationWait := tasks.idleWaiters.Add(testContext, opened.root.Id())
		harnessWait := tasks.idleHarness.Add(testContext, true)
		awake := make(chan int, 1)
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			if publicationTerminal(publication, id) {
				tasks.resolveIdleWaiters()
				count := 0
				for _, wait := range []*Waiter[struct{}]{conversationWait, harnessWait} {
					select {
					case <-wait.Done():
						count++
					default:
					}
				}
				awake <- count
			}
		})
		opened.harness.Resume()
		gate.resolve()
		if count := <-awake; count != 0 {
			t.Fatalf("%d idle waits woke before the host listeners returned", count)
		}
		for _, wait := range []*Waiter[struct{}]{conversationWait, harnessWait} {
			if _, err := wait.Wait(); err != nil {
				t.Fatal(err)
			}
		}
		mustClose(t, opened.harness)
	})

	// Upstream settles an idle wait inside the observer, at the commit that made its scope idle, so a close whose seal runs
	// before the code awaiting it resumes cannot reject it. A Go listener closes on a goroutine of its own, so this one
	// waits until the seal has rejected what it rejects, which it learns from a Harness wait under a key no settle resolves
	// (the seal rejects the Harness waits last), and only then returns for the publication to wake the waits.
	t.Run("keeps an idle wait resolved when a host listener's close has sealed the Harness before the publication wakes the waits", func(t *testing.T) {
		gate := deferred()
		gated := tkGated("test.seal-idle-in-listener", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()})
		id := tkStart(t, opened.root, gated.Erased())
		sealed := opened.harness.(*harnessImpl).tasks.idleHarness.Add(testContext, false)
		closing := make(chan struct{})
		var once sync.Once
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			if publicationTerminal(publication, id) {
				once.Do(func() {
					go func() { _ = opened.harness.Close(testContext); close(closing) }()
					<-sealed.Done()
				})
			}
		})
		opened.harness.Resume()
		idled := make(chan error, 2)
		callAsync(func() { idled <- opened.harness.WaitForIdle(testContext) })
		callAsync(func() { idled <- opened.root.WaitForIdle(testContext) })
		gate.resolve()
		for range 2 {
			if err := <-idled; err != nil {
				t.Error(err)
			}
		}
		<-closing
	})

	t.Run("keeps a task wait resolved when the Harness closes after adoption, before the storage resolved", func(t *testing.T) {
		store := newLateResolvingStorage()
		gate := deferred()
		gated := tkGated("test.close-in-gap", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()}, tkOptions{storage: store})
		id := tkStart(t, opened.root, gated.Erased())
		gapped := deferred()
		closing := make(chan struct{})
		store.setGap(func(writes []durable.StorageWrite) bool {
			if !writesEndTask(writes) {
				return false
			}
			callAsync(func() { _ = opened.harness.Close(testContext); close(closing) })
			gapped.resolve()
			return true
		})
		opened.harness.Resume()
		type waitResult struct {
			receipt durable.SettledTask[durable.JsonValue]
			err     error
		}
		waited := make(chan waitResult, 1)
		callAsync(func() {
			receipt, err := opened.harness.WaitForTask(testContext, id)
			waited <- waitResult{receipt, err}
		})
		gate.resolve()
		_ = gapped.wait(testContext)
		<-closing
		result := <-waited
		if result.err != nil || result.receipt.State.Outcome == nil || result.receipt.State.Outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("wait %+v %v", result.receipt.State, result.err)
		}
		<-opened.harness.Closed()
		if end := opened.harness.End(); end.Reason != durable.SessionEndClosed {
			t.Fatalf("end %+v", end)
		}
	})

	t.Run("fails the Session with the storage error when it rejects after adopting, and reports an observer's throw", func(t *testing.T) {
		store := newLateResolvingStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		bug := errors.New("observer bug")
		observeCommits(opened.harness, func(_ context.Context, publication session.ObservedCommit) {
			if publication.Memory {
				return
			}
			for _, change := range publication.Changes {
				if _, ok := change.(durable.EntryWrite); ok {
					panic(bug)
				}
			}
		})
		store.setRejectAfterAdopt(func(writes []durable.StorageWrite) bool {
			for _, write := range writes {
				if _, ok := write.(durable.EntryWrite); ok {
					return true
				}
			}
			return false
		})
		_, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "note", Data: map[string]any{}})
		})
		if err == nil || !strings.Contains(err.Error(), "reclaim failed") {
			t.Fatalf("error %v", err)
		}
		<-opened.harness.Closed()
		if end := opened.harness.End(); end.Reason != durable.SessionEndFailed || !errors.Is(end.Error, errReclaimFailed) {
			t.Fatalf("end %+v", end)
		}
		reports, infos := opened.reports.all(), opened.reports.allInfos()
		at := slices.IndexFunc(reports, func(err error) bool { return errors.Is(err, bug) })
		if at < 0 || infos[at].Type != durable.ReportListenerError {
			t.Fatalf("reports %v infos %v", reports, infos)
		}
		if !slices.Contains(infos, durable.ReportInfo{Type: durable.ReportSessionFailed}) {
			t.Fatalf("infos %v", infos)
		}
	})

	t.Run("stores a host listener's own commit before the reconcile commit that the same publication asks for", func(t *testing.T) {
		store := newControlledStorage()
		gate := deferred()
		child := tkGated("test.order-child", gate)
		parent := tkOneStep("test.order-parent", func(ctx context.Context, task stepRecord, runtime stepRuntime) error {
			if err := runtime.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				_, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: task.Id}})
				return nil, err
			}); err != nil {
				return err
			}
			// The child is still live, so the outcome is held as completing until a reconcile pass finalizes it.
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{parent.Erased(), child.Erased()}, tkOptions{storage: store})
		parentId := tkStart(t, opened.root, parent.Erased())
		opened.harness.Resume()
		eventually(t, func() bool {
			record, _ := opened.harness.GetTask(testContext, parentId)
			return record != nil && record.State.Status == durable.TaskCompleting
		})
		opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range publication.Changes {
				if write, ok := change.(durable.TaskWrite); ok && write.Value.Id != parentId && write.Value.State.Status == durable.TaskTerminal {
					// Upstream's void commit takes its line position in this turn; goroutines have no such order, so the
					// position is taken here and the commit made at it.
					impl := opened.harness.(*harnessImpl)
					ticket := impl.TakeLineTicket()
					go func() {
						_, _ = impl.CommitWithTicket(ticket, testContext, func(tx *session.Transaction) (any, error) {
							return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "note", Data: map[string]any{}})
						}, session.TransactionScope{})
					}()
					return
				}
			}
		})
		before := store.commitCount()
		gate.resolve()
		_, _ = opened.harness.WaitForTask(testContext, parentId)
		eventually(t, func() bool {
			for index := before; index < store.commitCount(); index++ {
				if writesHaveEntry(store.commitAt(index)) {
					return true
				}
			}
			return false
		})
		var kinds []string
		for index := before; index < store.commitCount(); index++ {
			writes := store.commitAt(index)
			if writesHaveEntry(writes) {
				kinds = append(kinds, "host entry")
				continue
			}
			for _, write := range writes {
				task, ok := write.(durable.TaskWrite)
				if !ok || task.Value.State.Status != durable.TaskTerminal {
					continue
				}
				if task.Value.Id == parentId {
					kinds = append(kinds, "parent terminal")
				} else {
					kinds = append(kinds, "child terminal")
				}
				break
			}
		}
		if !reflect.DeepEqual(kinds, []string{"child terminal", "host entry", "parent terminal"}) {
			t.Fatalf("commits %v", kinds)
		}
		mustClose(t, opened.harness)
	})

	t.Run("calls a host listener added on a Harness between adopt and the dispatch only when one was registered at adopt", func(t *testing.T) {
		store := newControlledStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		note := func() {
			_, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
				return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "note", Data: map[string]any{}})
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		var late syncList[durable.Seq]
		stopLate := func() {}
		// No host listener at adopt, although the Harness's own observers run: no dispatch, so the late one misses it.
		store.setAfterAdopt(func() {
			stopLate = opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) { late.add(publication.Seq) })
		})
		note()
		flush()
		if late.len() != 0 {
			t.Fatalf("late listener got %v", late.all())
		}
		note()
		if late.len() != 1 {
			t.Fatalf("late listener got %v", late.all())
		}
		stopLate()
		// One registered at adopt: the dispatch reads the listeners when it runs, the late one included.
		opened.harness.SubscribeCommits(func(context.Context, durable.CommitPublication) {})
		var second syncList[durable.Seq]
		store.setAfterAdopt(func() {
			opened.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) { second.add(publication.Seq) })
		})
		note()
		if second.len() != 1 {
			t.Fatalf("second late listener got %v", second.all())
		}
		mustClose(t, opened.harness)
	})
}

func writesHaveEntry(writes []durable.StorageWrite) bool {
	for _, write := range writes {
		if _, ok := write.(durable.EntryWrite); ok {
			return true
		}
	}
	return false
}
