// Ports packages/durable/test/harness-tasks.test.ts: "task runtime", "task scheduling", "task abort", and "task close".
// See harness_tasks_test.go for the Go mappings that apply to every case.

package harness

// pi: packages/durable/src/harness/scheduler.ts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

type tkNotes struct {
	Text string `json:"text"`
}

func tkSessionNotes(kind string) durable.DocToken[tkNotes] {
	return durable.DefineDoc(durable.DocDefinition[tkNotes]{
		CommonDocDefinition: durable.CommonDocDefinition[tkNotes]{Kind: kind, Version: 1, Initial: func() tkNotes { return tkNotes{} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
	})
}

func tkSetNotes(t *testing.T, harness Harness, token durable.DocToken[tkNotes], text string) {
	t.Helper()
	_, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
		notes, err := durable.TxDoc[tkNotes](tx, token)
		if err != nil {
			return nil, err
		}
		return nil, notes.Set("text", text)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// tkAppendBlocker commits a blocker entry to the conversation without waiting for it; the result arrives on the channel.
func tkAppendBlocker(harness Harness, conversationId durable.ConversationId) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
			_, appendErr := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "blocker"})
			return nil, appendErr
		})
		done <- err
	}()
	return done
}

// tkCloseSealed starts Close and returns once its close listener has sealed the scheduler. Upstream's close() seals
// synchronously, so a test's next statement (a held commit's release, a handler's proceed) runs after the seal; the Go
// Close runs on its own goroutine, and a fixed flush does not order it under load.
func tkCloseSealed(t *testing.T, harness Harness) <-chan error {
	t.Helper()
	closing := make(chan error, 1)
	go func() { closing <- harness.Close(testContext) }()
	select {
	case <-harness.(*harnessImpl).tasks.sealed:
	case <-testContext.Done():
		t.Fatal(context.Cause(testContext))
	}
	return closing
}

func tkWaitErr(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a call to return")
		return nil
	}
}

func tkTaskWrites(storage *controlledStorage, id durable.TaskId) []durable.TaskStatus {
	statuses := []durable.TaskStatus{}
	for index := range storage.commitCount() {
		for _, write := range storage.commitAt(index) {
			if task, ok := write.(durable.TaskWrite); ok && task.Value.Id == id {
				statuses = append(statuses, task.Value.State.Status)
			}
		}
	}
	return statuses
}

func tkLastTaskWrites(storage *controlledStorage) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	var records []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	for _, write := range storage.commitAt(storage.commitCount() - 1) {
		if task, ok := write.(durable.TaskWrite); ok {
			records = append(records, task.Value)
		}
	}
	return records
}

