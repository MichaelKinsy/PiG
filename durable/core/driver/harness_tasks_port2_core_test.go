// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

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
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// More of harness-tasks.test.ts through driver.OpenHarness over the real core.

type collectState struct {
	Phase string   `json:"phase"`
	Items []string `json:"items"`
}

// harness-tasks.test.ts:160 "compares checkpoints by value, including arrays": two rounds of progress, then an equal copy of the
// checkpoint, which is no progress, so the task faults.
func TestCoreTaskComparesCheckpointsByValueIncludingArrays(t *testing.T) {
	collect := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, collectState, durable.JsonValue, any]{
		Name: "test.collect", Version: 1,
		Initial: func(durable.JsonValue) collectState { return collectState{Phase: "collect", Items: []string{}} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, collectState, durable.JsonValue, any]{
			"collect": func(ctx context.Context, task durable.RunningTask[durable.JsonValue, collectState, durable.JsonValue], runtime durable.TaskRuntime[durable.JsonValue, collectState, durable.JsonValue, any]) error {
				items := task.State.Checkpoint.Items
				next := slices.Clone(items)
				if len(items) < 2 {
					next = append(next, "item"+string(rune('0'+len(items))))
				}
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, collectState, durable.JsonValue]) (*durable.NextTaskState[collectState, durable.JsonValue], error) {
					return &durable.NextTaskState[collectState, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &collectState{Phase: "collect", Items: next}}, nil
				})
			},
		},
		Abort: func(context.Context, durable.RunningTask[durable.JsonValue, collectState, durable.JsonValue], durable.TaskRuntime[durable.JsonValue, collectState, durable.JsonValue, any]) error {
			return nil
		},
	})
	h, root := openTasksHarness(t, collect.Erased())
	id := startTask(t, root, collect.Erased())
	outcome := waitSettled(t, h, id).State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeFaulted || outcome.Error == nil ||
		outcome.Error.Message != "Task test.collect phase collect returned without durable progress" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

type progressLines struct {
	Lines []string `json:"lines"`
}

type (
	writerState struct {
		Phase string `json:"phase"`
	}
	writerResult struct {
		EntryId durable.EntryId `json:"entryId"`
	}
	writerRuntime = durable.TaskRuntime[durable.JsonValue, writerState, writerResult, any]
	writerRecord  = durable.RunningTask[durable.JsonValue, writerState, writerResult]
	writerNext    = durable.NextTaskState[writerState, writerResult]
)

