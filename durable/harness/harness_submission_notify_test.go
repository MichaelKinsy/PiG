package harness

// Ports packages/durable/test/harness-park-children.test.ts (Pi 473515f9c), describe "submissions that notify a task"
// (spec suspendable §4.5): SubmissionDraft.Notify delivers SubmissionSettled, the settled record, to a task in the
// commit that settles the submission. The nested-call, keyed-task, and restart cases of the same file are in
// harness_park_children_test.go.

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

const notifyMemoPrefix = "pi.signal.pi.submission-settled/"

// notifyWaiter sleeps on SubmissionSettled under each of keys, then completes with the status of the settled record held
// under the first key, or null when none is (`Waiter` and `Two` of the Pi file).
func notifyWaiter(name string, keys ...string) stepTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    name,
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "sleep"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"sleep": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					woke := stepState{Phase: "woke"}
					signals := make([]durable.SignalKey, len(keys))
					for index, key := range keys {
						signals[index] = durable.SubmissionSettledSignal.Key(key)
					}
					return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &woke, Signals: signals}, nil
				})
			},
			"woke": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				state, err := durable.ReadSignal(ctx, runtime, durable.SubmissionSettledSignal, keys[0])
				if err != nil {
					return err
				}
				var status durable.JsonValue
				if state != nil && !state.Expired {
					status = string(state.Value.Status)
				}
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](status), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue]("test"), nil
			})
		},
	})
}

// notifyOpen is open() of the Pi file: a Harness over storage with scheduling resumed.
func notifyOpen(t *testing.T, setup *chatState, store durable.Storage) (Harness, Conversation) {
	t.Helper()
	harness, root := openChat(t, store, setup)
	harness.Resume()
	return harness, root
}

func notifyStartWaiter(t *testing.T, root Conversation, waiter stepTask) durable.TaskId {
	t.Helper()
	return tkStart(t, root, waiter.Erased())
}

func notifyWaitSleeping(t *testing.T, harness Harness, id durable.TaskId) {
	t.Helper()
	waitFor(t, func() bool {
		record, err := harness.GetTask(testContext, id)
		return err == nil && record != nil && record.State.Status == durable.TaskSleeping
	})
}

func notifyCompleted(t *testing.T, harness Harness, id durable.TaskId, result durable.JsonValue) {
	t.Helper()
	receipt := must(harness.WaitForTask(testContext, id))
	want := durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}
	if receipt.State.Outcome == nil || receipt.State.Outcome.Status != want.Status || !reflect.DeepEqual(*receipt.State.Outcome.Result, result) {
		t.Fatalf("outcome %+v, want completed with %v", receipt.State.Outcome, result)
	}
}

// notifyBatch is the first batch of the storage's commits that has a write satisfying match.
func notifyBatch(store *controlledStorage, match func(durable.StorageWrite) bool) []durable.StorageWrite {
	for index := range store.commitCount() {
		if batch := store.commitAt(index); slices.ContainsFunc(batch, match) {
			return batch
		}
	}
	return nil
}

func notifySubmissionDone(write durable.StorageWrite, id durable.SubmissionId) bool {
	submission, ok := write.(durable.SubmissionWrite)
	return ok && submission.Value.Id == id && submission.Value.Status == durable.SubmissionDone
}

