// Ports the changed cases of packages/durable/test/harness-tasks.test.ts at Pi 6d4b7443 (group commit e5c6182b) that
// harness_tasks_test.go and harness_tasks_scheduling_test.go do not carry: the case that counts document scans and the
// task documents the Session indexes, the two "task close" cases whose blocker is a job holding the Session line, and
// the "publication at adoption" cases whose expectation changed (the publication is at the Session's host batch, not at
// the storage's adopt). Each supersedes the old Go case named in its comment.

package harness

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
)

// documentScanCountingStorage is the MemoryStorage of the "commits results with entries atomically" case that counts
// the scanDocuments calls (CountingStorage of harness-tasks.test.ts).
type documentScanCountingStorage struct {
	*storage.MemoryStorage
	documentScans atomic.Int64
}

func (counting *documentScanCountingStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	counting.documentScans.Add(1)
	return counting.MemoryStorage.ScanDocuments(ctx, query, limit, cursor)
}

// holdLine holds the Session line with a commit whose callback waits: the jobs queued after it start once it is
// released (holdLine of harness-tasks.test.ts). A commit that fails fails the test and rejects entered, so what waits for the
// hold fails instead of hanging; the callback also ends with the test.
func holdLine(t testing.TB, harness Harness) (entered *deferredGate, release func()) {
	t.Helper()
	entered, released := deferred(), deferred()
	go func() {
		_, err := harness.Commit(testContext, func(durable.Tx) (any, error) {
			entered.resolve()
			return nil, released.wait(t.Context())
		})
		if err != nil && t.Context().Err() == nil {
			t.Errorf("the commit holding the line failed: %v", err)
			entered.reject(err)
		}
	}()
	return entered, released.resolve
}

// harness-tasks.test.ts "task phases".
func TestTaskPhasesGroupCommit(t *testing.T) {
	// Supersedes harness_tasks_test.go: "commits results with entries atomically, keeps memos until terminal, and retires task documents".
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
		store := &documentScanCountingStorage{MemoryStorage: storage.NewMemoryStorage()}
		opened := tkOpenRoot(t, []durable.AnyTask{writer.Erased(), child.Erased()}, tkOptions{storage: store})
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
		// The created child is a task of its own: nothing orders its end before the end of the task that created it, so its
		// entry is still indexed, and its terminal commit may still scan, until the child itself is terminal.
		childReceipt, err := opened.harness.WaitForTask(testContext, durable.TaskId(childId.Load()))
		if err != nil || childReceipt.ConversationId != conversation.Id() {
			t.Fatalf("child receipt %+v %v, want conversation %d", childReceipt, err, conversation.Id())
		}
		// The Session indexed the documents of the tasks it created, so no terminal commit scanned for them, and it dropped
		// each task's entry at its end.
		if scans := store.documentScans.Load(); scans != 0 {
			t.Fatalf("document scans = %d, want 0", scans)
		}
		if sizes := opened.harness.(*harnessImpl).DocumentCacheSizes(); sizes.TaskDocuments != 0 {
			t.Fatalf("task documents indexed = %d, want 0", sizes.TaskDocuments)
		}
		if stored, err := opened.harness.GetTask(testContext, id); err != nil || !reflect.DeepEqual(*stored, receipt) {
			t.Fatalf("stored task %+v %v differs from receipt %+v", stored, err, receipt)
		}
		if opened.root.Id() == conversation.Id() {
			t.Fatal("root and created conversation share an ID")
		}
		mustClose(t, opened.harness)
	})
}