// harness-tasks.test.ts:188 "commits results with entries atomically, keeps memos until terminal, and retires task documents".
func TestCoreTaskCommitsResultsWithEntriesKeepsMemosUntilTerminalAndRetiresDocuments(t *testing.T) {
	progress := durable.DefineDoc(durable.DocDefinition[progressLines]{
		CommonDocDefinition: durable.CommonDocDefinition[progressLines]{Kind: "test.task-progress", Version: 1, Initial: func() progressLines { return progressLines{Lines: []string{}} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeTask},
	})
	answer := durable.DefineEntry[durable.Never]("answer")
	child := quickTask("test.child")
	var childId atomic.Int64
	var mu sync.Mutex
	var seen []any
	var winner any
	writer := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, writerState, writerResult, any]{
		Name: "test.writer", Version: 1,
		Initial: func(durable.JsonValue) writerState { return writerState{Phase: "write"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, writerState, writerResult, any]{
			"write": func(ctx context.Context, task writerRecord, runtime writerRuntime) error {
				if _, ok, err := runtime.Memo(ctx, "choice"); err != nil || ok {
					return fmt.Errorf("memo before write: %v: %w", ok, err)
				}
				// Upstream races two candidates through Promise.all, where the first call queues first. Goroutines have no such
				// order, so either candidate may win; both callers must see the one durable winner.
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
				mu.Lock()
				winner = results[0]
				mu.Unlock()
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
				return runtime.Commit(ctx, func(tx durable.Tx, _ writerRecord) (*writerNext, error) {
					lines, err := durable.TxDoc[progressLines](tx, progress, task.Id)
					if err != nil {
						return nil, err
					}
					if _, err := lines.Array("lines").Push("wrote"); err != nil {
						return nil, err
					}
					// Task creation defaults to the task's own conversation.
					created, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
					if err != nil {
						return nil, err
					}
					childId.Store(int64(created))
					return &writerNext{Status: durable.TaskRunning, Checkpoint: &writerState{Phase: "answer"}}, nil
				})
			},
			"answer": func(ctx context.Context, task writerRecord, runtime writerRuntime) error {
				mu.Lock()
				want := winner
				mu.Unlock()
				if !reflect.DeepEqual(task.Memos, map[string]durable.JsonValue{"choice": want, "toString": "own"}) {
					return fmt.Errorf("memos %v", task.Memos)
				}
				if err := runtime.Commit(ctx, func(tx durable.Tx, _ writerRecord) (*writerNext, error) {
					lines, err := durable.TxDoc[progressLines](tx, progress, task.Id)
					if err != nil {
						return nil, err
					}
					mu.Lock()
					seen = lines.Array("lines").Snapshot()
					mu.Unlock()
					return nil, nil
				}); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx durable.Tx, current writerRecord) (*writerNext, error) {
					entry, err := tx.AppendEntry(current.ConversationId, durable.EntryDraft{Kind: "answer", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("done"), Timestamp: 1}}})
					if err != nil {
						return nil, err
					}
					result := writerResult{EntryId: entry.Id}
					return &writerNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[writerResult]{Status: durable.OutcomeCompleted, Result: &result}}, nil
				})
			},
		},
		Abort: func(context.Context, writerRecord, writerRuntime) error { return nil },
	})
	h, root := openTasksHarness(t, writer.Erased(), child.Erased())
	conversation, err := h.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
	if err != nil {
		t.Fatal(err)
	}
	id := startTask(t, conversation, writer.Erased())
	receipt := waitSettled(t, h, id)
	if receipt.State.Checkpoint != nil || receipt.Memos != nil {
		t.Fatalf("receipt keeps checkpoint %v or memos %v", receipt.State.Checkpoint, receipt.Memos)
	}
	outcome := receipt.State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeCompleted || outcome.Result == nil {
		t.Fatalf("outcome = %+v", outcome)
	}
	result, err := durable.FromJsonValue[writerResult](*outcome.Result)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := durable.Commit(t.Context(), h, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(result.EntryId) })
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Is(entry) || entry.ConversationId != conversation.Id() {
		t.Fatalf("entry %+v is not an answer of conversation %d", entry, conversation.Id())
	}
	mu.Lock()
	got := seen
	mu.Unlock()
	if !reflect.DeepEqual(got, []any{"wrote"}) {
		t.Fatalf("progress seen %v", got)
	}
	if retired, err := h.SnapshotErased(t.Context(), progress, id); err != nil || retired != nil {
		t.Fatalf("task document %v %v, want retired", retired, err)
	}
	if stored, err := h.GetTask(t.Context(), id); err != nil || !reflect.DeepEqual(*stored, receipt) {
		t.Fatalf("stored task %+v %v differs from receipt %+v", stored, err, receipt)
	}
	childReceipt := waitSettled(t, h, durable.TaskId(childId.Load()))
	if childReceipt.ConversationId != conversation.Id() {
		t.Fatalf("child receipt conversation %d, want %d", childReceipt.ConversationId, conversation.Id())
	}
	if root.Id() == conversation.Id() {
		t.Fatal("root and created conversation share an ID")
	}
}

// harness-tasks.test.ts:478 "rejects an agent() wait whose caller context is cancelled, without affecting the phase's resolution".
func TestCoreTaskRejectsAnAgentWaitWhoseCallerIsCancelledWithoutAffectingTheResolution(t *testing.T) {
	var mu sync.Mutex
	var cancelled error
	var level ai.ModelThinkingLevel
	agent := oneStepTask("test.agent-cancel", func(ctx context.Context, rt stepRuntime) error {
		caller, cancel := context.WithCancelCause(ctx)
		cancel(errors.New("caller gone"))
		_, err := rt.Agent(caller)
		resolved, err2 := rt.Agent(ctx)
		if err2 != nil {
			return err2
		}
		mu.Lock()
		cancelled, level = err, resolved.ThinkingLevel
		mu.Unlock()
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
	})
	h, root := openTasksHarness(t, agent.Erased())
	waitSettled(t, h, startTask(t, root, agent.Erased()))
	mu.Lock()
	defer mu.Unlock()
	if cancelled == nil || cancelled.Error() != "caller gone" {
		t.Fatalf("agent wait %v, want caller gone", cancelled)
	}
	if level != ai.ThinkingOff {
		t.Fatalf("thinking level %q, want off", level)
	}
}