func TestSubmissionsThatNotifyATask(t *testing.T) {
	t.Run("deliver SubmissionSettled in the commit that settles the submission", func(t *testing.T) {
		setup := chatSetup(t)
		waiterTask := notifyWaiter("test.waiter", "settled")
		addTask(t, setup.Registry, waiterTask.Erased())
		gate := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAfter(gate, "answer")})
		store := newControlledStorage()
		harness, root := notifyOpen(t, setup, store)
		waiter := notifyStartWaiter(t, root, waiterTask)
		notifyWaitSleeping(t, harness, waiter)
		submission := must(root.Submit(testContext, durable.SubmissionDraft{
			Type: durable.SubmissionTypeInput, Content: ai.UserText("go"), Notify: &durable.SubmissionNotify{TaskId: waiter, Key: "settled"},
		}))
		gate.resolve()
		if settled := must(submission.Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", settled.Status)
		}
		notifyCompleted(t, harness, waiter, "done")
		// One batch settles the submission and stores the signal in the waiter.
		settling := notifyBatch(store, func(write durable.StorageWrite) bool { return notifySubmissionDone(write, submission.Id()) })
		var signalled *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
		for _, write := range settling {
			if task, ok := write.(durable.TaskWrite); ok && task.Value.Id == waiter {
				signalled = &task.Value
			}
		}
		if signalled == nil {
			t.Fatalf("the settling batch %v has no write of the waiter", settling)
		}
		if _, held := signalled.Memos[notifyMemoPrefix+"settled"]; !held {
			t.Fatalf("waiter memos %v lack %s", signalled.Memos, notifyMemoPrefix+"settled")
		}
		closeHarness(t, harness)
	})

	t.Run("deliver it for a submission created settled, before the task ran, which then wakes at once", func(t *testing.T) {
		setup := chatSetup(t)
		harness, root := notifyOpen(t, setup, storage.NewMemoryStorage())
		// Blocked until its definition is installed, so it has not run when the write settles at its admission.
		waiterTask := notifyWaiter("test.waiter", "settled")
		waiter := notifyStartWaiter(t, root, waiterTask)
		write := must(root.Submit(testContext, durable.SubmissionDraft{
			Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "test.note", Data: "hi"}, Notify: &durable.SubmissionNotify{TaskId: waiter, Key: "settled"},
		}))
		if status := must(write.Status(testContext)); status.Status != durable.SubmissionDone {
			t.Fatalf("write status %s, want done", status.Status)
		}
		if record := must(harness.GetTask(testContext, waiter)); record.StartedAt != nil {
			t.Fatalf("waiter started at %v before its definition was installed", *record.StartedAt)
		}
		addTask(t, setup.Registry, waiterTask.Erased())
		notifyCompleted(t, harness, waiter, "done")
		closeHarness(t, harness)
	})

	t.Run("compose in one commit: two queued writes placed together signal one task under two keys", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAfter(gate, "answer")})
		store := newControlledStorage()
		harness, root := notifyOpen(t, setup, store)
		two := notifyWaiter("test.two-keys", "one", "two")
		addTask(t, setup.Registry, two.Erased())
		waiter := notifyStartWaiter(t, root, two)
		notifyWaitSleeping(t, harness, waiter)
		// Busy: the writes, which the model sees, queue until the run's final boundary places both.
		run := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")}))
		note := func(key string) durable.Submission {
			return must(root.Submit(testContext, durable.SubmissionDraft{
				Type:   durable.SubmissionTypeWrite,
				Entry:  &durable.EntryDraft{Kind: "test.note", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(key), Timestamp: 0}}},
				Notify: &durable.SubmissionNotify{TaskId: waiter, Key: key},
			}))
		}
		first := note("one")
		second := note("two")
		gate.resolve()
		must(run.Wait(testContext))
		if got := []durable.SubmissionStatus{must(first.Wait(testContext)).Status, must(second.Wait(testContext)).Status}; !reflect.DeepEqual(got, []durable.SubmissionStatus{durable.SubmissionDone, durable.SubmissionDone}) {
			t.Fatalf("statuses %v, want done, done", got)
		}
		placing := notifyBatch(store, func(write durable.StorageWrite) bool { return notifySubmissionDone(write, second.Id()) })
		// Both placed in this batch, and the waiter written once: one transaction composed both deliveries.
		if !slices.ContainsFunc(placing, func(write durable.StorageWrite) bool { return notifySubmissionDone(write, first.Id()) }) {
			t.Fatalf("the batch placing the second write does not settle the first: %v", placing)
		}
		var writes []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
		for _, write := range placing {
			if task, ok := write.(durable.TaskWrite); ok && task.Value.Id == waiter {
				writes = append(writes, task.Value)
			}
		}
		if len(writes) != 1 {
			t.Fatalf("%d writes of the waiter in the batch, want 1", len(writes))
		}
		memos := make([]string, 0, len(writes[0].Memos))
		for name := range writes[0].Memos {
			memos = append(memos, name)
		}
		slices.Sort(memos)
		if want := []string{notifyMemoPrefix + "one", notifyMemoPrefix + "two"}; !reflect.DeepEqual(memos, want) {
			t.Fatalf("memos %v, want %v", memos, want)
		}
		must(harness.WaitForTask(testContext, waiter))
		closeHarness(t, harness)
	})

	t.Run("refuse a delivery of SubmissionSettled that is no settled record", func(t *testing.T) {
		setup := chatSetup(t)
		waiterTask := notifyWaiter("test.waiter", "settled")
		addTask(t, setup.Registry, waiterTask.Erased())
		harness, root := notifyOpen(t, setup, storage.NewMemoryStorage())
		waiter := notifyStartWaiter(t, root, waiterTask)
		signal := func(value durable.JsonValue) durable.SignalResult {
			return must(harness.SignalErased(testContext, waiter, durable.SubmissionSettledSignal, "settled", value))
		}
		if got := signal(float64(17)); got != durable.SignalInvalid {
			t.Fatalf("a number: %s, want invalid", got)
		}
		// A done input without its entry and answer is no settled record either.
		partial := delta.JsonObjectOf("id", float64(2), "conversationId", float64(1), "type", "input", "status", "done")
		if got := signal(partial); got != durable.SignalInvalid {
			t.Fatalf("a partial record: %s, want invalid", got)
		}
		badEntry := delta.JsonObjectOf("id", float64(2), "conversationId", float64(1), "type", "input", "status", "unanswered", "reason", "x", "entry", "e1")
		if got := signal(badEntry); got != durable.SignalInvalid {
			t.Fatalf("a bad entry: %s, want invalid", got)
		}
		must(harness.AbortTask(testContext, waiter))
		closeHarness(t, harness)
	})

	t.Run("settle without failing when the task to notify is gone", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		harness, root := notifyOpen(t, setup, storage.NewMemoryStorage())
		submission := must(root.Submit(testContext, durable.SubmissionDraft{
			Type: durable.SubmissionTypeInput, Content: ai.UserText("go"), Notify: &durable.SubmissionNotify{TaskId: 999_999, Key: "settled"},
		}))
		if settled := must(submission.Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("status %s, want done", settled.Status)
		}
		closeHarness(t, harness)
	})
}
