// Ports packages/durable/test/harness-tasks.test.ts: task phases, runtime, scheduling, abort, and close.
//
// Go mappings forced by language mechanics. A TypeScript test that runs two calls "at once" with Promise.all or
// void-calls relies on the event loop's FIFO queueing; Go goroutines have no such order, so the tests either wait for
// the first call to reach storage before starting the second, or assert only what the order cannot change. Each
// such site says so. Abort reasons are context causes: the scheduler's signal is context.Canceled where upstream's
// is an AbortError.

package harness

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
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
	harness, registry, reports := openTasks(t, store, tasks, openTasksOptions{now: option.now, settings: option.settings})
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
		opened := tkOpenRoot(t, []durable.AnyTask{counter})
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

	t.Run("faults a phase without durable progress and a throwing phase", func(t *testing.T) {
		idle := tkOneStep("test.idle", func(context.Context, stepRecord, stepRuntime) error { return nil })
		documentOnly := tkOneStep("test.document-only", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			// A commit that returns no state is not progress.
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return nil, nil
			})
		})
		throws := tkOneStep("test.throws", func(context.Context, stepRecord, stepRuntime) error { return errors.New("boom") })
		opened := tkOpenRoot(t, []durable.AnyTask{idle, documentOnly, throws})
		ids := []durable.TaskId{tkStart(t, opened.root, idle), tkStart(t, opened.root, documentOnly), tkStart(t, opened.root, throws)}
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
		opened := tkOpenRoot(t, []durable.AnyTask{done})
		id := tkStart(t, opened.root, done)
		opened.harness.Resume()
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeCompleted("ok"))
		eventually(t, lateCommit.isSet)
		late, _ := lateCommit.get()
		tkContainsError(t, late, fmt.Sprintf("Task %d is terminal", id))
		mustClose(t, opened.harness)
	})

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
		opened := tkOpenRoot(t, []durable.AnyTask{collect})
		id := tkStart(t, opened.root, collect)
		opened.harness.Resume()
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeFaulted("Task test.collect phase collect returned without durable progress"))
		mustClose(t, opened.harness)
	})

	t.Run("commits results with entries atomically, keeps memos until terminal, and retires task documents", func(t *testing.T) {
		type progressState struct {
			Lines []string `json:"lines"`
		}
		progress := durable.DefineDoc(durable.DocDefinition[progressState]{
			CommonDocDefinition: durable.CommonDocDefinition[progressState]{Kind: "test.task-progress", Version: 1, Initial: func() progressState { return progressState{Lines: []string{}} }},
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
						created, err := tx.CreateTaskErased(child, nil, durable.TaskOptions{Ownership: conversationOwned})
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
		opened := tkOpenRoot(t, []durable.AnyTask{writer, child})
		conversation, err := opened.harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
		if err != nil {
			t.Fatal(err)
		}
		id := tkStart(t, conversation, writer)
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
		opened := tkOpenRoot(t, []durable.AnyTask{first, faulting, waiter})
		firstId := tkStart(t, opened.root, first)
		faultingId := tkStart(t, opened.root, faulting)
		waits := []durable.TaskId{firstId, faultingId}
		on.Store(&waits)
		waiterId := tkStart(t, opened.root, waiter)
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
		opened := tkOpenRoot(t, []durable.AnyTask{snapshots})
		id := tkStart(t, opened.root, snapshots)
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
		opened := tkOpenRoot(t, []durable.AnyTask{phases})
		addHooks(t, opened.registry, phases, &pingHooks{Ping: func() { pings.add("before") }})
		id := tkStart(t, opened.root, phases)
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
		addHooks(t, opened.registry, phases, &pingHooks{Ping: func() { pings.add("late") }})
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
		opened := tkOpenRoot(t, []durable.AnyTask{agent})
		tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, agent))
		if err, _ := cancelled.get(); err == nil || err.Error() != "caller gone" {
			t.Fatalf("agent wait %v, want caller gone", err)
		}
		if level, _ := resolved.get(); level != ai.ThinkingOff {
			t.Fatalf("thinking level %q, want off", level)
		}
		mustClose(t, opened.harness)
	})

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
		opened := tkOpenRoot(t, []durable.AnyTask{agent}, tkOptions{settings: settings})
		tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, agent))
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