// harness-tasks.test.ts:495 "observes a failed agent resolution whose only caller stopped waiting": the resolution the cancelled
// caller started fails and nobody waits for it; the next caller sees the failure.
func TestCoreTaskObservesAFailedAgentResolutionWhoseOnlyCallerStoppedWaiting(t *testing.T) {
	var fail atomic.Bool
	var mu sync.Mutex
	var resolved error
	agent := oneStepTask("test.agent-failure", func(ctx context.Context, rt stepRuntime) error {
		caller, cancel := context.WithCancelCause(ctx)
		cancel(errors.New("caller gone"))
		fail.Store(true)
		_, _ = rt.Agent(caller)
		_, err := rt.Agent(ctx)
		mu.Lock()
		resolved = err
		mu.Unlock()
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
	})
	h, root := openTasksWith(t, harness.HarnessOptions{Settings: func() *harness.HarnessSettings {
		if fail.Load() {
			panic(errors.New("settings broke"))
		}
		return nil
	}}, agent.Erased())
	waitSettled(t, h, startTask(t, root, agent.Erased()))
	mu.Lock()
	defer mu.Unlock()
	if resolved == nil || resolved.Error() != "settings broke" {
		t.Fatalf("agent resolution %v, want settings broke", resolved)
	}
}

type runtimeNotes struct {
	Text string `json:"text"`
}

// harness-tasks.test.ts:579 "reads committed documents and context through the runtime, and forwards the clock and reports".
func TestCoreTaskReadsCommittedDocumentsAndContextThroughTheRuntimeAndForwardsClockAndReports(t *testing.T) {
	notes := durable.DefineDoc(durable.DocDefinition[runtimeNotes]{
		CommonDocDefinition: durable.CommonDocDefinition[runtimeNotes]{Kind: "test.runtime-notes", Version: 1, Initial: func() runtimeNotes { return runtimeNotes{} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
	})
	captured := make(chan stepRuntime, 1)
	var mu sync.Mutex
	var seen []any
	reader := oneStepTask("test.reader", func(ctx context.Context, rt stepRuntime) error {
		captured <- rt
		view, err := rt.Context(ctx, rt.ConversationId(), nil)
		if err != nil {
			return err
		}
		first, second := view.Entries[0].Id, view.Entries[1].Id
		current, err := durable.Snapshot[runtimeNotes](ctx, rt, notes, rt.ConversationId())
		if err != nil {
			return err
		}
		asOf, err := durable.SnapshotAsOf[runtimeNotes](ctx, rt, notes, first, rt.ConversationId())
		if err != nil {
			return err
		}
		atFirst, err := rt.Context(ctx, rt.ConversationId(), &durable.ContextOptions{At: &first})
		if err != nil {
			return err
		}
		atSecond, err := rt.Context(ctx, rt.ConversationId(), &durable.ContextOptions{At: &second})
		if err != nil {
			return err
		}
		mu.Lock()
		seen = []any{current.Text, asOf.Text, len(atFirst.Entries), len(atSecond.Messages), rt.Now()}
		mu.Unlock()
		rt.Report(errors.New("reported"))
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
	})
	var reportsMu sync.Mutex
	var reports []string
	h, root := openTasksWith(t, harness.HarnessOptions{
		Now: func() float64 { return 1234 },
		OnReport: func(err error, _ durable.ReportInfo) {
			reportsMu.Lock()
			reports = append(reports, err.Error())
			reportsMu.Unlock()
		},
	}, reader.Erased())
	for _, text := range []string{"one", "two"} {
		if _, err := root.Commit(t.Context(), func(tx durable.Tx) (any, error) {
			draft, err := durable.TxDoc[runtimeNotes](tx, notes, root.Id())
			if err != nil {
				return nil, err
			}
			if err := draft.Set("text", text); err != nil {
				return nil, err
			}
			_, err = tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(text), Timestamp: 1}}})
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if outcome := waitSettled(t, h, startTask(t, root, reader.Erased())).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("outcome = %+v (error %+v)", outcome, outcome.Error)
	}
	mu.Lock()
	got := seen
	mu.Unlock()
	if want := []any{"two", "one", 1, 2, float64(1234)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("seen %v, want %v", got, want)
	}
	reportsMu.Lock()
	if !reflect.DeepEqual(reports, []string{"reported"}) {
		t.Fatalf("reports %v, want [reported]", reports)
	}
	reportsMu.Unlock()
	// The step after the phase ends the invocation (upstream awaits one flush before it looks).
	rt := <-captured
	endedBy := func(what string, call func() error) {
		t.Helper()
		var err error
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if err = call(); err != nil {
				break
			}
		}
		if err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("%s after the end = %v", what, err)
		}
	}
	endedBy("snapshot", func() error { _, err := rt.SnapshotErased(t.Context(), notes, root.Id()); return err })
	endedBy("context", func() error { _, err := rt.Context(t.Context(), root.Id(), nil); return err })
}

