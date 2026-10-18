package harness

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports the "listener and report errors" group of packages/durable/test/harness-storage-failure.test.ts (Pi 1.2.0). The
// cases about rejecting promises and thenables have no Go counterpart (a Go callback returns no promise) and skip.

func TestListenerAndReportErrors(t *testing.T) {
	// harness-storage-failure.test.ts:431 it("reports a listener or onReport whose promise rejects, without an unhandled rejection")
	t.Run("reports a listener or onReport whose promise rejects, without an unhandled rejection", func(t *testing.T) {
		t.Skip("Pi harness-storage-failure.test.ts:431: a Go listener or OnReport returns no promise, so there is no rejection to " +
			"report and no unhandledRejection to observe; a panicking listener is the next case, a panicking OnReport is TestSafeNow")
	})

	// harness-storage-failure.test.ts:466 it("reports a throwing commit or close listener and runs the others; the commit stands")
	t.Run("reports a throwing commit or close listener and runs the others; the commit stands", func(t *testing.T) {
		harness, root, reports := sfStarted(t, newControlledStorage(), &signalLog{})
		var seen []string
		harness.SubscribeCommits(func(context.Context, durable.CommitPublication) { panic(errors.New("commit listener")) })
		harness.SubscribeCommits(func(context.Context, durable.CommitPublication) { seen = append(seen, "commit") })
		harness.SubscribeClose(func() { panic(errors.New("close listener")) })
		harness.SubscribeClose(func() { seen = append(seen, "close") })
		if err := sfAppendNote(root); err != nil {
			t.Fatalf("the commit returned %v, want success", err)
		}
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(seen, []string{"commit", "close"}) {
			t.Fatalf("listeners that ran: %v", seen)
		}
		var messages []string
		for _, report := range reports.all() {
			messages = append(messages, report.Error())
		}
		if !slices.Equal(messages, []string{"commit listener", "close listener"}) {
			t.Fatalf("reports %v, want the commit listener's and the close listener's errors", messages)
		}
	})

	// harness-storage-failure.test.ts:484 it("keeps feeding the other observers of a view when one throws")
	t.Run("keeps feeding the other observers of a view when one throws", func(t *testing.T) {
		harness, root, reports := sfStarted(t, newControlledStorage(), &signalLog{})
		views := harness.(*harnessImpl).host.views
		failure := errors.New("observer broke")
		if _, _, err := views.Attach(testContext, root.Id(), func(ConversationView, func(), durable.Storage) (*ViewObserver, error) {
			return &ViewObserver{
				Publication: func(context.Context, ConversationView, ConversationView, []durable.Op, durable.CommitPublication) {
					panic(failure)
				},
				CloseSession: func(error) {},
			}, nil
		}); err != nil {
			t.Fatal(err)
		}
		watch := must(root.Watch(testContext))
		seen := make(chan ConversationView, 8)
		watch.Start(func(_ context.Context, value ConversationView, _ []durable.Op) error {
			seen <- value
			return nil
		})
		if err := sfAppendNote(root); err != nil {
			t.Fatal(err)
		}
		select {
		case <-seen:
		case <-time.After(10 * time.Second):
			t.Fatal("the watch behind the throwing observer never received the revision")
		}
		sfReports(t, reports, failure)
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
		<-watch.Closed()
		if end := watch.End(); end.Reason != durable.WatchSessionClosed {
			t.Fatalf("the watch ended %+v, want session_closed", end)
		}
	})

	// harness-storage-failure.test.ts:510 it("contains a fulfilled thenable that calls only its fulfillment handler")
	t.Run("contains a fulfilled thenable that calls only its fulfillment handler", func(t *testing.T) {
		t.Skip("Pi harness-storage-failure.test.ts:510: Go has no thenables; session.Contained runs a plain callback")
	})

	// harness-storage-failure.test.ts:533 it("contains a rejected promise from another realm")
	t.Run("contains a rejected promise from another realm", func(t *testing.T) {
		t.Skip("Pi harness-storage-failure.test.ts:533: Go has no promises or realms (node:vm); a panicking callback is contained " +
			"by the cases above")
	})

	// PiG coverage beyond the Pi file: the task graph's and the replicated states' observers, contained as view.ts is.
	t.Run("keeps feeding the other observers of the task graph when one throws, and closes them all", func(t *testing.T) {
		// Pi task-graph.ts advance (:203) and the close subscription (:77) contain each observer's call. The graph's own
		// observers do not throw, so a throwing one is mounted directly in front of a watch.
		log := &signalLog{}
		harness, root, reports := sfStarted(t, newControlledStorage(), log)
		graph := harness.(*harnessImpl).taskGraph
		watch := must(graph.Watch(testContext))
		failure := errors.New("graph observer broke")
		graph.mu.Lock()
		graph.mount.observers = append([]taskGraphObserver{throwingGraphObserver{failure}}, graph.mount.observers...)
		graph.mu.Unlock()
		seen := make(chan TaskGraph, 8)
		watch.Start(func(_ context.Context, value TaskGraph, _ []durable.Op) error {
			seen <- value
			return nil
		})
		if _, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
			return durable.CreateTask(tx, waitingTask(log), struct{}{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		}); err != nil {
			t.Fatalf("the commit returned %v, want success", err)
		}
		select {
		case value := <-seen:
			if len(value.Tasks) == 0 {
				t.Fatalf("the watch received %+v, want the new task", value)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the watch behind the throwing observer never received the revision")
		}
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
		select {
		case <-watch.Closed():
		case <-time.After(10 * time.Second):
			t.Fatal("the watch behind the throwing observer never closed with the Session")
		}
		if end := watch.End(); end.Reason != durable.WatchSessionClosed {
			t.Fatalf("the watch ended %+v, want session_closed", end)
		}
		// The advance and the close of the throwing observer each failed once, and each was reported.
		sfReports(t, reports, failure, failure)
	})

	t.Run("reports the failure of a listener of a view's state or of the task graph's state", func(t *testing.T) {
		// Pi view.ts and task-graph.ts: `replicatedState(observer, { onError: (error) => this.#session.report(error) })`.
		harness, root, reports := sfStarted(t, newControlledStorage(), &signalLog{})
		impl := harness.(*harnessImpl)
		viewState := must(impl.host.views.State(testContext, root.Id()))
		graphState := must(impl.taskGraph.State(testContext))
		failure := errors.New("state listener failed")
		viewState.Subscribe(func(ConversationView, context.Context, chord.ReplicatedStateDelivery) { panic(failure) })
		graphState.Subscribe(func(TaskGraph, context.Context, chord.ReplicatedStateDelivery) { panic(failure) })
		impl.WaitDeliveries()
		sfReports(t, reports, failure, failure)
		viewState.Dispose()
		graphState.Dispose()
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:552 it("reports a throwing watch listener, which ends that watch")
	t.Run("reports a throwing watch listener, which ends that watch", func(t *testing.T) {
		harness, root, reports := sfStarted(t, newControlledStorage(), &signalLog{})
		watch := must(harness.WatchDocErased(testContext, LiveDoc, root.Id()))
		failure := errors.New("listener broke")
		watch.Start(func(context.Context, durable.JsonObject, []durable.Op) error { return failure })
		if _, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
			live, err := docDraft(tx, LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			return nil, live.Set("compactions", []any{})
		}); err != nil {
			t.Fatal(err)
		}
		<-watch.Closed()
		if end := watch.End(); end.Reason != durable.WatchListenerError || !errors.Is(end.Error, failure) {
			t.Fatalf("the watch ended %+v, want listener_error with the listener's error", end)
		}
		sfReports(t, reports, failure)
		_ = harness.Close(testContext)
	})
}

type throwingGraphObserver struct{ failure error }

func (observer throwingGraphObserver) Advance(context.Context, TaskGraph, []durable.Op) {
	panic(observer.failure)
}

func (observer throwingGraphObserver) CloseSession(error) { panic(observer.failure) }

func TestSafeNow(t *testing.T) {
	// harness-storage-failure.test.ts:603 it("reports a throwing clock once, also when the report handler reads the clock again")
	t.Run("reports a throwing clock once, also when the report handler reads the clock again", func(t *testing.T) {
		var reports []error
		var clock func() float64
		clock = SafeNow(func() float64 { panic(errors.New("clock broke")) }, func(err error, _ durable.ReportInfo) {
			reports = append(reports, err)
			// Reentry, as a handler that commits does: the clock is read again before this report returns.
			if len(reports) < 5 {
				clock()
			}
		})
		if got := clock(); got <= 0 {
			t.Fatalf("a broken clock returned %v, want the wall clock", got)
		}
		clock()
		if len(reports) != 1 || reports[0].Error() != "clock broke" {
			t.Fatalf("reports %v, want the clock's error once", reports)
		}
	})

	// harness-storage-failure.test.ts:620 it("keeps working when onReport throws, and falls back to Date.now when the clock throws")
	t.Run("keeps working when onReport throws, and falls back to Date.now when the clock throws", func(t *testing.T) {
		var mu sync.Mutex
		reported := 0
		log := &signalLog{}
		registry := CreateRegistry()
		mustInstall(t, registry, new(durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{waitingTask(log).Erased()}}))
		harness := must(OpenHarness(testContext, newControlledStorage(), HarnessOptions{
			Models:   ai.CreateModels(),
			Registry: registry,
			Now:      func() float64 { panic(errors.New("clock broke")) },
			OnReport: func(error, durable.ReportInfo) {
				mu.Lock()
				reported++
				mu.Unlock()
				panic(errors.New("report broke"))
			},
		}))
		root := mustRoot(t, harness, nil)
		waiting := waitingTask(log)
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, waiting, struct{}{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		}))
		harness.Resume()
		eventually(t, func() bool {
			record, _ := harness.GetTask(testContext, id)
			return record != nil && record.State.Status == durable.TaskRunning
		})
		record, err := harness.GetTask(testContext, id)
		if err != nil || record.StartedAt == nil || *record.StartedAt <= 0 {
			t.Fatalf("task %+v (%v), want a startedAt from the wall clock", record, err)
		}
		if _, err := harness.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		if outcome := must(harness.WaitForTask(testContext, id)); outcome.State.Outcome == nil || outcome.State.Outcome.Status != durable.OutcomeAborted {
			t.Fatalf("outcome %+v, want aborted", outcome.State.Outcome)
		}
		mu.Lock()
		defer mu.Unlock()
		if reported != 1 {
			t.Fatalf("onReport ran %d times, want the clock's failure reported once", reported)
		}
		_ = harness.Close(testContext)
	})
}

