// Ports packages/durable/test/harness-lifecycle.test.ts.

package harness

// pi: packages/durable/src/harness/view.ts

// pi: packages/durable/src/harness/task-graph.ts

// pi: packages/durable/src/harness/harness.ts

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
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
	if _, err := durabletest.CommitWrites(testContext, store, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ConversationId(conversationId)}},
		durable.TaskWrite{Value: task},
	}); err != nil {
		t.Fatal(err)
	}
}

func isClosedError(err error) bool { return err != nil && strings.Contains(err.Error(), "closed") }

// providerResources is providerResources() of harness-lifecycle.test.ts: the provider resources a faux request allocates for its
// session ID, and a pi-ai session resource cleanup hook that frees them and records what it freed.
type providerResources struct {
	mu         sync.Mutex
	live       map[string]struct{}
	cleaned    []string
	unregister func()
}

func newProviderResources(t *testing.T) *providerResources {
	t.Helper()
	resources := &providerResources{live: map[string]struct{}{}}
	resources.unregister = ai.RegisterSessionResourceCleanup(func(sessionId string) {
		resources.mu.Lock()
		defer resources.mu.Unlock()
		targets := []string{sessionId}
		if sessionId == "" {
			targets = slices.Sorted(maps.Keys(resources.live))
		}
		resources.cleaned = append(resources.cleaned, targets...)
		for _, target := range targets {
			delete(resources.live, target)
		}
	})
	t.Cleanup(resources.unregister)
	return resources
}

func (resources *providerResources) allocate(sessionId string) error {
	if sessionId == "" {
		return errors.New("Provider request has no session ID")
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.live[sessionId] = struct{}{}
	return nil
}

// answer is a faux step that allocates a resource for the request's session ID and answers text.
func (resources *providerResources) answer(text string) ai.FauxResponseStep {
	return ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		if err := resources.allocate(options.SessionID); err != nil {
			return ai.AssistantMessage{}, err
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}}.AssistantMessage(), nil
	})
}

// liveIds are the session IDs whose resources are allocated, sorted.
func (resources *providerResources) liveIds() []string {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return slices.Sorted(maps.Keys(resources.live))
}

// cleanedIds are the session IDs the cleanup hook freed, in order.
func (resources *providerResources) cleanedIds() []string {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return slices.Clone(resources.cleaned)
}

// sameIds reports whether got and want hold the same session IDs, in any order.
func sameIds(got, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}

func TestHarnessOpen(t *testing.T) {
	// harness-lifecycle.test.ts:85 it("closes without a cancelled caller context and rethrows the original error when open fails")
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

	// harness-lifecycle.test.ts:105 it("rethrows the open error and reports it once, though closing then fails too")
	t.Run("rethrows the open error and reports it once, though closing then fails too", func(t *testing.T) {
		store := newControlledStorage()
		store.closeFailure = errors.New("close failed")
		seedRunningTask(t, store)
		store.failNextCommit(errors.New("disk full"))
		reports := &reportLog{}
		_, err := OpenHarness(testContext, store, HarnessOptions{Models: ai.CreateModels(), Registry: CreateRegistry(), OnReport: reports.add})
		expectErrorContains(t, err, "disk full")
		got := reports.all()
		// The storage failure that failed open is the cause; the close that fails after it adds no report.
		if len(got) != 1 || got[0].Error() != "disk full" {
			t.Fatalf("reports %v", got)
		}
	})
}

// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: zeroing the results of Conversation.Reset fails it
// packages/durable/src/types.ts:907: Session.subscribeClose(listener) runs the listener when the session closes and returns its unsubscribe.
func TestHarnessClose(t *testing.T) {
	// harness-lifecycle.test.ts:128 it("joins a task handler that ignores its signal before closing Storage")
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
		harness, _, _ := openTasks(t, store, []durable.AnyTask{stubborn.Erased()})
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

	// harness-lifecycle.test.ts:151 it("joins a tool execute and a hook that ignore their signal before closing Storage")
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
				Name: "wait", Description: "wait", Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
				Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					if where == "tool" {
						stubborn()
					}
					return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
				},
			}))
			addHooks(t, setup.Registry, GenerationTask.Erased(), &GenerationHooks{
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

	// harness-lifecycle.test.ts:233 it("cleans up provider session resources after running tasks join"), regression coverage for #11676.
	t.Run("cleans up provider session resources after running tasks join", func(t *testing.T) {
		resources := newProviderResources(t)
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{resources.answer("ok")})
		reached, gate := deferred(), deferred()
		stubborn := oneStep("test.provider-cleanup-order", func(context.Context) error {
			reached.resolve()
			_ = gate.wait(context.Background())
			return nil
		})
		addTask(t, setup.Registry, stubborn.Erased())
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		sessionId := providerSessionId(t, harness, root.Id())
		if _, err := submitInput(t, root, "allocate").Wait(testContext); err != nil {
			t.Fatal(err)
		}
		start(t, root, stubborn)
		harness.Resume()
		_ = reached.wait(testContext)
		closing := asyncErr(func() error { return harness.Close(testContext) })
		if settled(closing.done) {
			t.Fatal("close did not wait for the running task")
		}
		if cleaned := resources.cleanedIds(); len(cleaned) != 0 {
			t.Fatalf("cleaned before the task joined: %v", cleaned)
		}
		if live := resources.liveIds(); !reflect.DeepEqual(live, []string{sessionId}) {
			t.Fatalf("live = %v, want [%s]", live, sessionId)
		}
		gate.resolve()
		if _, err := closing.wait(); err != nil {
			t.Fatal(err)
		}
		resources.unregister()
		if cleaned := resources.cleanedIds(); !reflect.DeepEqual(cleaned, []string{sessionId}) {
			t.Fatalf("cleaned = %v, want [%s]", cleaned, sessionId)
		}
		if live := resources.liveIds(); len(live) != 0 {
			t.Fatalf("live = %v", live)
		}
	})

	// harness-lifecycle.test.ts:262 it("cleans up provider session resources when the Harness fails")
	t.Run("cleans up provider session resources when the Harness fails", func(t *testing.T) {
		resources := newProviderResources(t)
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{resources.answer("ok")})
		store := newControlledStorage()
		harness, root := openChat(t, store, setup)
		sessionId := providerSessionId(t, harness, root.Id())
		if _, err := submitInput(t, root, "allocate").Wait(testContext); err != nil {
			t.Fatal(err)
		}
		failure := errors.New("disk gone")
		store.failNextCommit(failure)
		_, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note", Data: map[string]any{}})
		})
		if !errors.Is(err, failure) {
			t.Fatalf("commit error = %v, want the storage failure", err)
		}
		<-harness.Closed()
		resources.unregister()
		if end := harness.End(); end.Kind != durable.SessionEndFailed || !errors.Is(end.Error, failure) {
			t.Fatalf("end = %+v, want failed with the storage failure", end)
		}
		if cleaned := resources.cleanedIds(); !reflect.DeepEqual(cleaned, []string{sessionId}) {
			t.Fatalf("cleaned = %v, want [%s]", cleaned, sessionId)
		}
		if live := resources.liveIds(); len(live) != 0 {
			t.Fatalf("live = %v", live)
		}
	})

	// harness-lifecycle.test.ts:280 it("does not clean up provider session resources owned by another Harness")
	t.Run("does not clean up provider session resources owned by another Harness", func(t *testing.T) {
		resources := newProviderResources(t)
		firstSetup, secondSetup := chatSetup(t), chatSetup(t)
		firstSetup.Faux.SetResponses([]ai.FauxResponseStep{resources.answer("first root"), resources.answer("first child")})
		secondSetup.Faux.SetResponses([]ai.FauxResponseStep{resources.answer("second")})
		firstHarness, firstRoot := openChat(t, storage.NewMemoryStorage(), firstSetup)
		firstChild, err := firstHarness.CreateConversation(testContext, ConversationCreateOptions{
			Ownership: ownerless,
			Agent:     &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
		})
		if err != nil {
			t.Fatal(err)
		}
		secondHarness, secondRoot := openChat(t, storage.NewMemoryStorage(), secondSetup)
		firstId := providerSessionId(t, firstHarness, firstRoot.Id())
		firstChildId := providerSessionId(t, firstHarness, firstChild.Id())
		secondId := providerSessionId(t, secondHarness, secondRoot.Id())
		for _, send := range []struct {
			conversation Conversation
			text         string
		}{{firstRoot, "allocate first root"}, {firstChild, "allocate first child"}, {secondRoot, "allocate second"}} {
			if _, err := submitInput(t, send.conversation, send.text).Wait(testContext); err != nil {
				t.Fatal(err)
			}
		}
		if err := firstHarness.Close(testContext); err != nil {
			t.Fatal(err)
		}
		cleanedAfterFirst, liveAfterFirst := resources.cleanedIds(), resources.liveIds()
		if err := secondHarness.Close(testContext); err != nil {
			t.Fatal(err)
		}
		resources.unregister()
		if len(cleanedAfterFirst) != 2 || !sameIds(cleanedAfterFirst, []string{firstId, firstChildId}) {
			t.Fatalf("cleaned after the first Harness = %v, want %v", cleanedAfterFirst, []string{firstId, firstChildId})
		}
		if !reflect.DeepEqual(liveAfterFirst, []string{secondId}) {
			t.Fatalf("live after the first Harness = %v, want [%s]", liveAfterFirst, secondId)
		}
		if cleaned := resources.cleanedIds(); len(cleaned) != 3 || !sameIds(cleaned, []string{firstId, firstChildId, secondId}) {
			t.Fatalf("cleaned = %v, want all three", cleaned)
		}
		if live := resources.liveIds(); len(live) != 0 {
			t.Fatalf("live = %v", live)
		}
	})

	// harness-lifecycle.test.ts:198 it("lets a new Harness open the same Storage once close resolved: no old invocation code runs")
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
		openSqlite := func() durable.Storage { return openNodeSqliteStorage(t, path) }
		first, _, _ := openTasks(t, openSqlite(), []durable.AnyTask{stubborn.Erased()})
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
		second, _, _ := openTasks(t, openSqlite(), []durable.AnyTask{stubborn.Erased()})
		second.Resume()
		if _, err := second.WaitForTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		expectStrings(t, snapshot(), []string{"start 1", "end 1", "closed", "start 2", "end 2"})
		mustClose(t, second)
	})

	// harness-lifecycle.test.ts:231 it("keeps shutting down after a cancelled close, and a second close awaits the same shutdown")
	t.Run("keeps shutting down after a cancelled close, and a second close awaits the same shutdown", func(t *testing.T) {
		store := storage.NewMemoryStorage()
		reached, gate := deferred(), deferred()
		stubborn := oneStep("test.close-cancelled", func(context.Context) error {
			reached.resolve()
			_ = gate.wait(context.Background())
			return nil
		})
		harness, _, _ := openTasks(t, store, []durable.AnyTask{stubborn.Erased()})
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

	// harness-lifecycle.test.ts:257 it("settles durably a commit whose committer was cancelled while it was in Storage")
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

	// harness-lifecycle.test.ts:276 it("publishes no frame to watches from a commit that settles during close")
	t.Run("publishes no frame to states and watches from a commit that settles during close", func(t *testing.T) {
		notes := durable.DefineDoc(durable.DocDefinition[noteState]{
			Kind: "test.close-frames", Version: 1, Initial: func() noteState { return noteState{} }, CheckpointWhen: func(noteState, []durable.Op, durable.CheckpointInfo) bool { return false },
			Scope: durable.ScopeSession,
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
		docWatch, err := harness.WatchDocErased(testContext, notes)
		if err != nil || docWatch == nil {
			t.Fatalf("doc watch %v", err)
		}
		viewWatch, err := root.Watch(testContext, nil)
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
		graphWatch.Start(func(context.Context, chord.WatchCall[TaskGraph]) error { frame("graphWatch"); return nil })
		docWatch.Start(func(context.Context, chord.WatchCall[durable.JsonObject]) error { frame("docWatch"); return nil })
		viewWatch.Start(func(context.Context, chord.WatchCall[ConversationView]) error { frame("viewWatch"); return nil })
		docValue := docWatch.Value()
		viewValue := viewWatch.Value()

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
		if !reflect.DeepEqual(docWatch.Value(), docValue) || !reflect.DeepEqual(viewWatch.Value(), viewValue) {
			t.Fatal("a watch advanced during close")
		}
		expectEqualJSON(t, graphWatch.Value(), `{"tasks":{}}`)
		<-graphWatch.Closed()
		<-docWatch.Closed()
		<-viewWatch.Closed()
		for name, end := range map[string]durable.WatchEnd{"graph": graphWatch.End(), "doc": docWatch.End(), "view": viewWatch.End()} {
			if end.Kind != durable.WatchEnded {
				t.Fatalf("%s watch ended %v", name, end.Kind)
			}
		}
		// The commit itself settled.
		last := store.commitAt(store.commitCount() - 1)
		if !slicesContainsFunc(writeTypes(last), "entry") {
			t.Fatalf("last commit %v", writeTypes(last))
		}
	})

	// harness-lifecycle.test.ts:337 it("leaves no subscription behind a watch acquisition cancelled on the line")
	t.Run("leaves no subscription behind a watch acquisition cancelled on the line", func(t *testing.T) {
		notes := durable.DefineDoc(durable.DocDefinition[noteState]{
			Kind: "test.cancelled-watch", Version: 1, Initial: func() noteState { return noteState{} }, CheckpointWhen: func(noteState, []durable.Op, durable.CheckpointInfo) bool { return false },
			Scope: durable.ScopeSession,
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
		// Count commit subscriptions of document observers: upstream wraps session.followCommits; the Session counts them.
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
			_, err := root.Watch(cancelled, nil)
			return err
		})
		_ = find.entered.wait(testContext)
		cancel()
		find.release()
		_, err = viewWatch.wait()
		expectError(t, err, "cancelled")
		// No observer kept the mount: the next observer builds a new one, and the one after that another.
		first, err := root.Watch(testContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		firstValue := first.Value()
		_, _ = first.Stop()
		second, err := root.Watch(testContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sameEntries(second.Value().Entries, firstValue.Entries) && second.Value().Docs == firstValue.Docs {
			t.Fatal("a cancelled acquisition kept the mount")
		}
		_, _ = second.Stop()
		mustClose(t, harness)
	})

	// harness-lifecycle.test.ts:394 it("rejects conversation and Harness operations once close begins; inspect queued before reports closing")
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
		queueOnLineAdmitted(t, func(ctx context.Context) <-chan struct{} {
			queuedInspect = async(func() (HarnessInspection, error) { return harness.Inspect(ctx) })
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
			{"watch", func() error { _, err := root.Watch(testContext, nil); return err }},
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

	// harness-lifecycle.test.ts:451 it("completes reads queued at the seal and rejects queued waits and acquisitions that would follow commits")
	t.Run("completes reads queued at the seal and rejects queued waits and acquisitions that would follow commits", func(t *testing.T) {
		notes := durable.DefineDoc(durable.DocDefinition[noteState]{
			Kind: "test.queued-at-seal", Version: 1, Initial: func() noteState { return noteState{} }, CheckpointWhen: func(noteState, []durable.Op, durable.CheckpointInfo) bool { return false },
			Scope: durable.ScopeSession,
		})
		absent := durable.DefineDoc(durable.DocDefinition[noteState]{
			Kind: "test.queued-absent", Version: 1, Initial: func() noteState { return noteState{} }, CheckpointWhen: func(noteState, []durable.Op, durable.CheckpointInfo) bool { return false },
			Scope: durable.ScopeSession,
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
		names := []string{"context", "snapshot", "settledWait", "absentWatch", "waitForTask", "watchDoc", "watch", "watchTaskGraph"}
		runs := map[string]func(ctx context.Context) (bool, error){
			"context": func(ctx context.Context) (bool, error) { _, err := root.Context(ctx, nil); return true, err },
			"snapshot": func(ctx context.Context) (bool, error) {
				value, err := harness.SnapshotErased(ctx, notes)
				return value != nil, err
			},
			"settledWait": func(ctx context.Context) (bool, error) { _, err := write.Wait(ctx); return true, err },
			"absentWatch": func(ctx context.Context) (bool, error) {
				watch, err := harness.WatchDocErased(ctx, absent)
				return watch != nil, err
			},
			"waitForTask": func(ctx context.Context) (bool, error) { _, err := harness.WaitForTask(ctx, taskId); return true, err },
			"watchDoc": func(ctx context.Context) (bool, error) {
				watch, err := harness.WatchDocErased(ctx, notes)
				return watch != nil, err
			},
			"watch":          func(ctx context.Context) (bool, error) { _, err := root.Watch(ctx, nil); return true, err },
			"watchTaskGraph": func(ctx context.Context) (bool, error) { _, err := harness.WatchTaskGraph(ctx); return true, err },
		}
		queued := map[string]*future[string]{}
		for _, name := range names {
			queueOnLineAdmitted(t, func(ctx context.Context) <-chan struct{} {
				queued[name] = settle(func() (bool, error) { return runs[name](ctx) })
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
			"context":        "resolved",
			"snapshot":       "resolved",
			"settledWait":    "resolved",
			"absentWatch":    "undefined",
			"waitForTask":    "closed",
			"watchDoc":       "closed",
			"watch":          "closed",
			"watchTaskGraph": "closed",
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
	harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{marker.Erased()})
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
	// harness-lifecycle.test.ts:534 it("never schedules from a read-only viewer")
	t.Run("never schedules from a read-only viewer", func(t *testing.T) {
		opened := paused(t)
		harness, root := opened.harness, opened.root
		notes := durable.DefineDoc(durable.DocDefinition[noteState]{
			Kind: "test.viewer-notes", Version: 1, Initial: func() noteState { return noteState{} }, CheckpointWhen: func(noteState, []durable.Op, durable.CheckpointInfo) bool { return false },
			Scope: durable.ScopeSession,
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
		viewWatch, err := root.Watch(testContext, nil)
		check(err)
		_, err = viewWatch.Stop()
		check(err)
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

	// harness-lifecycle.test.ts:566 it("schedules from every progress call")
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
	// harness-lifecycle.test.ts:591 it("runs what the registry holds at resume: a definition installed or replaced after open")
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
		installed := addTask(t, registry, define("v1").Erased())
		if tasks := inspectOf(t, harness).Tasks; len(tasks) != 1 || tasks[0].State.Kind != TaskInspectionPending {
			t.Fatalf("tasks %+v", tasks)
		}
		// The same extension name replaces the definition in place, still before resume.
		installed.dispose()
		addTask(t, registry, define("v2").Erased())
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

var _ = session.CreateSession
