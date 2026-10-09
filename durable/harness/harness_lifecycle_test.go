// Ports packages/durable/test/harness-lifecycle.test.ts.

package harness

// pi: packages/durable/src/harness/view.ts

// pi: packages/durable/src/harness/task-graph.ts

// pi: packages/durable/src/harness/harness.ts

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// oneStep is a one-phase task running run and completing with null.
func oneStep(name string, run ...func(ctx context.Context) error) stepTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    name,
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				if len(run) > 0 {
					if err := run[0](ctx); err != nil {
						return err
					}
				}
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(context.Context, stepRecord, stepRuntime) error { return nil },
	})
}

func start(t *testing.T, conversation Conversation, task stepTask, background ...bool) durable.TaskId {
	t.Helper()
	options := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, Background: len(background) > 0 && background[0]}
	id, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, task, nil, options)
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// unloadDocuments drops the loaded trackers, so the next acquisition reads Storage on the line.
func unloadDocuments(t *testing.T, harness Harness) {
	t.Helper()
	if err := harness.(*harnessImpl).UnloadDocuments(); err != nil {
		t.Fatal(err)
	}
}

// storageOpen reports whether a raw Storage read still succeeds, as code of a joined invocation relies on.
func storageOpen(store durable.Storage) bool {
	_, err := store.Task(testContext, 1)
	return err == nil
}

// seedRunningTask commits a conversation with a running task, as a crash leaves it, so open has a reconciliation commit to fail.
func seedRunningTask(t *testing.T, store durable.Storage) {
	t.Helper()
	conversationId, err := store.MintId()
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.MintId()
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := durable.JsonValue(map[string]any{"phase": "run"})
	task := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
		Id:             durable.TaskId(id),
		ConversationId: durable.ConversationId(conversationId),
		Kind:           "test.seeded",
		Version:        1,
		State:          durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &checkpoint},
	}
	if _, err := store.Commit(testContext, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ConversationId(conversationId)}},
		durable.TaskWrite{Value: task},
	}); err != nil {
		t.Fatal(err)
	}
}

func isClosedError(err error) bool { return err != nil && strings.Contains(err.Error(), "closed") }

func TestHarnessOpen(t *testing.T) {
	t.Run("closes without a cancelled caller context and rethrows the original error when open fails", func(t *testing.T) {
		store := newControlledStorage()
		seedRunningTask(t, store)
		reader := countingReader(CreateRegistry())
		held := store.holdCommits()
		store.failNextCommit(errors.New("disk full"))
		ctx, cancel := context.WithCancelCause(testContext)
		opening := async(func() (Harness, error) {
			return OpenHarness(ctx, store, HarnessOptions{Models: ai.CreateModels(), Registry: reader})
		})
		_ = held.entered.wait(testContext)
		cancel(errors.New("caller gave up"))
		held.release()
		_, err := opening.wait()
		expectErrorContains(t, err, "disk full")
		if reader.subscriptions() != 0 {
			t.Fatalf("subscriptions %d", reader.subscriptions())
		}
		if storageOpen(store) {
			t.Fatal("storage is still open")
		}
	})

	t.Run("reports a failing close and still rethrows the open error", func(t *testing.T) {
		store := newControlledStorage()
		store.closeFailure = errors.New("close failed")
		seedRunningTask(t, store)
		store.failNextCommit(errors.New("disk full"))
		reports := &reportLog{}
		_, err := OpenHarness(testContext, store, HarnessOptions{Models: ai.CreateModels(), Registry: CreateRegistry(), OnReport: reports.add})
		expectErrorContains(t, err, "disk full")
		got := reports.all()
		if len(got) != 1 || got[0].Error() != "close failed" {
			t.Fatalf("reports %v", got)
		}
	})
}

// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: zeroing the results of Conversation.Reset fails it
// packages/durable/src/types.ts:907: Session.subscribeClose(listener) runs the listener when the session closes and returns its unsubscribe.
func TestHarnessClose(t *testing.T) {
	t.Run("joins a task handler that ignores its signal before closing Storage", func(t *testing.T) {
		store := storage.NewMemoryStorage()
		reached, gate := deferred(), deferred()
		var readAfterRelease *bool
		stubborn := oneStep("test.close-stubborn", func(context.Context) error {
			reached.resolve()
			_ = gate.wait(context.Background())
			readAfterRelease = new(storageOpen(store))
			return nil
		})
		harness, _, _ := openTasks(t, store, []durable.AnyTask{stubborn})
		start(t, mustRoot(t, harness, nil), stubborn)
		harness.Resume()
		_ = reached.wait(testContext)
		closing := asyncErr(func() error { return harness.Close(testContext) })
		if settled(closing.done) {
			t.Fatal("close did not wait for the handler")
		}
		if !storageOpen(store) {
			t.Fatal("storage closed before the handler returned")
		}
		gate.resolve()
		if _, err := closing.wait(); err != nil {
			t.Fatal(err)
		}
		if readAfterRelease == nil || !*readAfterRelease {
			t.Fatal("the handler could not read storage after release")
		}
		if storageOpen(store) {
			t.Fatal("storage is still open")
		}
	})

	t.Run("joins a tool execute and a hook that ignore their signal before closing Storage", func(t *testing.T) {
		for _, where := range []string{"tool", "hook"} {
			setup := chatSetup(t)
			store := storage.NewMemoryStorage()
			reached, gate := deferred(), deferred()
			var readAfterRelease *bool
			stubborn := func() {
				reached.resolve()
				_ = gate.wait(context.Background())
				readAfterRelease = new(storageOpen(store))
			}
			addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
				ToolSchema: ai.ToolSchema{Name: "wait", Description: "wait", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
				Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					if where == "tool" {
						stubborn()
					}
					return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
				},
			}))
			addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{
				BeforeRequest: func(context.Context, GenerationRequest, HookApi) (*GenerationRequest, error) {
					if where == "hook" {
						stubborn()
					}
					return nil, nil
				},
			})
			setup.Faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("wait", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			harness, root := openChat(t, store, setup)
			if _, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")}); err != nil {
				t.Fatal(err)
			}
			_ = reached.wait(testContext)
			closing := asyncErr(func() error { return harness.Close(testContext) })
			if settled(closing.done) {
				t.Fatalf("%s: close did not wait", where)
			}
			if !storageOpen(store) {
				t.Fatalf("%s: storage closed early", where)
			}
			gate.resolve()
			if _, err := closing.wait(); err != nil {
				t.Fatal(err)
			}
			if readAfterRelease == nil || !*readAfterRelease || storageOpen(store) {
				t.Fatalf("%s: storage lifetime", where)
			}
		}
	})

	t.Run("lets a new Harness open the same Storage once close resolved: no old invocation code runs", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		var mu sync.Mutex
		var log []string
		push := func(line string) {
			mu.Lock()
			defer mu.Unlock()
			log = append(log, line)
		}
		snapshot := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), log...)
		}
		gate := deferred()
		generation := 1
		stubborn := oneStep("test.generations", func(context.Context) error {
			mu.Lock()
			mine := generation
			mu.Unlock()
			push(fmt.Sprintf("start %d", mine))
			if mine == 1 {
				_ = gate.wait(context.Background())
			}
			push(fmt.Sprintf("end %d", mine))
			return nil
		})
		openSqlite := func() durable.Storage {
			store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return store
		}
		first, _, _ := openTasks(t, openSqlite(), []durable.AnyTask{stubborn})
		id := start(t, mustRoot(t, first, nil), stubborn)
		first.Resume()
		eventually(t, func() bool { return len(snapshot()) == 1 })
		closing := asyncErr(func() error {
			err := first.Close(testContext)
			push("closed")
			return err
		})
		if settled(closing.done) {
			t.Fatal("close did not wait")
		}
		gate.resolve()
		if _, err := closing.wait(); err != nil {
			t.Fatal(err)
		}
		expectStrings(t, snapshot(), []string{"start 1", "end 1", "closed"})
		mu.Lock()
		generation = 2
		mu.Unlock()
		second, _, _ := openTasks(t, openSqlite(), []durable.AnyTask{stubborn})
		second.Resume()
		if _, err := second.WaitForTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		expectStrings(t, snapshot(), []string{"start 1", "end 1", "closed", "start 2", "end 2"})
		mustClose(t, second)
	})

	t.Run("keeps shutting down after a cancelled close, and a second close awaits the same shutdown", func(t *testing.T) {
		store := storage.NewMemoryStorage()
		reached, gate := deferred(), deferred()
		stubborn := oneStep("test.close-cancelled", func(context.Context) error {
			reached.resolve()
			_ = gate.wait(context.Background())
			return nil
		})
		harness, _, _ := openTasks(t, store, []durable.AnyTask{stubborn})
		start(t, mustRoot(t, harness, nil), stubborn)
		harness.Resume()
		_ = reached.wait(testContext)
		ctx, cancel := context.WithCancelCause(testContext)
		cancelled := asyncErr(func() error { return harness.Close(ctx) })
		cancel(errors.New("stop waiting"))
		_, err := cancelled.wait()
		expectErrorContains(t, err, "stop waiting")
		// Admission stays sealed and the invocation still holds Storage open.
		_, err = harness.Commit(testContext, func(durable.Tx) (any, error) { return nil, nil })
		expectErrorContains(t, err, "closed")
		if !storageOpen(store) {
			t.Fatal("storage closed early")
		}
		second := asyncErr(func() error { return harness.Close(testContext) })
		if settled(second.done) {
			t.Fatal("second close did not wait")
		}
		gate.resolve()
		if _, err := second.wait(); err != nil {
			t.Fatal(err)
		}
		if storageOpen(store) {
			t.Fatal("storage is still open")
		}
	})

	t.Run("settles durably a commit whose committer was cancelled while it was in Storage", func(t *testing.T) {
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, nil)
		root := mustRoot(t, harness, nil)
		held := store.holdCommits()
		ctx, cancel := context.WithCancelCause(testContext)
		committing := async(func() (durable.EntryId, error) {
			return durable.Commit(ctx, root, func(tx durable.Tx) (durable.EntryId, error) {
				entry, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
				return entry.Id, err
			})
		})
		_ = held.entered.wait(testContext)
		cancel(errors.New("committer gave up"))
		held.release()
		id, err := committing.wait()
		if err != nil {
			t.Fatal(err)
		}
		page, err := root.Entries(testContext, durable.EntryQuery{}, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		expectIds(t, entryIds(page.Items), []durable.EntryId{id})
		mustClose(t, harness)
	})

	t.Run("publishes no frame to states and watches from a commit that settles during close", func(t *testing.T) {
		notes := durable.DefineDoc(durable.DocDefinition[noteState]{
			CommonDocDefinition: durable.CommonDocDefinition[noteState]{Kind: "test.close-frames", Version: 1, Initial: func() noteState { return noteState{} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
		})
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, nil)
		root := mustRoot(t, harness, nil)
		if _, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
			note, err := durable.TxDoc[noteState](tx, notes)
			if err != nil {
				return nil, err
			}
			return nil, note.Set("text", "before")
		}); err != nil {
			t.Fatal(err)
		}
		docState, err := harness.DocumentStateErased(testContext, notes)
		if err != nil || docState == nil {
			t.Fatalf("doc state %v", err)
		}
		docWatch, err := harness.WatchDocErased(testContext, notes)
		if err != nil || docWatch == nil {
			t.Fatalf("doc watch %v", err)
		}
		viewState, err := root.ViewState(testContext)
		if err != nil {
			t.Fatal(err)
		}
		viewWatch, err := root.Watch(testContext)
		if err != nil {
			t.Fatal(err)
		}
		graphState, err := harness.TaskGraph(testContext)
		if err != nil {
			t.Fatal(err)
		}
		graphWatch, err := harness.WatchTaskGraph(testContext)
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var frames []string
		frame := func(name string) {
			mu.Lock()
			defer mu.Unlock()
			frames = append(frames, name)
		}
		subscribeUpdates(t, graphState, func() { frame("graphState") })
		graphWatch.Start(func(context.Context, TaskGraph, []durable.Op) error { frame("graphWatch"); return nil })
		subscribeUpdates(t, docState, func() { frame("docState") })
		subscribeUpdates(t, viewState, func() { frame("viewState") })
		docWatch.Start(func(context.Context, durable.JsonObject, []durable.Op) error { frame("docWatch"); return nil })
		viewWatch.Start(func(context.Context, ConversationView, []durable.Op) error { frame("viewWatch"); return nil })
		docValue := docState.Value()
		viewValue := viewState.Value()

		held := store.holdCommits()
		committing := asyncErr(func() error {
			_, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
				note, err := durable.TxDoc[noteState](tx, notes)
				if err != nil {
					return nil, err
				}
				if err := note.Set("text", "during close"); err != nil {
					return nil, err
				}
				if _, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"}); err != nil {
					return nil, err
				}
				rootId := root.Id()
				return durable.CreateTask(tx, oneStep("test.close-graph"), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &rootId})
			})
			return err
		})
		_ = held.entered.wait(testContext)
		// Upstream's close() begins synchronously; wait until it has, through the last close listener.
		closeBegan := deferred()
		harness.SubscribeClose(closeBegan.resolve)
		closing := asyncErr(func() error { return harness.Close(testContext) })
		_ = closeBegan.wait(testContext)
		held.release()
		if _, err := committing.wait(); err != nil {
			t.Fatal(err)
		}
		if _, err := closing.wait(); err != nil {
			t.Fatal(err)
		}
		harness.(*harnessImpl).WaitDeliveries()
		mu.Lock()
		if len(frames) != 0 {
			t.Fatalf("frames %v", frames)
		}
		mu.Unlock()
		if !reflect.DeepEqual(docState.Value(), docValue) || !reflect.DeepEqual(viewState.Value(), viewValue) {
			t.Fatal("a state advanced during close")
		}
		<-graphWatch.Closed()
		<-docWatch.Closed()
		<-viewWatch.Closed()
		for name, end := range map[string]durable.WatchEnd{"graph": graphWatch.End(), "doc": docWatch.End(), "view": viewWatch.End()} {
			if end.Reason != durable.WatchSessionClosed {
				t.Fatalf("%s watch ended %v", name, end.Reason)
			}
		}
		// The commit itself settled.
		last := store.commitAt(store.commitCount() - 1)
		if !slicesContainsFunc(writeTypes(last), "entry") {
			t.Fatalf("last commit %v", writeTypes(last))
		}
	})

	t.Run("leaves no subscription behind a watch acquisition cancelled on the line", func(t *testing.T) {
		notes := durable.DefineDoc(durable.DocDefinition[noteState]{
			CommonDocDefinition: durable.CommonDocDefinition[noteState]{Kind: "test.cancelled-watch", Version: 1, Initial: func() noteState { return noteState{} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
		})
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, nil)
		root := mustRoot(t, harness, nil)
		if _, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
			note, err := durable.TxDoc[noteState](tx, notes)
			if err != nil {
				return nil, err
			}
			return nil, note.Set("text", "x")
		}); err != nil {
			t.Fatal(err)
		}
		// Count commit subscriptions of document observers: upstream wraps harness.subscribeCommits; the Session counts them.
		impl := harness.(*harnessImpl)
		baseline := impl.CommitSubscriptions()

		// Cancel a document watch while its acquisition loads the document on the line.
		unloadDocuments(t, harness)
		find := store.holdFindDocument()
		cancelled, cancel := cancelledContext(errors.New("cancelled"))
		docWatch := asyncErr(func() error {
			_, err := harness.WatchDocErased(cancelled, notes)
			return err
		})
		_ = find.entered.wait(testContext)
		cancel()
		find.release()
		_, err := docWatch.wait()
		expectError(t, err, "cancelled")
		if subscriptions := impl.CommitSubscriptions() - baseline; subscriptions != 0 {
			t.Fatalf("%d subscriptions left", subscriptions)
		}

		// Cancel a view watch while it builds its mount on the line.
		unloadDocuments(t, harness)
		find = store.holdFindDocument()
		cancelled, cancel = cancelledContext(errors.New("cancelled"))
		viewWatch := asyncErr(func() error {
			_, err := root.Watch(cancelled)
			return err
		})
		_ = find.entered.wait(testContext)
		cancel()
		find.release()
		_, err = viewWatch.wait()
		expectError(t, err, "cancelled")
		// No observer kept the mount: the next observer builds a new one, and the one after that another.
		first, err := root.ViewState(testContext)
		if err != nil {
			t.Fatal(err)
		}
		firstValue := first.Value()
		first.Dispose()
		second, err := root.ViewState(testContext)
		if err != nil {
			t.Fatal(err)
		}
		if sameEntries(second.Value().Entries, firstValue.Entries) && second.Value().Docs == firstValue.Docs {
			t.Fatal("a cancelled acquisition kept the mount")
		}
		second.Dispose()
		mustClose(t, harness)
	})

	t.Run("rejects conversation and Harness operations once close begins; inspect queued before reports closing", func(t *testing.T) {
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, nil)
		root := mustRoot(t, harness, nil)
		entry, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		})
		if err != nil {
			t.Fatal(err)
		}
		submission, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}})
		if err != nil {
			t.Fatal(err)
		}

		held := store.holdCommits()
		blocking := asyncErr(func() error {
			_, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "blocker"})
			})
			return err
		})
		_ = held.entered.wait(testContext)
		var queuedInspect *future[HarnessInspection]
		queueOnLineIn(t, "harnessImpl).Inspect", func() <-chan struct{} {
			queuedInspect = async(func() (HarnessInspection, error) { return harness.Inspect(testContext) })
			return queuedInspect.done
		})
		closeBegan := deferred()
		harness.SubscribeClose(closeBegan.resolve)
		closing := asyncErr(func() error { return harness.Close(testContext) })
		_ = closeBegan.wait(testContext)
		operations := []struct {
			name string
			run  func() error
		}{
			{"submit", func() error {
				_, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("x")})
				return err
			}},
			{"agent", func() error { _, err := root.Agent(testContext); return err }},
			{"configure", func() error {
				return root.Configure(testContext, AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("low")})
			}},
			{"commit", func() error {
				_, err := root.Commit(testContext, func(durable.Tx) (any, error) { return nil, nil })
				return err
			}},
			{"context", func() error { _, err := root.Context(testContext, nil); return err }},
			{"entries", func() error { _, err := root.Entries(testContext, durable.EntryQuery{}, 10, nil); return err }},
			{"fork", func() error {
				_, err := root.Fork(testContext, entry.Id, ConversationCreateOptions{Ownership: ownerless})
				return err
			}},
			{"compact", func() error { _, err := root.Compact(testContext, nil); return err }},
			{"reset", func() error { return root.Reset(testContext, nil) }},
			{"abort", func() error { return root.Abort(testContext, nil) }},
			{"conversationIdle", func() error { return root.WaitForIdle(testContext) }},
			{"viewState", func() error { _, err := root.ViewState(testContext); return err }},
			{"watch", func() error { _, err := root.Watch(testContext); return err }},
			{"taskGraph", func() error { _, err := harness.TaskGraph(testContext); return err }},
			{"watchTaskGraph", func() error { _, err := harness.WatchTaskGraph(testContext); return err }},
			{"status", func() error { _, err := submission.Status(testContext); return err }},
			{"wait", func() error { _, err := submission.Wait(testContext); return err }},
			{"abortSubmission", func() error { _, err := submission.Abort(testContext); return err }},
			{"root", func() error { _, err := harness.Root(testContext, nil); return err }},
			{"conversation", func() error { _, err := harness.Conversation(testContext, root.Id()); return err }},
			{"createConversation", func() error {
				_, err := harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
				return err
			}},
			{"getTask", func() error { _, err := harness.GetTask(testContext, 1); return err }},
			{"inspect", func() error { _, err := harness.Inspect(testContext); return err }},
			{"submission", func() error { _, err := harness.Submission(testContext, submission.Id()); return err }},
			{"abortTask", func() error { _, err := harness.AbortTask(testContext, 1); return err }},
			{"waitForTask", func() error { _, err := harness.WaitForTask(testContext, 1); return err }},
			{"harnessIdle", func() error { return harness.WaitForIdle(testContext) }},
			{"usage", func() error { _, err := harness.Usage(testContext); return err }},
		}
		for _, operation := range operations {
			err := operation.run()
			if !isClosedError(err) {
				t.Errorf("%s: %v, want closed", operation.name, err)
			}
		}
		func() {
			defer func() {
				if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "closed") {
					t.Errorf("resume after close: %v", recovered)
				}
			}()
			harness.Resume()
		}()
		held.release()
		if _, err := blocking.wait(); err != nil {
			t.Fatal(err)
		}
		inspection, err := queuedInspect.wait()
		if err != nil {
			t.Fatal(err)
		}
		if inspection.Scheduling != "closing" {
			t.Fatalf("scheduling %s", inspection.Scheduling)
		}
		if _, err := closing.wait(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("completes reads queued at the seal and rejects queued waits and acquisitions that would follow commits", func(t *testing.T) {
		notes := durable.DefineDoc(durable.DocDefinition[noteState]{
			CommonDocDefinition: durable.CommonDocDefinition[noteState]{Kind: "test.queued-at-seal", Version: 1, Initial: func() noteState { return noteState{} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
		})
		absent := durable.DefineDoc(durable.DocDefinition[noteState]{
			CommonDocDefinition: durable.CommonDocDefinition[noteState]{Kind: "test.queued-absent", Version: 1, Initial: func() noteState { return noteState{} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
		})
		pending := oneStep("test.queued-pending")
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, nil)
		root := mustRoot(t, harness, nil)
		if _, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) { return tx.Doc(notes) }); err != nil {
			t.Fatal(err)
		}
		taskId := start(t, root, pending)
		write, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := write.Wait(testContext); err != nil {
			t.Fatal(err)
		}

		held := store.holdCommits()
		blocking := asyncErr(func() error {
			_, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "blocker"})
			})
			return err
		})
		_ = held.entered.wait(testContext)
		settle := func(run func() (bool, error)) *future[string] {
			return async(func() (string, error) {
				present, err := run()
				switch {
				case isClosedError(err):
					return "closed", nil
				case err != nil:
					return err.Error(), nil
				case !present:
					return "undefined", nil
				}
				return "resolved", nil
			})
		}
		// Queue each read in upstream's order: each one enters the line before the next is issued.
		names := []string{"context", "snapshot", "settledWait", "absentWatch", "absentState", "waitForTask", "documentState", "watchDoc", "viewState", "watch", "taskGraph"}
		runs := map[string]func() (bool, error){
			"context": func() (bool, error) { _, err := root.Context(testContext, nil); return true, err },
			"snapshot": func() (bool, error) {
				value, err := harness.SnapshotErased(testContext, notes)
				return value != nil, err
			},
			"settledWait": func() (bool, error) { _, err := write.Wait(testContext); return true, err },
			"absentWatch": func() (bool, error) {
				watch, err := harness.WatchDocErased(testContext, absent)
				return watch != nil, err
			},
			"absentState": func() (bool, error) {
				state, err := harness.DocumentStateErased(testContext, absent)
				return state != nil, err
			},
			"waitForTask": func() (bool, error) { _, err := harness.WaitForTask(testContext, taskId); return true, err },
			"documentState": func() (bool, error) {
				state, err := harness.DocumentStateErased(testContext, notes)
				return state != nil, err
			},
			"watchDoc": func() (bool, error) {
				watch, err := harness.WatchDocErased(testContext, notes)
				return watch != nil, err
			},
			"viewState": func() (bool, error) { _, err := root.ViewState(testContext); return true, err },
			"watch":     func() (bool, error) { _, err := root.Watch(testContext); return true, err },
			"taskGraph": func() (bool, error) { _, err := harness.TaskGraph(testContext); return true, err },
		}
		queued := map[string]*future[string]{}
		for _, name := range names {
			queueOnLine(t, harness, func() <-chan struct{} {
				queued[name] = settle(runs[name])
				return queued[name].done
			})
		}
		// Upstream's close() seals before release() runs; the Go Close runs on its own goroutine, so release only once
		// its close listeners ran.
		closeBegan := deferred()
		harness.SubscribeClose(closeBegan.resolve)
		closing := asyncErr(func() error { return harness.Close(testContext) })
		_ = closeBegan.wait(testContext)
		held.release()
		if _, err := blocking.wait(); err != nil {
			t.Fatal(err)
		}
		outcomes := map[string]string{}
		for name, outcome := range queued {
			outcomes[name], _ = outcome.wait()
		}
		want := map[string]string{
			"context":       "resolved",
			"snapshot":      "resolved",
			"settledWait":   "resolved",
			"absentWatch":   "undefined",
			"absentState":   "undefined",
			"waitForTask":   "closed",
			"documentState": "closed",
			"watchDoc":      "closed",
			"viewState":     "closed",
			"watch":         "closed",
			"taskGraph":     "closed",
		}
		if !reflect.DeepEqual(outcomes, want) {
			t.Fatalf("outcomes %v, want %v", outcomes, want)
		}
		if _, err := closing.wait(); err != nil {
			t.Fatal(err)
		}
	})
}