// The ContextView of a conversation with entries: contributions and messages are ai.Message values, decoded by role (the plain
// json.Unmarshal into the interface type failed for any conversation that had a message).
func TestCoreConversationContextDecodesItsMessages(t *testing.T) {
	_, root := openTasksHarness(t)
	if _, err := root.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		_, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}})
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	view, err := root.Context(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 1 || len(view.Contributions) != 1 || len(view.Contributions[0]) != 1 || len(view.Messages) != 1 {
		t.Fatalf("view = %+v", view)
	}
	if user, ok := view.Messages[0].(ai.UserMessage); !ok || !reflect.DeepEqual(user.Content, ai.UserText("hello")) {
		t.Fatalf("message = %#v", view.Messages[0])
	}
}

type taskPage = durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor]

// #10546, harness-tasks.test.ts:844 "pages the newest tasks first without scanning older ones".
func TestCoreTaskScanPagesTheNewestFirst(t *testing.T) {
	idle := oneStepTask("test.idle", func(context.Context, stepRuntime) error { return nil })
	h, root := openTasksHarness(t, idle.Erased())
	ids := make([]durable.TaskId, 5)
	for index := range ids {
		ids[index] = startTask(t, root, idle.Erased())
	}
	scan := func(query durable.TaskQuery, cursor durable.Cursor) taskPage {
		t.Helper()
		page, err := h.Commit(t.Context(), func(tx durable.Tx) (any, error) { return tx.ScanTasks(query, 2, cursor) })
		if err != nil {
			t.Fatal(err)
		}
		return page.(taskPage)
	}
	taskIds := func(page taskPage) []durable.TaskId {
		var found []durable.TaskId
		for _, record := range page.Items {
			found = append(found, record.Id)
		}
		return found
	}
	first := scan(durable.TaskQuery{Order: new(durable.ScanDescending)}, nil)
	if got, want := taskIds(first), []durable.TaskId{ids[4], ids[3]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first page %v, want %v", got, want)
	}
	// The cursor carries the order: the next page needs no query order.
	second := scan(durable.TaskQuery{}, *first.Next)
	if got, want := taskIds(second), []durable.TaskId{ids[2], ids[1]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page %v, want %v", got, want)
	}
}