// Pi TaskRuntime.agent: packages/durable/src/types.ts:181.
// Pi TaskRuntime.sleep: packages/durable/src/types.ts:229.
func TestTaskRuntime(t *testing.T) {
	t.Run("rejects runtime operations after the invocation ends and stops its watches", func(t *testing.T) {
		notes := tkSessionNotes("test.task-notes")
		absentNotes := tkSessionNotes("test.task-absent")
		captured := &syncValue[stepRuntime]{}
		handlerContext := &syncValue[context.Context]{}
		watchHandle := &syncValue[durable.WatchHandle[durable.JsonObject]]{}
		delivered := &syncList[string]{}
		absent := &syncValue[durable.WatchHandle[durable.JsonObject]]{}
		watcher := tkOneStep("test.watcher", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			captured.put(runtime)
			handlerContext.put(ctx)
			missing, err := runtime.WatchDocErased(ctx, absentNotes)
			if err != nil {
				return err
			}
			absent.put(missing)
			watch, err := runtime.WatchDocErased(ctx, notes)
			if err != nil {
				return err
			}
			watch.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
				if value == nil {
					delivered.add("retired")
				} else {
					delivered.add(value.Value("text").(string))
				}
				return nil
			})
			watchHandle.put(watch)
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{watcher})
		tkSetNotes(t, opened.harness, notes, "hello")
		id := tkStart(t, opened.root, watcher)
		opened.harness.Resume()
		tkWaitOutcome(t, opened.harness, id)
		if missing, _ := absent.get(); missing != nil {
			t.Fatalf("watch of an absent document is %v, want nil", missing)
		}
		// The watch stops when the step after the phase ends the invocation; later commits deliver nothing.
		watch, _ := watchHandle.get()
		<-watch.Closed()
		if end := watch.End(); end.Reason != durable.WatchStopped {
			t.Fatalf("watch ended %+v, want stopped", end)
		}
		tkSetNotes(t, opened.harness, notes, "after")
		flush()
		if delivered.len() != 0 {
			t.Fatalf("delivered %v after the invocation ended", delivered.all())
		}
		runtime, _ := captured.get()
		noChange := func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
			return nil, nil
		}
		tkContainsError(t, runtime.Commit(testContext, noChange), "invocation has ended")
		_, _, memoErr := runtime.Memo(testContext, "x")
		tkContainsError(t, memoErr, "invocation has ended")
		_, candidateErr := runtime.MemoCandidate(testContext, "x", float64(1))
		tkContainsError(t, candidateErr, "invocation has ended")
		tkContainsError(t, runtime.Sleep(testContext, 0), "invocation has ended")
		_, watchErr := runtime.WatchDocErased(testContext, notes)
		tkContainsError(t, watchErr, "invocation has ended")
		// The handler's own context is cancelled by now; the ended invocation still wins.
		ended, _ := handlerContext.get()
		_, agentErr := runtime.Agent(ended)
		tkContainsError(t, agentErr, "invocation has ended")
		tkExpectPanic(t, "invocation has ended", func() { runtime.Now() })
		tkExpectPanic(t, "invocation has ended", func() { runtime.Report(errors.New("late")) })
		mustClose(t, opened.harness)
	})

	t.Run("reads committed documents and context through the runtime, and forwards the clock and reports", func(t *testing.T) {
		notes := durable.DefineDoc(durable.DocDefinition[tkNotes]{
			CommonDocDefinition: durable.CommonDocDefinition[tkNotes]{Kind: "test.runtime-notes", Version: 1, Initial: func() tkNotes { return tkNotes{} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
		})
		captured := &syncValue[stepRuntime]{}
		seen := &syncList[any]{}
		reader := tkOneStep("test.reader", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			captured.put(runtime)
			view, err := runtime.Context(ctx, runtime.ConversationId(), nil)
			if err != nil {
				return err
			}
			first, second := view.Entries[0].Id, view.Entries[1].Id
			current, err := durable.Snapshot[tkNotes](ctx, runtime, notes, runtime.ConversationId())
			if err != nil {
				return err
			}
			seen.add(current.Text)
			asOf, err := durable.SnapshotAsOf[tkNotes](ctx, runtime, notes, first, runtime.ConversationId())
			if err != nil {
				return err
			}
			seen.add(asOf.Text)
			atFirst, err := runtime.Context(ctx, runtime.ConversationId(), &durable.ContextOptions{At: &first})
			if err != nil {
				return err
			}
			seen.add(len(atFirst.Entries))
			atSecond, err := runtime.Context(ctx, runtime.ConversationId(), &durable.ContextOptions{At: &second})
			if err != nil {
				return err
			}
			seen.add(len(atSecond.Messages))
			seen.add(runtime.Now())
			runtime.Report(errors.New("reported"))
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{reader}, tkOptions{now: func() float64 { return 1234 }})
		for _, text := range []string{"one", "two"} {
			_, err := opened.root.Commit(testContext, func(tx durable.Tx) (any, error) {
				draft, err := durable.TxDoc[tkNotes](tx, notes, opened.root.Id())
				if err != nil {
					return nil, err
				}
				if err := draft.Set("text", text); err != nil {
					return nil, err
				}
				_, err = tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user(text)}})
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		id := tkStart(t, opened.root, reader)
		opened.harness.Resume()
		tkWaitOutcome(t, opened.harness, id)
		if want := []any{"two", "one", 1, 2, float64(1234)}; !reflect.DeepEqual(seen.all(), want) {
			t.Fatalf("seen %v, want %v", seen.all(), want)
		}
		reports := opened.reports.all()
		if len(reports) != 1 || reports[0].Error() != "reported" {
			t.Fatalf("reports %v, want [reported]", reports)
		}
		// The step after the phase ends the invocation.
		flush()
		runtime, _ := captured.get()
		_, snapshotErr := runtime.SnapshotErased(testContext, notes, opened.root.Id())
		tkContainsError(t, snapshotErr, "invocation has ended")
		_, contextErr := runtime.Context(testContext, opened.root.Id(), nil)
		tkContainsError(t, contextErr, "invocation has ended")
		mustClose(t, opened.harness)
	})

	t.Run("orders runtime commits against the step: one queued before it lands, one after it rejects", func(t *testing.T) {
		store := newControlledStorage()
		before := make(chan error, 1)
		after := make(chan error, 1)
		var afterQueued atomic.Bool
		heldGate := &syncValue[gate]{}
		var harnessRef atomic.Pointer[Harness]
		detached := tkOneStep("test.detached", func(_ context.Context, _ stepRecordOf[string], runtime stepRuntimeOf[string]) error {
			// Hold the line, queue a commit without awaiting it, and return; the step queues behind that commit.
			held := store.holdCommits()
			heldGate.put(held)
			tkAppendBlocker(*harnessRef.Load(), runtime.ConversationId())
			// Goroutines queue in no fixed order: wait until a commit is in storage, then let the other one queue.
			if err := held.entered.wait(testContext); err != nil {
				return err
			}
			go func() {
				before <- runtime.Commit(testContext, func(durable.Tx, stepRecordOf[string]) (*durable.NextTaskState[stepState, string], error) {
					return completed[stepState, string]("before"), nil
				})
			}()
			flush()
			// Queued after the handler returned, so after the step.
			time.AfterFunc(20*time.Millisecond, func() {
				afterQueued.Store(true)
				after <- runtime.Commit(testContext, func(durable.Tx, stepRecordOf[string]) (*durable.NextTaskState[stepState, string], error) {
					return completed[stepState, string]("after"), nil
				})
			})
			return nil
		})
		opened := tkOpenRoot(t, []durable.AnyTask{detached}, tkOptions{storage: store})
		harnessRef.Store(&opened.harness)
		id := tkStart(t, opened.root, detached)
		opened.harness.Resume()
		eventually(t, func() bool { return heldGate.isSet() })
		eventually(t, afterQueued.Load)
		flush()
		held, _ := heldGate.get()
		held.release()
		if err := tkWaitErr(t, before); err != nil {
			t.Fatal(err)
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeCompleted("before"))
		err := tkWaitErr(t, after)
		if err == nil || (!containsAny(err.Error(), "invocation has ended", "is terminal")) {
			t.Fatalf("late commit error %v, want ended or terminal", err)
		}
		mustClose(t, opened.harness)
	})

	t.Run("stops a watch whose acquisition finishes after the invocation ended", func(t *testing.T) {
		notes := tkSessionNotes("test.late-watch")
		store := newControlledStorage()
		watching := make(chan error, 1)
		var watchQueued atomic.Bool
		heldGate := &syncValue[gate]{}
		var harnessRef atomic.Pointer[Harness]
		late := tkOneStep("test.late-watch", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			held := store.holdCommits()
			heldGate.put(held)
			tkAppendBlocker(*harnessRef.Load(), runtime.ConversationId())
			// Starts after the handler returned, so its line job queues behind the step that ends the invocation.
			time.AfterFunc(20*time.Millisecond, func() {
				watchQueued.Store(true)
				_, err := runtime.WatchDocErased(testContext, notes)
				watching <- err
			})
			return nil
		})
		opened := tkOpenRoot(t, []durable.AnyTask{late}, tkOptions{storage: store})
		harnessRef.Store(&opened.harness)
		tkSetNotes(t, opened.harness, notes, "x")
		id := tkStart(t, opened.root, late)
		opened.harness.Resume()
		eventually(t, heldGate.isSet)
		held, _ := heldGate.get()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		eventually(t, watchQueued.Load)
		flush()
		held.release()
		tkContainsError(t, tkWaitErr(t, watching), "invocation has ended")
		if outcome := tkWaitOutcome(t, opened.harness, id); outcome.Status != durable.OutcomeFaulted {
			t.Fatalf("outcome %s, want faulted", describeOutcome(outcome))
		}
		mustClose(t, opened.harness)
	})

	t.Run("sleeps until the Harness clock reaches the deadline, rechecking after each timer", func(t *testing.T) {
		var clock atomic.Int64
		clock.Store(1000)
		woke := deferred()
		sleeper := tkOneStep("test.sleeper", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			if err := runtime.Sleep(ctx, 900); err != nil {
				return err
			}
			if err := runtime.Sleep(ctx, 1005); err != nil {
				return err
			}
			woke.resolve()
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{sleeper}, tkOptions{now: func() float64 { return float64(clock.Load()) }})
		id := tkStart(t, opened.root, sleeper)
		opened.harness.Resume()
		// The clock stands still, so real timers keep firing without waking the task.
		time.Sleep(30 * time.Millisecond)
		if settled(woke.done) {
			t.Fatal("the task woke before the clock reached its deadline")
		}
		clock.Store(1005)
		tkWaitOutcome(t, opened.harness, id)
		mustClose(t, opened.harness)
	})

	t.Run("rejects a sleep when the invocation is signalled or the sleep's own context is cancelled", func(t *testing.T) {
		results := &syncList[string]{}
		sleeping := deferred()
		signalled := tkOneStep("test.sleep-signalled", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			sleeping.resolve()
			err := runtime.Sleep(ctx, float64(time.Now().UnixMilli()+60_000))
			// Upstream's signal reason is an AbortError; the scheduler's signal is context.Canceled.
			results.add(fmt.Sprintf("signalled:%v", errors.Is(err, context.Canceled)))
			return err
		})
		cancelled := tkOneStep("test.sleep-cancelled", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			own, cancel := context.WithCancelCause(ctx)
			result := make(chan error, 1)
			go func() { result <- runtime.Sleep(own, float64(time.Now().UnixMilli()+60_000)) }()
			flush()
			cancel(errors.New("stop sleeping"))
			results.add("cancelled:" + (<-result).Error())
			return tkComplete(ctx, runtime)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{signalled, cancelled})
		signalledId := tkStart(t, opened.root, signalled)
		cancelledId := tkStart(t, opened.root, cancelled)
		opened.harness.Resume()
		tkWaitOutcome(t, opened.harness, cancelledId)
		if err := sleeping.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if result, err := opened.harness.AbortTask(testContext, signalledId); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, signalledId), outcomeAborted("test"))
		if want := []string{"cancelled:stop sleeping", "signalled:true"}; !reflect.DeepEqual(results.all(), want) {
			t.Fatalf("results %v, want %v", results.all(), want)
		}
		mustClose(t, opened.harness)
	})
}