type pausedHarness struct {
	harness      Harness
	root         Conversation
	id           durable.TaskId
	submissionId durable.SubmissionId
	ran          func() bool
}

// paused opens a paused Harness with one background task pending and a queued write submission.
func paused(t *testing.T) pausedHarness {
	var ran sync.Once
	var didRun bool
	var mu sync.Mutex
	marker := oneStep("test.marker", func(context.Context) error {
		ran.Do(func() {
			mu.Lock()
			didRun = true
			mu.Unlock()
		})
		return nil
	})
	harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{marker})
	root := mustRoot(t, harness, nil)
	// Background, so idle waits and conversation abort leave it alone.
	id := start(t, root, marker, true)
	submissionId, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.SubmissionId, error) {
		record, err := tx.CreateSubmission(durable.SubmissionCreate{ConversationId: root.Id(), Type: durable.SubmissionTypeWrite, Status: durable.SubmissionQueued})
		return record.Id, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return pausedHarness{harness: harness, root: root, id: id, submissionId: submissionId, ran: func() bool {
		mu.Lock()
		defer mu.Unlock()
		return didRun
	}}
}

func TestSchedulingOnAPausedHarness(t *testing.T) {
	t.Run("never schedules from a read-only viewer", func(t *testing.T) {
		opened := paused(t)
		harness, root := opened.harness, opened.root
		notes := durable.DefineDoc(durable.DocDefinition[noteState]{
			CommonDocDefinition: durable.CommonDocDefinition[noteState]{Kind: "test.viewer-notes", Version: 1, Initial: func() noteState { return noteState{} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
		})
		check := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) { return tx.Doc(notes) })
		check(err)
		_, err = harness.Inspect(testContext)
		check(err)
		_, err = harness.GetTask(testContext, opened.id)
		check(err)
		_, err = harness.Usage(testContext)
		check(err)
		_, err = harness.Conversation(testContext, root.Id())
		check(err)
		_, err = harness.SnapshotErased(testContext, notes)
		check(err)
		docState, err := harness.DocumentStateErased(testContext, notes)
		check(err)
		docState.Dispose()
		docWatch, err := harness.WatchDocErased(testContext, notes)
		check(err)
		_, err = docWatch.Stop()
		check(err)
		submission, err := harness.Submission(testContext, opened.submissionId)
		check(err)
		_, err = submission.Status(testContext)
		check(err)
		_, err = root.Agent(testContext)
		check(err)
		_, err = root.Context(testContext, nil)
		check(err)
		_, err = root.Entries(testContext, durable.EntryQuery{}, 10, nil)
		check(err)
		viewState, err := root.ViewState(testContext)
		check(err)
		viewState.Dispose()
		viewWatch, err := root.Watch(testContext)
		check(err)
		_, err = viewWatch.Stop()
		check(err)
		graphState, err := harness.TaskGraph(testContext)
		check(err)
		graphState.Dispose()
		graphWatch, err := harness.WatchTaskGraph(testContext)
		check(err)
		_, err = graphWatch.Stop()
		check(err)
		flush()
		flush()
		if opened.ran() {
			t.Fatal("a viewer enabled scheduling")
		}
		if inspectOf(t, harness).Scheduling != "paused" {
			t.Fatal("scheduling is not paused")
		}
		mustClose(t, harness)
	})

	t.Run("schedules from every progress call", func(t *testing.T) {
		calls := []struct {
			name string
			call func(opened pausedHarness) error
		}{
			{"submit", func(opened pausedHarness) error {
				_, err := opened.root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}})
				return err
			}},
			{"compact", func(opened pausedHarness) error { _, err := opened.root.Compact(testContext, nil); return err }},
			{"abort", func(opened pausedHarness) error { return opened.root.Abort(testContext, nil) }},
			{"conversationIdle", func(opened pausedHarness) error { return opened.root.WaitForIdle(testContext) }},
			{"submissionWait", func(opened pausedHarness) error {
				submission, err := opened.harness.Submission(testContext, opened.submissionId)
				if err != nil {
					return err
				}
				_, err = submission.Wait(testContext)
				return err
			}},
			{"waitForTask", func(opened pausedHarness) error {
				_, err := opened.harness.WaitForTask(testContext, opened.id)
				return err
			}},
			{"harnessIdle", func(opened pausedHarness) error { return opened.harness.WaitForIdle(testContext) }},
		}
		for _, call := range calls {
			opened := paused(t)
			// A queued write's wait settles only on placement; close rejects it.
			pending := asyncErr(func() error { return call.call(opened) })
			reached := false
			for range 200 {
				if opened.ran() {
					reached = true
					break
				}
				flush()
			}
			if !reached {
				t.Fatalf("%s did not enable scheduling", call.name)
			}
			mustClose(t, opened.harness)
			_, _ = pending.wait()
		}
	})
}