// harness-tasks.test.ts "task close".
func TestTaskCloseGroupCommit(t *testing.T) {
	type heldLine struct {
		entered *deferredGate
		release func()
	}

	// Supersedes harness_tasks_scheduling_test.go: "writes no fault when close seals while the step after a failed phase is queued"
	// (the blocker is a job that holds the Session line, not a held storage commit).
	t.Run("writes no fault when close seals while the step after a failed phase is queued", func(t *testing.T) {
		store := newControlledStorage()
		held := &syncValue[heldLine]{}
		var harnessRef atomic.Pointer[Harness]
		throws := tkOneStep("test.close-fault", func(context.Context, stepRecord, stepRuntime) error {
			// The step after this phase queues behind a job that holds the line.
			entered, release := holdLine(t, *harnessRef.Load())
			held.put(heldLine{entered, release})
			if err := entered.wait(testContext); err != nil {
				return err
			}
			return errors.New("would fault")
		})
		opened := tkOpenRoot(t, []durable.AnyTask{throws.Erased()}, tkOptions{storage: store})
		harnessRef.Store(&opened.harness)
		id := tkStart(t, opened.root, throws.Erased())
		opened.harness.Resume()
		eventually(t, held.isSet)
		line, _ := held.get()
		if err := line.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		awaitQuiescent(t)
		closing := tkCloseSealed(t, opened.harness)
		line.release()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		// Created, reserved, and written back as pending by close: no fault.
		if got := tkTaskWrites(store, id); !reflect.DeepEqual(got, []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskPending}) {
			t.Fatalf("task writes %v, want [pending running pending]", got)
		}
	})

	// Supersedes harness_tasks_scheduling_test.go: "rejects a runtime commit that was queued on the line when close sealed it".
	t.Run("rejects a runtime commit that was queued on the line when close sealed it", func(t *testing.T) {
		store := newControlledStorage()
		queued := make(chan error, 1)
		held := &syncValue[heldLine]{}
		var harnessRef atomic.Pointer[Harness]
		proceed, admitted := deferred(), deferred()
		queuedTask := tkOneStep("test.close-queued-commit", func(_ context.Context, _ stepRecord, runtime stepRuntime) error {
			entered, release := holdLine(t, *harnessRef.Load())
			held.put(heldLine{entered, release})
			if err := entered.wait(testContext); err != nil {
				return err
			}
			go func() {
				queued <- runtime.Commit(lineadmission.With(testContext, admitted.resolve), func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](nil), nil
				})
			}()
			// Ignores the close signal, so the invocation is still alive when the queued commit reaches the line.
			return proceed.wait(testContext)
		})
		opened := tkOpenRoot(t, []durable.AnyTask{queuedTask.Erased()}, tkOptions{storage: store})
		harnessRef.Store(&opened.harness)
		id := tkStart(t, opened.root, queuedTask.Erased())
		opened.harness.Resume()
		eventually(t, held.isSet)
		line, _ := held.get()
		// The commit must be queued on the line behind the held job before close seals it, as upstream's
		// runtime.commit() enters the line synchronously: the queue reports the moment it fixed the commit's place.
		if err := admitted.wait(testContext); err != nil {
			t.Fatal(err)
		}
		closing := tkCloseSealed(t, opened.harness)
		line.release()
		tkContainsError(t, tkWaitErr(t, queued), "Harness is closed")
		proceed.resolve()
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		// Created, reserved, and written back as pending by close; the queued outcome never landed.
		if got := tkTaskWrites(store, id); !reflect.DeepEqual(got, []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskPending}) {
			t.Fatalf("task writes %v, want [pending running pending]", got)
		}
	})
}