func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if len(fragment) > 0 && len(text) >= len(fragment) && indexOf(text, fragment) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(text, fragment string) int {
	for index := 0; index+len(fragment) <= len(text); index++ {
		if text[index:index+len(fragment)] == fragment {
			return index
		}
	}
	return -1
}

// Pi source: packages/durable/src/errors.ts
// mutation-checked: dropping the reads and writes of StorageRejected.Message fails it
// TestTaskScheduling Conversation.waitForIdle resolves when the ordinary ownership scope has no live non-background task (packages/durable/src/harness/types.ts:546-550).
// mutation-checked: Conversation.WaitForIdle returning without waiting fails it.
func TestTaskScheduling(t *testing.T) {
	t.Run("retries reservation on the next wakeup after a rejected reservation commit", func(t *testing.T) {
		var runs atomic.Int32
		once := tkOneStep("test.once", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			runs.Add(1)
			return tkComplete(ctx, runtime)
		})
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{once}, tkOptions{storage: store})
		id := tkStart(t, opened.root, once)
		store.failNextCommit(durable.NewStorageRejected("busy", nil))
		opened.harness.Resume()
		eventually(t, func() bool { return len(opened.reports.all()) == 1 })
		record, err := opened.harness.GetTask(testContext, id)
		if err != nil || record.State.Status != durable.TaskPending {
			t.Fatalf("task %+v %v, want pending", record, err)
		}
		// Any wakeup, here a registry change, reserves again.
		addTool(t, opened.registry, supportTool("wake"))
		tkWaitOutcome(t, opened.harness, id)
		if runs.Load() != 1 {
			t.Fatalf("%d runs, want 1", runs.Load())
		}
		mustClose(t, opened.harness)
	})

	t.Run("keeps a wakeup that arrives while a rejected reservation commit is in storage", func(t *testing.T) {
		first := tkOneStep("test.wake-first", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) })
		late := tkOneStep("test.wake-late", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) })
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{first}, tkOptions{storage: store})
		firstId := tkStart(t, opened.root, first)
		lateId := tkStart(t, opened.root, late)
		held := store.holdCommits()
		store.failNextCommit(durable.NewStorageRejected("busy", nil))
		opened.harness.Resume()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		// Registering the missing definition wakes the scheduler while the doomed reservation is in storage.
		addTask(t, opened.registry, late)
		held.release()
		tkWaitOutcome(t, opened.harness, firstId)
		tkWaitOutcome(t, opened.harness, lateId)
		mustClose(t, opened.harness)
	})

	t.Run("reruns a task whose fault write was rejected", func(t *testing.T) {
		var runs atomic.Int32
		store := newControlledStorage()
		throws := tkOneStep("test.rejected-fault", func(context.Context, stepRecord, stepRuntime) error {
			// The next commit is the step's fault write.
			if runs.Add(1) == 1 {
				store.failNextCommit(durable.NewStorageRejected("busy", nil))
			}
			return errors.New("boom")
		})
		opened := tkOpenRoot(t, []durable.AnyTask{throws}, tkOptions{storage: store})
		id := tkStart(t, opened.root, throws)
		opened.harness.Resume()
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeFaulted("boom"))
		if runs.Load() != 2 {
			t.Fatalf("%d runs, want 2", runs.Load())
		}
		reports := opened.reports.all()
		var rejected *durable.StorageRejected
		if len(reports) != 1 || !errors.As(reports[0], &rejected) || rejected.Message != "busy" {
			t.Fatalf("reports %v, want one StorageRejected: busy", reports)
		}
		mustClose(t, opened.harness)
	})

	t.Run("waits for Harness and conversation idleness, counting blocked work and ignoring background tasks", func(t *testing.T) {
		var gatesMu sync.Mutex
		gates := map[durable.TaskId]chan struct{}{}
		gated := tkOneStep("test.gated", func(ctx context.Context, task stepRecord, runtime stepRuntime) error {
			release := make(chan struct{})
			gatesMu.Lock()
			gates[task.Id] = release
			gatesMu.Unlock()
			select {
			case <-release:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			return tkComplete(ctx, runtime)
		})
		release := func(id durable.TaskId) {
			gatesMu.Lock()
			defer gatesMu.Unlock()
			close(gates[id])
		}
		opened := tkOpenRoot(t, []durable.AnyTask{gated})
		if err := opened.harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		other, err := opened.harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
		if err != nil {
			t.Fatal(err)
		}
		foreground := tkStart(t, opened.root, gated)
		background := tkStart(t, opened.root, gated, true)
		elsewhere := tkStart(t, other, gated)
		// Work that has not started yet is live; cancelling a wait only rejects that wait.
		stopWaiting, cancelWait := context.WithCancelCause(testContext)
		cancelledWait := make(chan error, 1)
		go func() { cancelledWait <- opened.harness.WaitForIdle(stopWaiting) }()
		flush()
		cancelWait(errors.New("stop waiting"))
		tkContainsError(t, tkWaitErr(t, cancelledWait), "stop waiting")
		already, cancelAlready := context.WithCancelCause(testContext)
		cancelAlready(errors.New("already cancelled"))
		tkContainsError(t, opened.root.WaitForIdle(already), "already cancelled")
		opened.harness.Resume()
		eventually(t, func() bool {
			gatesMu.Lock()
			defer gatesMu.Unlock()
			return len(gates) == 3
		})
		rootIdle := make(chan error, 1)
		go func() { rootIdle <- opened.root.WaitForIdle(testContext) }()
		harnessIdle := make(chan error, 1)
		go func() { harnessIdle <- opened.harness.WaitForIdle(testContext) }()
		release(foreground)
		if err := tkWaitErr(t, rootIdle); err != nil {
			t.Fatal(err)
		}
		flush()
		select {
		case <-harnessIdle:
			t.Fatal("Harness idle while another conversation has a live task")
		default:
		}
		release(elsewhere)
		if err := tkWaitErr(t, harnessIdle); err != nil {
			t.Fatal(err)
		}
		if record, err := opened.harness.GetTask(testContext, background); err != nil || record.State.Status != durable.TaskRunning {
			t.Fatalf("background task %+v %v, want running", record, err)
		}
		release(background)
		tkWaitOutcome(t, opened.harness, background)
		mustClose(t, opened.harness)
		tkContainsError(t, opened.harness.WaitForIdle(testContext), "closed")
		tkContainsError(t, opened.root.WaitForIdle(testContext), "closed")
	})

	t.Run("rejects unknown tasks and reports terminal tasks", func(t *testing.T) {
		done := tkOneStep("test.quick", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) })
		opened := tkOpenRoot(t, []durable.AnyTask{done})
		unknown := durable.TaskId(999_999)
		if record, err := opened.harness.GetTask(testContext, unknown); err != nil || record != nil {
			t.Fatalf("GetTask of an unknown task %+v %v", record, err)
		}
		_, waitErr := opened.harness.WaitForTask(testContext, unknown)
		tkContainsError(t, waitErr, "does not exist")
		_, abortErr := opened.harness.AbortTask(testContext, unknown)
		tkContainsError(t, abortErr, "does not exist")
		id := tkStart(t, opened.root, done)
		opened.harness.Resume()
		tkWaitOutcome(t, opened.harness, id)
		if receipt, err := opened.harness.WaitForTask(testContext, id); err != nil || receipt.State.Status != durable.TaskTerminal {
			t.Fatalf("WaitForTask of a terminal task %+v %v", receipt, err)
		}
		if result, err := opened.harness.AbortTask(testContext, id); err != nil || result != "terminal" {
			t.Fatalf("AbortTask of a terminal task %q %v", result, err)
		}
		mustClose(t, opened.harness)
		tkExpectPanic(t, "closed", opened.harness.Resume)
	})

	t.Run("rejects task waits cancelled or closed while queued on the line, and pending waits on close", func(t *testing.T) {
		gate := deferred()
		blocking := tkGated("test.wait-close", gate)
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{blocking}, tkOptions{storage: store})
		id := tkStart(t, opened.root, blocking)

		// Hold the line with a commit, then queue waits behind it.
		held := store.holdCommits()
		blocker := tkAppendBlocker(opened.harness, opened.root.Id())
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		cancelCtx, cancel := context.WithCancelCause(testContext)
		cancelledWhileQueued := make(chan error, 1)
		go func() {
			_, err := opened.harness.WaitForTask(cancelCtx, id)
			cancelledWhileQueued <- err
		}()
		flush()
		cancel(errors.New("wait cancelled"))
		held.release()
		if err := tkWaitErr(t, blocker); err != nil {
			t.Fatal(err)
		}
		tkContainsError(t, tkWaitErr(t, cancelledWhileQueued), "wait cancelled")

		pending := make(chan error, 1)
		go func() {
			_, err := opened.harness.WaitForTask(testContext, id)
			pending <- err
		}()
		idle := make(chan error, 1)
		go func() { idle <- opened.harness.WaitForIdle(testContext) }()
		conversationIdle := make(chan error, 1)
		go func() { conversationIdle <- opened.root.WaitForIdle(testContext) }()
		flush()
		heldAgain := store.holdCommits()
		blockerAgain := tkAppendBlocker(opened.harness, opened.root.Id())
		if err := heldAgain.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		closedWhileQueued := make(chan error, 1)
		go func() {
			_, err := opened.harness.WaitForTask(testContext, id)
			closedWhileQueued <- err
		}()
		closing := tkCloseSealed(t, opened.harness)
		heldAgain.release()
		if err := tkWaitErr(t, blockerAgain); err != nil {
			t.Fatal(err)
		}
		tkContainsError(t, tkWaitErr(t, closedWhileQueued), "closed")
		tkContainsError(t, tkWaitErr(t, pending), "closed")
		tkContainsError(t, tkWaitErr(t, idle), "closed")
		tkContainsError(t, tkWaitErr(t, conversationIdle), "closed")
		gate.resolve()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTaskAbort(t *testing.T) {
	t.Run("rejects the run's commits and memo writes after the mark, and settles through a fresh abort invocation", func(t *testing.T) {
		reached := deferred()
		errs := &syncList[string]{}
		memoRead := &syncValue[durable.JsonValue]{}
		abortRuntimes := &syncList[any]{}
		runRuntime := &syncValue[any]{}
		marked := tkOneStep("test.marked", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			runRuntime.put(tkRuntimeIdentity(runtime))
			if _, err := runtime.MemoCandidate(ctx, "kept", float64(1)); err != nil {
				return err
			}
			reached.resolve()
			// Keep working after the signal: every later write of this run must reject.
			_ = abortedBy(runtime.Signal())
			value, _, err := runtime.Memo(testContext, "kept")
			if err != nil {
				return err
			}
			memoRead.put(value)
			if _, err := runtime.MemoCandidate(testContext, "late", float64(2)); err != nil {
				errs.add(err.Error())
			}
			if err := runtime.Commit(testContext, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return completed[stepState, durable.JsonValue](nil), nil
			}); err != nil {
				errs.add(err.Error())
			}
			return nil
		}, func(ctx context.Context, runtime stepRuntime) error {
			abortRuntimes.add(tkRuntimeIdentity(runtime))
			return runtime.Commit(ctx, func(_ durable.Tx, current stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				if !current.AbortRequested {
					t.Error("abort handler sees no abort mark")
				}
				if !reflect.DeepEqual(current.Memos, map[string]durable.JsonValue{"kept": float64(1)}) {
					t.Errorf("abort handler memos %v, want kept: 1", current.Memos)
				}
				return abortedWith[stepState, durable.JsonValue]("mark"), nil
			})
		})
		opened := tkOpenRoot(t, []durable.AnyTask{marked})
		id := tkStart(t, opened.root, marked)
		opened.harness.Resume()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if result, err := opened.harness.AbortTask(testContext, id); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeAborted("mark"))
		if value, _ := memoRead.get(); value != float64(1) {
			t.Fatalf("memo read %v, want 1", value)
		}
		message := fmt.Sprintf("Task %d has a durable abort mark", id)
		if !reflect.DeepEqual(errs.all(), []string{message, message}) {
			t.Fatalf("errors %v, want the abort mark twice", errs.all())
		}
		run, _ := runRuntime.get()
		if aborts := abortRuntimes.all(); len(aborts) != 1 || aborts[0] == run {
			t.Fatalf("abort runtimes %v, want one distinct from the run's", aborts)
		}
		mustClose(t, opened.harness)
	})

	t.Run("starts no further phase after a mark that lands during a phase with progress", func(t *testing.T) {
		reached, proceed := deferred(), deferred()
		phases := &syncList[string]{}
		type twoState struct {
			Phase string `json:"phase"`
		}
		type twoRuntime = durable.TaskRuntime[durable.JsonValue, twoState, durable.JsonValue, any]
		type twoRecord = durable.RunningTask[durable.JsonValue, twoState, durable.JsonValue]
		two := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, twoState, durable.JsonValue, any]{
			Name:    "test.mark-boundary",
			Version: 1,
			Initial: func(durable.JsonValue) twoState { return twoState{Phase: "one"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, twoState, durable.JsonValue, any]{
				"one": func(ctx context.Context, _ twoRecord, runtime twoRuntime) error {
					phases.add("one")
					if err := runtime.Commit(ctx, func(durable.Tx, twoRecord) (*durable.NextTaskState[twoState, durable.JsonValue], error) {
						return runningState[twoState, durable.JsonValue](twoState{Phase: "two"}), nil
					}); err != nil {
						return err
					}
					reached.resolve()
					// Ignores the signal and returns normally after the mark.
					return proceed.wait(testContext)
				},
				"two": func(context.Context, twoRecord, twoRuntime) error {
					phases.add("two")
					return nil
				},
			},
			Abort: func(ctx context.Context, _ twoRecord, runtime twoRuntime) error {
				phases.add("abort")
				return runtime.Commit(ctx, func(durable.Tx, twoRecord) (*durable.NextTaskState[twoState, durable.JsonValue], error) {
					return abortedWith[twoState, durable.JsonValue]("boundary"), nil
				})
			},
		})
		opened := tkOpenRoot(t, []durable.AnyTask{two})
		id := tkStart(t, opened.root, two)
		opened.harness.Resume()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		aborting := tkMarkDurably(t, opened.harness, id)
		proceed.resolve()
		if result := <-aborting; result.err != nil || result.result != "marked" {
			t.Fatalf("AbortTask %+v", result)
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeAborted("boundary"))
		if !reflect.DeepEqual(phases.all(), []string{"one", "abort"}) {
			t.Fatalf("phases %v, want [one abort]", phases.all())
		}
		mustClose(t, opened.harness)
	})

	t.Run("signals and joins the run before returning, then runs the abort handler", func(t *testing.T) {
		reached := deferred()
		var runEnded atomic.Bool
		// Upstream's second abortTask call lands before the abort invocation finishes only by microtask timing;
		// the abort handler waits for the second call so the Go goroutines cannot reorder them.
		secondCalled := deferred()
		signalled := tkOneStep("test.signalled", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			reached.resolve()
			defer runEnded.Store(true)
			_ = abortedBy(runtime.Signal())
			return nil
		}, func(ctx context.Context, runtime stepRuntime) error {
			if err := secondCalled.wait(ctx); err != nil {
				return err
			}
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue]("test"), nil
			})
		})
		opened := tkOpenRoot(t, []durable.AnyTask{signalled})
		id := tkStart(t, opened.root, signalled)
		opened.harness.Resume()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if result, err := opened.harness.AbortTask(testContext, id); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		if !runEnded.Load() {
			t.Fatal("AbortTask returned before the run ended")
		}
		if result, err := opened.harness.AbortTask(testContext, id); err != nil || result != "marked" {
			t.Fatalf("second AbortTask %q %v", result, err)
		}
		secondCalled.resolve()
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeAborted("test"))
		mustClose(t, opened.harness)
	})

	t.Run("aborts waiting work before its wait ends and faults abort handlers that throw or settle nothing", func(t *testing.T) {
		gate := deferred()
		first := tkGated("test.dependency", gate)
		var firstId atomic.Int64
		wait := func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return waitingState[stepState, durable.JsonValue](stepState{Phase: "run"}, []durable.TaskId{durable.TaskId(firstId.Load())}, durable.JoinAllSettled), nil
			})
		}
		lazy := tkOneStep("test.lazy-abort", wait, func(context.Context, stepRuntime) error { return nil })
		throwing := tkOneStep("test.throwing-abort", wait, func(context.Context, stepRuntime) error { return errors.New("abort failed") })
		opened := tkOpenRoot(t, []durable.AnyTask{first, lazy, throwing})
		firstTask := tkStart(t, opened.root, first)
		firstId.Store(int64(firstTask))
		lazyId := tkStart(t, opened.root, lazy)
		throwingId := tkStart(t, opened.root, throwing)
		opened.harness.Resume()
		waiting := func(id durable.TaskId) bool {
			record, err := opened.harness.GetTask(testContext, id)
			return err == nil && record != nil && record.State.Status == durable.TaskWaiting
		}
		eventually(t, func() bool { return waiting(throwingId) })
		eventually(t, func() bool { return waiting(lazyId) })
		for _, id := range []durable.TaskId{lazyId, throwingId} {
			if _, err := opened.harness.AbortTask(testContext, id); err != nil {
				t.Fatal(err)
			}
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, lazyId), outcomeFaulted(fmt.Sprintf("Abort handler of task %d returned without a terminal outcome", lazyId)))
		expectOutcome(t, tkWaitOutcome(t, opened.harness, throwingId), outcomeFaulted("abort failed"))
		if record, err := opened.harness.GetTask(testContext, firstTask); err != nil || record.State.Status != durable.TaskRunning {
			t.Fatalf("dependency %+v %v, want running", record, err)
		}
		gate.resolve()
		tkWaitOutcome(t, opened.harness, firstTask)
		mustClose(t, opened.harness)
	})

	t.Run("does not signal a running abort handler when aborted again, and a cancelled caller leaves the mark durable", func(t *testing.T) {
		started, reached, proceed, runRelease := deferred(), deferred(), deferred(), deferred()
		abortSignalled := &syncValue[bool]{}
		var aborts atomic.Int32
		run := tkOneStep("test.abort-again", func(ctx context.Context, _ stepRecord, _ stepRuntime) error {
			started.resolve()
			// Ignores the signal, so the first caller is still joining when it gives up.
			return runRelease.wait(testContext)
		}, func(ctx context.Context, runtime stepRuntime) error {
			aborts.Add(1)
			reached.resolve()
			if err := proceed.wait(testContext); err != nil {
				return err
			}
			abortSignalled.put(runtime.Signal().Err() != nil)
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue]("once"), nil
			})
		})
		opened := tkOpenRoot(t, []durable.AnyTask{run})
		id := tkStart(t, opened.root, run)
		opened.harness.Resume()
		// AbortTask joins only a run invocation already on the line (scheduler.ts abort), so the caller can be seen
		// joining only once the run phase has started; a fixed flush does not guarantee it under load.
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		// This caller gives up while joining; the mark and the abort invocation are unaffected.
		caller, cancel := context.WithCancelCause(testContext)
		cancelled := make(chan error, 1)
		go func() {
			_, err := opened.harness.AbortTask(caller, id)
			cancelled <- err
		}()
		for {
			record, err := opened.harness.GetTask(testContext, id)
			if err != nil {
				t.Fatal(err)
			}
			if record != nil && record.AbortRequested {
				break
			}
			flush()
		}
		cancel(errors.New("caller gave up"))
		tkContainsError(t, tkWaitErr(t, cancelled), "caller gave up")
		runRelease.resolve()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if result, err := opened.harness.AbortTask(testContext, id); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		proceed.resolve()
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeAborted("once"))
		if signalled, _ := abortSignalled.get(); signalled {
			t.Fatal("the running abort handler was signalled")
		}
		if aborts.Load() != 1 {
			t.Fatalf("%d abort invocations, want 1", aborts.Load())
		}
		mustClose(t, opened.harness)
	})

	t.Run("keeps a terminal outcome committed by an abort handler that throws afterwards", func(t *testing.T) {
		run := tkOneStep("test.abort-then-throw", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			return abortedBy(runtime.Signal())
		}, func(ctx context.Context, runtime stepRuntime) error {
			if err := runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue]("done"), nil
			}); err != nil {
				return err
			}
			return errors.New("after terminal")
		})
		opened := tkOpenRoot(t, []durable.AnyTask{run})
		id := tkStart(t, opened.root, run)
		opened.harness.Resume()
		flush()
		if _, err := opened.harness.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeAborted("done"))
		mustClose(t, opened.harness)
	})

	t.Run("never starts phase one when the abort lands while the reservation settles", func(t *testing.T) {
		var ran atomic.Bool
		reserved := tkOneStep("test.reserved", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			ran.Store(true)
			return abortedBy(runtime.Signal())
		})
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{reserved}, tkOptions{storage: store})
		id := tkStart(t, opened.root, reserved)
		held := store.holdCommits()
		opened.harness.Resume()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		// The reservation commit is in storage; the abort mark commit queues behind it before the release, as upstream's
		// abortTask() enters the line synchronously.
		aborting := make(chan tkAbortResult, 1)
		queueOnLineIn(t, "TaskScheduler).Abort", func() <-chan struct{} {
			done := make(chan struct{})
			go func() {
				defer close(done)
				result, err := opened.harness.AbortTask(testContext, id)
				aborting <- tkAbortResult{result: result, err: err}
			}()
			return done
		})
		held.release()
		if result := <-aborting; result.err != nil || result.result != "marked" {
			t.Fatalf("AbortTask %+v", result)
		}
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeAborted("test"))
		if ran.Load() {
			t.Fatal("phase one ran")
		}
		mustClose(t, opened.harness)
	})

	t.Run("lets an abort mark win over a fault that races it", func(t *testing.T) {
		store := newControlledStorage()
		marking := make(chan tkAbortResult, 1)
		heldGate := &syncValue[gate]{}
		var harnessRef atomic.Pointer[Harness]
		racing := tkOneStep("test.racing-fault", func(_ context.Context, task stepRecord, _ stepRuntime) error {
			held := store.holdCommits()
			heldGate.put(held)
			go func() {
				result, err := (*harnessRef.Load()).AbortTask(testContext, task.Id)
				marking <- tkAbortResult{result: result, err: err}
			}()
			if err := held.entered.wait(testContext); err != nil {
				return err
			}
			// The mark is in storage but not committed when the handler throws, so the fault commit queues behind it.
			return errors.New("would fault")
		})
		opened := tkOpenRoot(t, []durable.AnyTask{racing}, tkOptions{storage: store})
		harnessRef.Store(&opened.harness)
		id := tkStart(t, opened.root, racing)
		opened.harness.Resume()
		eventually(t, heldGate.isSet)
		held, _ := heldGate.get()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		flush()
		held.release()
		expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeAborted("test"))
		if result := <-marking; result.err != nil || result.result != "marked" {
			t.Fatalf("AbortTask %+v", result)
		}
		mustClose(t, opened.harness)
	})
}