// unformattable is a value whose formatting panics, the Go form of Pi's Object.create(null) (no toString).
type unformattable struct{}

func (unformattable) String() string { panic("cannot be formatted") }

func TestAThrownValueThatCannotBeFormatted(t *testing.T) {
	// harness-storage-failure.test.ts:567 it("faults only the task, and ends only the watch, for a thrown value that cannot be
	// formatted")
	t.Run("faults only the task, and ends only the watch, for a thrown value that cannot be formatted", func(t *testing.T) {
		throws := durable.DefineTask(durable.TaskDefinition[struct{}, stepState, durable.JsonValue, any]{
			Name:    "test.throws-unformattable",
			Version: 1,
			Initial: func(struct{}) stepState { return stepState{Phase: "work"} },
			Phases: map[string]durable.PhaseHandler[struct{}, stepState, durable.JsonValue, any]{
				"work": func(context.Context, durable.RunningTask[struct{}, stepState, durable.JsonValue], durable.TaskRuntime[struct{}, stepState, durable.JsonValue, any]) error {
					panic(unformattable{})
				},
			},
			Abort: func(context.Context, durable.RunningTask[struct{}, stepState, durable.JsonValue], durable.TaskRuntime[struct{}, stepState, durable.JsonValue, any]) error {
				return nil
			},
		})
		harness, _, _ := openTasks(t, newControlledStorage(), []durable.AnyTask{throws.Erased()})
		root := mustRoot(t, harness, nil)
		watch := must(harness.WatchDocErased(testContext, LiveDoc, root.Id()))
		watch.Start(func(context.Context, durable.JsonObject, []durable.Op) error { panic(unformattable{}) })
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, throws, struct{}{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		}))
		harness.Resume()
		outcome := must(harness.WaitForTask(testContext, id)).State.Outcome
		if outcome == nil || outcome.Status != durable.OutcomeFaulted || outcome.Error == nil || outcome.Error.Message != "[object Object]" {
			t.Fatalf("outcome %+v (error %+v), want faulted with the message [object Object]", outcome, outcome.Error)
		}
		if _, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
			live, err := docDraft(tx, LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			return nil, live.Set("compactions", []any{})
		}); err != nil {
			t.Fatal(err)
		}
		<-watch.Closed()
		if end := watch.End(); end.Reason != durable.WatchListenerError {
			t.Fatalf("the watch ended %+v, want listener_error", end)
		}
		if err := sfAppendNote(root); err != nil {
			t.Fatalf("a commit after the failures returned %v, want success", err)
		}
		mustClose(t, harness)
	})
}