func TestRegistryChangesBeforeResume(t *testing.T) {
	t.Run("runs what the registry holds at resume: a definition installed or replaced after open", func(t *testing.T) {
		var mu sync.Mutex
		var log []string
		define := func(label string) stepTask {
			return oneStep("test.late-definition", func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				log = append(log, label)
				return nil
			})
		}
		store := storage.NewMemoryStorage()
		harness, registry, _ := openTasks(t, store, nil)
		root := mustRoot(t, harness, nil)
		missing := start(t, root, define("unused"))
		if tasks := inspectOf(t, harness).Tasks; len(tasks) != 1 || tasks[0].State.Kind != TaskInspectionBlocked || tasks[0].State.Reason != "missing_task" {
			t.Fatalf("tasks %+v", tasks)
		}
		installed := addTask(t, registry, define("v1"))
		if tasks := inspectOf(t, harness).Tasks; len(tasks) != 1 || tasks[0].State.Kind != TaskInspectionReady {
			t.Fatalf("tasks %+v", tasks)
		}
		// The same extension name replaces the definition in place, still before resume.
		installed.dispose()
		addTask(t, registry, define("v2"))
		harness.Resume()
		if _, err := harness.WaitForTask(testContext, missing); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		expectStrings(t, log, []string{"v2"})
		mu.Unlock()
		mustClose(t, harness)
	})
}

func slicesContainsFunc(values []string, want string) bool {
	return slices.Contains(values, want)
}

// subscribeUpdates calls onUpdate for every update delivery of state.
func subscribeUpdates[T any](t *testing.T, state durable.AttachedReplicatedState[T], onUpdate func()) {
	t.Helper()
	if _, err := state.Subscribe(func(_ T, _ context.Context, delivery chord.ReplicatedStateDelivery) {
		if delivery.Kind == chord.DeliveryUpdate {
			onUpdate()
		}
	}); err != nil {
		t.Fatal(err)
	}
}

var _ = session.CreateSession