// harness-tasks.test.ts:806 "waits for Harness and conversation idleness, counting blocked work and ignoring background tasks".
func TestCoreWaitForIdleCountsBlockedWorkAndIgnoresBackgroundTasks(t *testing.T) {
	var gatesMu sync.Mutex
	gates := map[durable.TaskId]chan struct{}{}
	gated := defineSteps("test.gated", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{"start": func(ctx context.Context, task stepRecord, rt stepRuntime) error {
		release := make(chan struct{})
		gatesMu.Lock()
		gates[task.Id] = release
		gatesMu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
	}})
	release := func(id durable.TaskId) {
		gatesMu.Lock()
		defer gatesMu.Unlock()
		close(gates[id])
	}
	h, root := openTasksHarness(t, gated.Erased())
	if err := h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	other, err := h.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
	if err != nil {
		t.Fatal(err)
	}
	start := func(conversation harness.Conversation, background bool) durable.TaskId {
		id, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (durable.TaskId, error) {
			return tx.CreateTaskErased(gated.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, Background: background})
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	foreground, background, elsewhere := start(root, false), start(root, true), start(other, false)
	// Work that has not started yet is live; cancelling a wait only rejects that wait.
	stopWaiting, cancelWait := context.WithCancelCause(t.Context())
	cancelledWait := make(chan error, 1)
	go func() { cancelledWait <- h.WaitForIdle(stopWaiting) }()
	time.Sleep(20 * time.Millisecond)
	cancelWait(errors.New("stop waiting"))
	waitErr := func(ch chan error) error {
		t.Helper()
		select {
		case err := <-ch:
			return err
		case <-time.After(15 * time.Second):
			t.Fatal("no result")
			return nil
		}
	}
	if err := waitErr(cancelledWait); err == nil || !strings.Contains(err.Error(), "stop waiting") {
		t.Fatalf("a cancelled wait = %v", err)
	}
	already, cancelAlready := context.WithCancelCause(t.Context())
	cancelAlready(errors.New("already cancelled"))
	if err := root.WaitForIdle(already); err == nil || !strings.Contains(err.Error(), "already cancelled") {
		t.Fatalf("an already cancelled wait = %v", err)
	}
	h.Resume()
	deadline := time.Now().Add(15 * time.Second)
	for {
		gatesMu.Lock()
		n := len(gates)
		gatesMu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of 3 tasks started", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	rootIdle := make(chan error, 1)
	go func() { rootIdle <- root.WaitForIdle(t.Context()) }()
	harnessIdle := make(chan error, 1)
	go func() { harnessIdle <- h.WaitForIdle(t.Context()) }()
	release(foreground)
	if err := waitErr(rootIdle); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	select {
	case <-harnessIdle:
		t.Fatal("Harness idle while another conversation has a live task")
	default:
	}
	release(elsewhere)
	if err := waitErr(harnessIdle); err != nil {
		t.Fatal(err)
	}
	if record, err := h.GetTask(t.Context(), background); err != nil || record.State.Status != durable.TaskRunning {
		t.Fatalf("background task %+v %v, want running", record, err)
	}
	release(background)
	waitSettled(t, h, background)
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.WaitForIdle(t.Context()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Harness.WaitForIdle after close = %v", err)
	}
	if err := root.WaitForIdle(t.Context()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Conversation.WaitForIdle after close = %v", err)
	}
}

// harness-tasks.test.ts:1210 "seals admission before signalling handlers and stops watches before joining them": a handler that
// uses a context close does not cancel waits for its watch to close; the signal's listener commits during the close and is refused.
func TestCoreCloseSealsAdmissionBeforeSignallingHandlersAndStopsWatchesBeforeJoiningThem(t *testing.T) {
	notes := defineDoc("test.close-notes", 1, func() obj { return delta.JsonObjectOf("text", "") })
	reached := make(chan struct{})
	fromListener := make(chan error, 1)
	var harnessRef atomic.Pointer[harness.Harness]
	var mu sync.Mutex
	var watchEnd durable.WatchEnd
	stubborn := oneStepTask("test.stubborn", func(ctx context.Context, rt stepRuntime) error {
		// Uses a context close does not cancel, then waits for the watch to close.
		watch, err := rt.WatchDocErased(context.Background(), notes)
		if err != nil {
			return err
		}
		signal := rt.Signal()
		go func() {
			<-signal.Done()
			_, commitErr := (*harnessRef.Load()).Commit(context.Background(), func(durable.Tx) (any, error) { return nil, nil })
			fromListener <- commitErr
		}()
		close(reached)
		<-watch.Closed()
		mu.Lock()
		watchEnd = watch.End()
		mu.Unlock()
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
	})
	h, root := openTasksHarness(t, stubborn.Erased())
	harnessRef.Store(&h)
	if _, err := h.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		o, err := tx.Doc(notes)
		if err != nil {
			return nil, err
		}
		return nil, o.Set("text", "x")
	}); err != nil {
		t.Fatal(err)
	}
	startTask(t, root, stubborn.Erased())
	h.Resume()
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the handler never reached its watch")
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fromListener:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("a commit from the signal's listener = %v, want closed", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the signal's listener never committed")
	}
	mu.Lock()
	defer mu.Unlock()
	if watchEnd.Reason != durable.WatchSessionClosed {
		t.Fatalf("watch ended %+v, want session_closed", watchEnd)
	}
}