func TestTaskClose(t *testing.T) {
	t.Run("stops without outcomes and starts no fresh phase or abort invocation while closing", func(t *testing.T) {
		reached, proceed := deferred(), deferred()
		phases := &syncList[string]{}
		var abortRan atomic.Bool
		type twoState struct {
			Phase string `json:"phase"`
		}
		type twoRuntime = durable.TaskRuntime[durable.JsonValue, twoState, durable.JsonValue, any]
		type twoRecord = durable.RunningTask[durable.JsonValue, twoState, durable.JsonValue]
		two := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, twoState, durable.JsonValue, any]{
			Name:    "test.two",
			Version: 1,
			Initial: func(durable.JsonValue) twoState { return twoState{Phase: "one"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, twoState, durable.JsonValue, any]{
				"one": func(ctx context.Context, _ twoRecord, runtime twoRuntime) error {
					phases.add("one")
					if err := runtime.Commit(ctx, func(durable.Tx, twoRecord) (*durable.NextTaskState[twoState, durable.JsonValue], error) {
						return runningState[twoState, durable.JsonValue](twoState{Phase: "two"}), nil
					}); err != nil {
						return err
					}
					reached.resolve()
					// Ignores signals and returns normally once released; the closing rule wins over the next phase.
					return proceed.wait(testContext)
				},
				"two": func(context.Context, twoRecord, twoRuntime) error {
					phases.add("two")
					return nil
				},
			},
			Abort: func(context.Context, twoRecord, twoRuntime) error {
				abortRan.Store(true)
				return nil
			},
		})
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{two}, tkOptions{storage: store})
		id := tkStart(t, opened.root, two)
		opened.harness.Resume()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		aborting := tkMarkDurably(t, opened.harness, id)
		record, err := opened.harness.GetTask(testContext, id)
		if err != nil {
			t.Fatal(err)
		}
		commits := store.commitCount()
		closing := tkCloseSealed(t, opened.harness)
		proceed.resolve()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		if result := <-aborting; result.err != nil || result.result != "marked" {
			t.Fatalf("AbortTask %+v", result)
		}
		if store.commitCount() != commits {
			t.Fatalf("%d commits after close began, want %d", store.commitCount(), commits)
		}
		if !reflect.DeepEqual(phases.all(), []string{"one"}) {
			t.Fatalf("phases %v, want [one]", phases.all())
		}
		if abortRan.Load() {
			t.Fatal("the abort handler ran")
		}
		checkpoint := record.State.Checkpoint
		if !record.AbortRequested || record.State.Status != durable.TaskRunning || checkpoint == nil || !reflect.DeepEqual(*checkpoint, map[string]any{"phase": "two"}) {
			t.Fatalf("task %+v, want abort-marked and running at phase two", record)
		}
		_, commitErr := opened.harness.Commit(testContext, func(durable.Tx) (any, error) { return nil, nil })
		tkContainsError(t, commitErr, "closed")
	})

	t.Run("seals admission before signalling handlers and stops watches before joining them", func(t *testing.T) {
		notes := tkSessionNotes("test.close-notes")
		reached := deferred()
		fromListener := make(chan error, 1)
		var harnessRef atomic.Pointer[Harness]
		watchEnd := &syncValue[durable.WatchEnd]{}
		stubborn := tkOneStep("test.stubborn", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			// Uses a context close does not cancel, then waits for the watch to close.
			watch, err := runtime.WatchDocErased(testContext, notes)
			if err != nil {
				return err
			}
			signal := runtime.Signal()
			go func() {
				<-signal.Done()
				_, commitErr := (*harnessRef.Load()).Commit(testContext, func(durable.Tx) (any, error) { return nil, nil })
				fromListener <- commitErr
			}()
			reached.resolve()
			<-watch.Closed()
			watchEnd.put(watch.End())
			return nil
		})
		opened := tkOpenRoot(t, []durable.AnyTask{stubborn})
		harnessRef.Store(&opened.harness)
		tkSetNotes(t, opened.harness, notes, "x")
		tkStart(t, opened.root, stubborn)
		opened.harness.Resume()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, opened.harness)
		tkContainsError(t, tkWaitErr(t, fromListener), "closed")
		if end, _ := watchEnd.get(); end.Reason != durable.WatchSessionClosed {
			t.Fatalf("watch ended %+v, want session_closed", end)
		}
	})

	t.Run("writes no fault when close seals while the step after a failed phase is queued", func(t *testing.T) {
		store := newControlledStorage()
		heldGate := &syncValue[gate]{}
		var harnessRef atomic.Pointer[Harness]
		throws := tkOneStep("test.close-fault", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			held := store.holdCommits()
			heldGate.put(held)
			tkAppendBlocker(*harnessRef.Load(), runtime.ConversationId())
			// The blocker must hold the line before the step queues behind it; goroutines queue in no fixed order.
			if err := held.entered.wait(testContext); err != nil {
				return err
			}
			return errors.New("would fault")
		})
		opened := tkOpenRoot(t, []durable.AnyTask{throws}, tkOptions{storage: store})
		harnessRef.Store(&opened.harness)
		id := tkStart(t, opened.root, throws)
		opened.harness.Resume()
		eventually(t, heldGate.isSet)
		held, _ := heldGate.get()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		flush()
		closing := tkCloseSealed(t, opened.harness)
		held.release()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		if got := tkTaskWrites(store, id); !reflect.DeepEqual(got, []durable.TaskStatus{durable.TaskPending, durable.TaskRunning}) {
			t.Fatalf("task writes %v, want [pending running]", got)
		}
	})

	t.Run("starts no abort handler whose reservation settles while closing", func(t *testing.T) {
		var ran atomic.Bool
		marked := tkOneStep("test.close-abort-reservation", func(context.Context, stepRecord, stepRuntime) error { return nil }, func(context.Context, stepRuntime) error {
			ran.Store(true)
			return nil
		})
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{marked}, tkOptions{storage: store})
		id := tkStart(t, opened.root, marked)
		if result, err := opened.harness.AbortTask(testContext, id); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		held := store.holdCommits()
		opened.harness.Resume()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		closing := tkCloseSealed(t, opened.harness)
		held.release()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		if ran.Load() {
			t.Fatal("the abort handler ran")
		}
		writes := tkLastTaskWrites(store)
		if len(writes) != 1 || writes[0].Id != id || !writes[0].AbortRequested || writes[0].State.Status != durable.TaskRunning {
			t.Fatalf("last commit task writes %+v, want one abort-marked running record of %d", writes, id)
		}
	})

	t.Run("rejects a runtime commit that was queued on the line when close sealed it", func(t *testing.T) {
		store := newControlledStorage()
		queued := make(chan error, 1)
		heldGate := &syncValue[gate]{}
		var harnessRef atomic.Pointer[Harness]
		proceed, commitStarted := deferred(), deferred()
		commitDone := make(chan struct{})
		queuedTask := tkOneStep("test.close-queued-commit", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			held := store.holdCommits()
			heldGate.put(held)
			tkAppendBlocker(*harnessRef.Load(), runtime.ConversationId())
			if err := held.entered.wait(testContext); err != nil {
				return err
			}
			go func() {
				defer close(commitDone)
				queued <- runtime.Commit(testContext, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](nil), nil
				})
			}()
			commitStarted.resolve()
			// Ignores the close signal, so the invocation is still alive when the queued commit reaches the line.
			return proceed.wait(testContext)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{queuedTask}, tkOptions{storage: store})
		harnessRef.Store(&opened.harness)
		id := tkStart(t, opened.root, queuedTask)
		opened.harness.Resume()
		eventually(t, heldGate.isSet)
		held, _ := heldGate.get()
		// The commit must be queued on the line behind the held one before close seals it, as upstream's
		// runtime.commit() enters the line synchronously.
		queueOnLineIn(t, "taskRuntime).Commit", func() <-chan struct{} {
			if err := commitStarted.wait(testContext); err != nil {
				t.Fatal(err)
			}
			return commitDone
		})
		closing := tkCloseSealed(t, opened.harness)
		held.release()
		tkContainsError(t, tkWaitErr(t, queued), "Harness is closed")
		proceed.resolve()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		if got := tkTaskWrites(store, id); len(got) != 2 {
			t.Fatalf("task writes %v, want two", got)
		}
	})

	t.Run("starts no next phase when close seals during a step that decided to continue", func(t *testing.T) {
		phases := &syncList[string]{}
		registry := CreateRegistry()
		var harnessRef atomic.Pointer[Harness]
		closing := make(chan error, 1)
		var closeOnSnapshot atomic.Bool
		// The step refreshes the snapshot after progress, inside its line callback and after its closing check.
		reader := &snapshotHook{registry: registry, onSnapshot: func() {
			if closeOnSnapshot.CompareAndSwap(true, false) {
				harness := *harnessRef.Load()
				go func() { closing <- harness.Close(testContext) }()
				// Close begins on another goroutine here, where upstream's close() seals before it returns to the
				// step; wait until the scheduler's close listener has begun before the step ends.
				for deadline := time.Now().Add(10 * time.Second); !harness.(*harnessImpl).tasks.isSealed() && time.Now().Before(deadline); {
					time.Sleep(50 * time.Microsecond)
				}
			}
		}}
		type twoState struct {
			Phase string `json:"phase"`
		}
		type twoRuntime = durable.TaskRuntime[durable.JsonValue, twoState, durable.JsonValue, any]
		type twoRecord = durable.RunningTask[durable.JsonValue, twoState, durable.JsonValue]
		two := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, twoState, durable.JsonValue, any]{
			Name:    "test.close-in-step",
			Version: 1,
			Initial: func(durable.JsonValue) twoState { return twoState{Phase: "one"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, twoState, durable.JsonValue, any]{
				"one": func(ctx context.Context, _ twoRecord, runtime twoRuntime) error {
					phases.add("one")
					if err := runtime.Commit(ctx, func(durable.Tx, twoRecord) (*durable.NextTaskState[twoState, durable.JsonValue], error) {
						return runningState[twoState, durable.JsonValue](twoState{Phase: "two"}), nil
					}); err != nil {
						return err
					}
					closeOnSnapshot.Store(true)
					return nil
				},
				"two": func(context.Context, twoRecord, twoRuntime) error {
					phases.add("two")
					return nil
				},
			},
			Abort: tkNoopAbort[durable.JsonValue, twoState, durable.JsonValue, any],
		})
		addTask(t, registry, two)
		harness, err := OpenHarness(testContext, newControlledStorage(), HarnessOptions{Models: ai.CreateModels(), Registry: reader})
		if err != nil {
			t.Fatal(err)
		}
		harnessRef.Store(&harness)
		root := mustRoot(t, harness, nil)
		tkStart(t, root, two)
		harness.Resume()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(phases.all(), []string{"one"}) {
			t.Fatalf("phases %v, want [one]", phases.all())
		}
	})

	t.Run("joins a reservation that settles while closing without starting its handler", func(t *testing.T) {
		var ran atomic.Bool
		never := tkOneStep("test.close-reservation", func(context.Context, stepRecord, stepRuntime) error {
			ran.Store(true)
			return nil
		})
		store := newControlledStorage()
		opened := tkOpenRoot(t, []durable.AnyTask{never}, tkOptions{storage: store})
		id := tkStart(t, opened.root, never)
		held := store.holdCommits()
		opened.harness.Resume()
		if err := held.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		closing := tkCloseSealed(t, opened.harness)
		held.release()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		if ran.Load() {
			t.Fatal("the handler ran")
		}
		writes := tkLastTaskWrites(store)
		if len(writes) != 1 || writes[0].Id != id || writes[0].State.Status != durable.TaskRunning {
			t.Fatalf("last commit task writes %+v, want one running record of %d", writes, id)
		}
	})
}