// harness-tasks.test.ts "publication at adoption".
func TestPublicationAtAdoptionGroupCommit(t *testing.T) {
	// Supersedes harness_tasks_test.go: "resolves a waitForIdle() called after adoption, before the storage resolved, after host listeners".
	t.Run("resolves a waitForIdle() called while the storage stores the commit at its publication, after host listeners", func(t *testing.T) {
		store := newLateResolvingStorage()
		gate := deferred()
		gated := tkGated("test.gap-idle", gate)
		opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()}, tkOptions{storage: store})
		var order syncList[string]
		id := tkStart(t, opened.root, gated.Erased())
		observeStored(opened.harness, func(_ context.Context, publication durable.CommitPublication) {
			if publicationTerminal(publication, id) {
				order.add("host")
			}
		})
		gapped := deferred()
		idle := make(chan struct{})
		store.setGap(func(writes []durable.StorageWrite) bool {
			if !writesEndTask(writes) {
				return false
			}
			go func() {
				_ = opened.harness.WaitForIdle(testContext)
				order.add(fmt.Sprintf("idle committing=%v", store.committing.Load()))
				close(idle)
			}()
			gapped.resolve()
			return true
		})
		opened.harness.Resume()
		gate.resolve()
		_ = gapped.wait(testContext)
		<-idle
		if got := order.all(); !reflect.DeepEqual(got, []string{"host", "idle committing=false"}) {
			t.Fatalf("order %v", got)
		}
		mustClose(t, opened.harness)
	})

	// Supersedes harness_tasks_test.go: "keeps a task wait resolved when the Harness closes after adoption, before the storage resolved".
	t.Run("ends a task wait when the Harness closes while the terminal commit is stored, and still stores it", func(t *testing.T) {
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
		waited := make(chan error, 1)
		callAsync(func() {
			_, err := opened.harness.WaitForTask(testContext, id)
			waited <- err
		})
		gate.resolve()
		_ = gapped.wait(testContext)
		<-closing
		// Close ends the waits before it drains the commit in flight, which is published and stored all the same.
		tkContainsError(t, <-waited, "closed")
		<-opened.harness.Closed()
		if end := opened.harness.End(); end.Kind != durable.SessionEndClosed {
			t.Fatalf("end %+v", end)
		}
		store.Reopen()
		reopened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()}, tkOptions{storage: store})
		record, err := reopened.harness.GetTask(testContext, id)
		if err != nil || record == nil || record.State.Status != durable.TaskTerminal {
			t.Fatalf("stored task %+v %v, want terminal", record, err)
		}
		mustClose(t, reopened.harness)
	})

	// Supersedes harness_tasks_test.go: "calls a host listener added on a Harness between adopt and the dispatch only when one was registered at adopt".
	t.Run("calls a host listener added on a Harness during a publication only when one was registered at it", func(t *testing.T) {
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
		var mu sync.Mutex
		stopLate := durable.CommitObservation(nil)
		// Runs once, during the next publication, after its dispatch was queued or not.
		var during func()
		followCommits(opened.harness, func(context.Context, session.ObservedCommit) {
			mu.Lock()
			run := during
			during = nil
			mu.Unlock()
			if run != nil {
				run()
			}
		})
		setDuring := func(run func()) {
			mu.Lock()
			defer mu.Unlock()
			during = run
		}
		// No host listener at the publication, although the Harness's own observers run: no dispatch, so the late one
		// misses it.
		setDuring(func() {
			observation := observeStored(opened.harness, func(_ context.Context, publication durable.CommitPublication) { late.add(publication.Seq) })
			mu.Lock()
			stopLate = observation
			mu.Unlock()
		})
		note()
		awaitQuiescent(t)
		if late.len() != 0 {
			t.Fatalf("late listener got %v", late.all())
		}
		note()
		if late.len() != 1 {
			t.Fatalf("late listener got %v", late.all())
		}
		mu.Lock()
		stopLate.Detach()
		mu.Unlock()
		// One registered at the publication: the dispatch reads the listeners when it runs, the late one included.
		observeStored(opened.harness, func(context.Context, durable.CommitPublication) {})
		var second syncList[durable.Seq]
		setDuring(func() {
			observeStored(opened.harness, func(_ context.Context, publication durable.CommitPublication) { second.add(publication.Seq) })
		})
		note()
		if second.len() != 1 {
			t.Fatalf("second late listener got %v", second.all())
		}
		mustClose(t, opened.harness)
	})
}