// snapshotHook is a registry reader that calls onSnapshot before every snapshot.
type snapshotHook struct {
	registry   Registry
	onSnapshot func()
}

func (reader *snapshotHook) Snapshot() durable.RegistrySnapshot {
	reader.onSnapshot()
	return reader.registry.Snapshot()
}

func (reader *snapshotHook) Subscribe(listener func()) func() {
	return reader.registry.Subscribe(listener)
}

// scheduler.ts #seal sets #closing and aborts every invocation's controller in one synchronous turn, so a commit that
// the closing flag rejects always finds its signal aborted; the tool progress writer relies on that to treat the
// rejection as expected (tool.ts:327-328). Go observers run on other goroutines, so the flag and the abort must be
// published together under the scheduler's lock.
func TestTaskCloseAbortsHandlersBeforeClosingIsObservable(t *testing.T) {
	reached := deferred()
	var signal atomic.Pointer[context.Context]
	running := tkOneStep("test.seal-order", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
		current := runtime.Signal()
		signal.Store(&current)
		reached.resolve()
		<-current.Done()
		return nil
	})
	opened := tkOpenRoot(t, []durable.AnyTask{running})
	tkStart(t, opened.root, running)
	opened.harness.Resume()
	if err := reached.wait(testContext); err != nil {
		t.Fatal(err)
	}
	scheduler := opened.harness.(*harnessImpl).tasks
	// Seal does its other work between publishing the flag and aborting; slowing it widens that gap without changing the order.
	scheduler.mu.Lock()
	unsubscribe := scheduler.unsubscribeRegistry
	scheduler.unsubscribeRegistry = func() {
		time.Sleep(20 * time.Millisecond)
		unsubscribe()
	}
	scheduler.mu.Unlock()
	handlerSignal := *signal.Load()
	violation := make(chan bool, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		for {
			scheduler.mu.Lock()
			closing := scheduler.closing
			scheduler.mu.Unlock()
			if closing {
				violation <- handlerSignal.Err() == nil
				return
			}
		}
	}()
	<-started
	closing := make(chan error, 1)
	go func() { closing <- opened.harness.Close(testContext) }()
	if observed := <-violation; observed {
		t.Error("the scheduler's closing flag was visible while an invocation's signal was still live")
	}
	if err := tkWaitErr(t, closing); err != nil {
		t.Fatal(err)
	}
}
