// Ports packages/durable/test/harness-observers.test.ts: the Harness's synchronous observers (ObserveConversation,
// ObserveTaskGraph, ObserveDoc of a task document) and task document watches, which end with their task.
//
// Go mappings forced by language mechanics. A WeakRef is a weak.Pointer and gc() is runtime.GC. Upstream reads the
// observer's call list right after an awaited commit; observers run inside the commit, so the same holds here.

package harness

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
)

type observersProgressState struct {
	Lines []string `json:"lines"`
}

var observersProgress = durable.DefineDoc(durable.DocDefinition[observersProgressState]{
	CommonDocDefinition: durable.CommonDocDefinition[observersProgressState]{Kind: "test.observers.progress", Version: 1, Initial: func() observersProgressState { return observersProgressState{Lines: []string{}} }, CheckpointWhen: func(observersProgressState, []durable.Op, durable.CheckpointInfo) bool { return false }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeTask},
})

// observersWriter is a task that writes its progress document, waits for gate, then completes, which retires the document.
func observersWriter(name string, gate *deferredGate, written func()) stepTask {
	return tkOneStep(name, func(ctx context.Context, task stepRecord, runtime stepRuntime) error {
		if err := runtime.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
			lines, err := durable.TxDoc[observersProgressState](tx, observersProgress, task.Id)
			if err != nil {
				return nil, err
			}
			return nil, pushJSON(lines.Array("lines"), "started")
		}); err != nil {
			return err
		}
		written()
		if err := gate.wait(ctx); err != nil {
			return err
		}
		return tkComplete(ctx, runtime)
	})
}

// observedCall is one call of a recorded observer: an advance with whether it ran inside the storage's adopt, or the end.
type observedCall[T any] struct {
	advance   *chord.WatchChange[T]
	adopting  bool
	end       bool
	endFailed error
}

// callRecorder is an observer recording each call and whether it ran inside the storage's adopt.
type callRecorder[T any] struct {
	adopting func() bool
	mu       sync.Mutex
	calls    []observedCall[T]
	onEnd    func(failure error)
}

func newCallRecorder[T any](store *controlledStorage) *callRecorder[T] {
	return &callRecorder[T]{adopting: store.adopting.Load}
}

func (recorder *callRecorder[T]) Advance(_ context.Context, change chord.WatchChange[T]) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.calls = append(recorder.calls, observedCall[T]{advance: &change, adopting: recorder.adopting()})
}

func (recorder *callRecorder[T]) End(failure error) {
	if recorder.onEnd != nil {
		recorder.onEnd(failure)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.calls = append(recorder.calls, observedCall[T]{end: true, endFailed: failure})
}

func (recorder *callRecorder[T]) all() []observedCall[T] {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]observedCall[T](nil), recorder.calls...)
}

// observersWriterOpened is a Harness with a writer task started in the root conversation and its progress document written.
type observersWriterOpened struct {
	harness Harness
	store   *controlledStorage
	root    Conversation
	id      durable.TaskId
	finish  func()
}

func openObserversWriter(t *testing.T) observersWriterOpened {
	t.Helper()
	store := newControlledStorage()
	gate, written := deferred(), deferred()
	writer := observersWriter("test.observers.writer", gate, written.resolve)
	opened := tkOpenRoot(t, []durable.AnyTask{writer.Erased()}, tkOptions{storage: store})
	id := tkStart(t, opened.root, writer.Erased())
	opened.harness.Resume()
	_ = written.wait(testContext)
	return observersWriterOpened{harness: opened.harness, store: store, root: opened.root, id: id, finish: gate.resolve}
}

// collectGarbage runs the collector until weak references to unreachable values are cleared.
func collectGarbage() {
	for range 5 {
		time.Sleep(time.Millisecond)
		runtime.GC()
	}
}

func appendPlainNote(t *testing.T, root Conversation) durable.EntryRecord {
	t.Helper()
	entry, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note", Data: map[string]any{}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestObserveConversationAndObserveTaskGraph(t *testing.T) {
	t.Run("start at the current value and advance inside adopt with the same change a watch gets", func(t *testing.T) {
		store := newControlledStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		harness, root := opened.harness, opened.root
		view := newCallRecorder[ConversationView](store)
		viewObservation := must(harness.ObserveConversation(testContext, root.Id(), view))
		graph := newCallRecorder[TaskGraph](store)
		graphObservation := must(harness.ObserveTaskGraph(testContext, graph))
		if viewObservation.Value().Conversation.Id != root.Id() {
			t.Fatalf("view conversation %d", viewObservation.Value().Conversation.Id)
		}
		if len(graphObservation.Value().Tasks) != 0 {
			t.Fatalf("graph %+v", graphObservation.Value())
		}
		watch := must(root.Watch(testContext))
		var watchedMu sync.Mutex
		var watched []chord.WatchChange[ConversationView]
		watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
			watchedMu.Lock()
			defer watchedMu.Unlock()
			watched = append(watched, change)
			return nil
		})
		entry := appendPlainNote(t, root)
		calls := view.all()
		if len(calls) != 1 {
			t.Fatalf("view calls %d", len(calls))
		}
		call := calls[0]
		if !call.adopting || call.advance.Retired || call.advance.Value.Entries[len(call.advance.Value.Entries)-1].Id != entry.Id {
			t.Fatalf("view call %+v", call)
		}
		eventually(t, func() bool { watchedMu.Lock(); defer watchedMu.Unlock(); return len(watched) == 1 })
		watchedMu.Lock()
		if !reflect.DeepEqual(watched[0], *call.advance) {
			t.Fatalf("watched %+v, observed %+v", watched[0], *call.advance)
		}
		watchedMu.Unlock()
		// No task changed: the graph got nothing.
		if got := graph.all(); len(got) != 0 {
			t.Fatalf("graph calls %+v", got)
		}
		viewObservation.Detach()
		appendPlainNote(t, root)
		if got := view.all(); len(got) != 1 {
			t.Fatalf("view calls after detach %d", len(got))
		}
		must(watch.Stop())
		mustClose(t, harness)
		if got := graph.all(); len(got) != 1 || !got[0].end || got[0].endFailed != nil {
			t.Fatalf("graph calls %+v", got)
		}
	})

	t.Run("end with the error that failed the Harness", func(t *testing.T) {
		store := newControlledStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		graph := newCallRecorder[TaskGraph](store)
		must(opened.harness.ObserveTaskGraph(testContext, graph))
		failure := errors.New("disk gone")
		store.failNextCommit(failure)
		_, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "note", Data: map[string]any{}})
		})
		if err == nil || !strings.Contains(err.Error(), "disk gone") {
			t.Fatalf("commit error %v", err)
		}
		<-opened.harness.Closed()
		got := graph.all()
		if len(got) != 1 || !got[0].end || !errors.Is(got[0].endFailed, failure) {
			t.Fatalf("graph calls %+v", got)
		}
	})

	t.Run("report a throwing observer, detach it and end it with the error, and keep the view going", func(t *testing.T) {
		store := newControlledStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		harness, root := opened.harness, opened.root
		bug := errors.New("observer bug")
		var endsMu sync.Mutex
		var ends []error
		must(harness.ObserveConversation(testContext, root.Id(), observerFuncs[ConversationView]{
			advance: func(context.Context, chord.WatchChange[ConversationView]) { panic(bug) },
			end:     func(failure error) { endsMu.Lock(); ends = append(ends, failure); endsMu.Unlock() },
		}))
		healthy := newCallRecorder[ConversationView](store)
		must(harness.ObserveConversation(testContext, root.Id(), healthy))
		appendPlainNote(t, root)
		appendPlainNote(t, root)
		if reports := opened.reports.all(); len(reports) != 1 || !errors.Is(reports[0], bug) {
			t.Fatalf("reports %v", reports)
		}
		if infos := opened.reports.allInfos(); !reflect.DeepEqual(infos, []durable.ReportInfo{{Type: durable.ReportListenerError}}) {
			t.Fatalf("infos %v", infos)
		}
		endsMu.Lock()
		if len(ends) != 1 || !errors.Is(ends[0], bug) {
			t.Fatalf("ends %v", ends)
		}
		endsMu.Unlock()
		if got := healthy.all(); len(got) != 2 {
			t.Fatalf("healthy calls %d", len(got))
		}
		mustClose(t, harness)
		endsMu.Lock()
		defer endsMu.Unlock()
		if len(ends) != 1 {
			t.Fatalf("ends after close %v", ends)
		}
		if end := harness.End(); end.Reason != durable.SessionEndClosed {
			t.Fatalf("end %+v", end)
		}
	})

	t.Run("reject, never throw, on a closed Harness", func(t *testing.T) {
		store := newControlledStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		mustClose(t, opened.harness)
		_, viewErr := opened.harness.ObserveConversation(testContext, opened.root.Id(), newCallRecorder[ConversationView](store))
		_, graphErr := opened.harness.ObserveTaskGraph(testContext, newCallRecorder[TaskGraph](store))
		for _, err := range []error{viewErr, graphErr} {
			if err == nil || !strings.Contains(err.Error(), "Session is closed") {
				t.Fatalf("error %v", err)
			}
		}
	})

	t.Run("reject an attachment cancelled while it waits on the line, and attach nothing", func(t *testing.T) {
		store := newControlledStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		harness, root := opened.harness, opened.root
		view := newCallRecorder[ConversationView](store)
		graph := newCallRecorder[TaskGraph](store)
		// A commit holds the line, so both attachments wait behind it.
		held := store.holdCommits()
		committing := make(chan struct{})
		go func() { appendPlainNote(t, root); close(committing) }()
		_ = held.entered.wait(testContext)
		ctx, cancel := context.WithCancelCause(testContext)
		jobs := harness.(*harnessImpl).LineJobs()
		attachingView := make(chan error, 1)
		attachingGraph := make(chan error, 1)
		go func() { _, err := harness.ObserveConversation(ctx, root.Id(), view); attachingView <- err }()
		go func() { _, err := harness.ObserveTaskGraph(ctx, graph); attachingGraph <- err }()
		eventually(t, func() bool { return harness.(*harnessImpl).LineJobs() >= jobs+2 })
		cancel(errors.New("gone"))
		held.release()
		<-committing
		for _, attaching := range []chan error{attachingView, attachingGraph} {
			if err := <-attaching; err == nil || !strings.Contains(err.Error(), "gone") {
				t.Fatalf("attach error %v", err)
			}
		}
		appendPlainNote(t, root)
		mustClose(t, harness)
		if len(view.all()) != 0 || len(graph.all()) != 0 {
			t.Fatalf("calls %+v %+v", view.all(), graph.all())
		}
	})

	t.Run("advance a graph observer with a blocked refresh, outside any commit", func(t *testing.T) {
		missing := tkOneStep("test.observers.missing", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) })
		store := newControlledStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		harness := opened.harness
		graph := newCallRecorder[TaskGraph](store)
		observation := must(harness.ObserveTaskGraph(testContext, graph))
		// Scheduling is not resumed: the task stays pending, blocked while its definition is missing.
		id := tkStart(t, opened.root, missing.Erased())
		key := taskKey(id)
		calls := graph.all()
		if len(calls) != 1 || !calls[0].adopting || calls[0].advance.Retired || calls[0].advance.Value.Tasks[key].Blocked != durable.BlockedMissingTask {
			t.Fatalf("calls %+v", calls)
		}
		first := calls[0].advance
		if len(first.Ops) != 1 || first.Ops[0][0] != "s" || !reflect.DeepEqual(first.Ops[0][1], []any{"tasks", key}) {
			t.Fatalf("ops %v", first.Ops)
		}
		if encoded := mustJSON(t, first.Ops[0][2]); !strings.Contains(encoded, `"blocked":"missing_task"`) {
			t.Fatalf("op value %s", encoded)
		}
		mustInstall(t, opened.registry, &durable.Extension{Name: "late", Tasks: []durable.AnyTask{missing.Erased()}})
		eventually(t, func() bool { return len(graph.all()) == 2 })
		// The registry change re-derives the reason on the line, with no commit and outside the storage's adopt.
		second := graph.all()[1]
		wantOps := []durable.Op{{"d", []any{"tasks", key, "blocked"}}}
		if second.adopting || second.advance.Retired || !reflect.DeepEqual(second.advance.Ops, wantOps) || second.advance.Value.Tasks[key].Blocked != "" {
			t.Fatalf("second call %+v", second)
		}
		harness.Resume()
		must(harness.WaitForTask(testContext, id))
		observation.Detach()
		mustClose(t, harness)
	})

	t.Run("keep no detached observer alive", func(t *testing.T) {
		store := newControlledStorage()
		opened := tkOpenRoot(t, nil, tkOptions{storage: store})
		harness, root := opened.harness, opened.root
		type attached struct {
			observations []any
			detachAll    func()
			view         weak.Pointer[observerFuncs[ConversationView]]
			graphObs     weak.Pointer[observerFuncs[TaskGraph]]
		}
		// attach attaches observers only the Harness references; detach says whether to detach them before returning.
		attach := func(detach bool) attached {
			view := &observerFuncs[ConversationView]{advance: func(context.Context, chord.WatchChange[ConversationView]) {}, end: func(error) {}}
			graph := &observerFuncs[TaskGraph]{advance: func(context.Context, chord.WatchChange[TaskGraph]) {}, end: func(error) {}}
			viewObservation := must(harness.ObserveConversation(testContext, root.Id(), view))
			graphObservation := must(harness.ObserveTaskGraph(testContext, graph))
			detachAll := func() { viewObservation.Detach(); graphObservation.Detach() }
			if detach {
				detachAll()
			}
			return attached{observations: []any{viewObservation, graphObservation}, detachAll: detachAll, view: weak.Make(view), graphObs: weak.Make(graph)}
		}
		// Control: attached observers stay, so the collection below is real.
		control := attach(false)
		collectGarbage()
		if control.view.Value() == nil || control.graphObs.Value() == nil {
			t.Fatal("an attached observer was collected")
		}
		control.detachAll()
		runtime.KeepAlive(control.observations)

		detached := attach(true)
		collectGarbage()
		if detached.view.Value() != nil || detached.graphObs.Value() != nil {
			t.Fatal("a detached observer is still alive")
		}
		runtime.KeepAlive(detached.observations)
		mustClose(t, harness)
	})
}

// observerFuncs is an Observer made of two functions.
type observerFuncs[T any] struct {
	advance func(ctx context.Context, change chord.WatchChange[T])
	end     func(failure error)
}

func (observer observerFuncs[T]) Advance(ctx context.Context, change chord.WatchChange[T]) {
	observer.advance(ctx, change)
}
func (observer observerFuncs[T]) End(failure error) { observer.end(failure) }

func TestTaskDocuments(t *testing.T) {
	watchProgress := func(t *testing.T, opened observersWriterOpened) durable.DocumentWatch[observersProgressState] {
		t.Helper()
		watch, err := durable.WatchDoc(testContext, opened.harness, observersProgress, opened.id)
		if err != nil || watch == nil {
			t.Fatalf("watch %v %v", watch, err)
		}
		return watch
	}
	t.Run("a watch delivers the retirement of the commit that ends its task, then ends ended", func(t *testing.T) {
		opened := openObserversWriter(t)
		watch := watchProgress(t, opened)
		if !reflect.DeepEqual(watch.Value(), observersProgressState{Lines: []string{"started"}}) {
			t.Fatalf("value %+v", watch.Value())
		}
		var mu sync.Mutex
		var changes []chord.WatchChange[observersProgressState]
		watch.Start(func(_ context.Context, change chord.WatchChange[observersProgressState]) error {
			mu.Lock()
			defer mu.Unlock()
			changes = append(changes, change)
			return nil
		})
		opened.finish()
		must(opened.harness.WaitForTask(testContext, opened.id))
		<-watch.Closed()
		if end := watch.End(); end.Reason != chord.WatchEnded {
			t.Fatalf("end %+v", end)
		}
		mu.Lock()
		if len(changes) != 1 || !changes[0].Retired || len(changes[0].Ops) != 0 || !reflect.DeepEqual(changes[0].Value, observersProgressState{Lines: []string{"started"}}) || !reflect.DeepEqual(changes[0].Value, watch.Value()) {
			t.Fatalf("changes %+v", changes)
		}
		mu.Unlock()
		// The task is terminal: its address is absent for good.
		if again, err := durable.WatchDoc(testContext, opened.harness, observersProgress, opened.id); err != nil || again != nil {
			t.Fatalf("watch of an ended task %v %v", again, err)
		}
		mustClose(t, opened.harness)
	})

	t.Run("an observer gets the retirement, then its end, inside the adopt of the commit that ends the task", func(t *testing.T) {
		opened := openObserversWriter(t)
		progress := newCallRecorder[observersProgressState](opened.store)
		var endedAdopting bool
		progress.onEnd = func(error) { endedAdopting = opened.store.adopting.Load() }
		must(durable.ObserveDoc(testContext, opened.harness, observersProgress, progress, opened.id))
		opened.finish()
		must(opened.harness.WaitForTask(testContext, opened.id))
		calls := progress.all()
		if len(calls) != 2 || calls[0].advance == nil || !calls[0].adopting || !calls[0].advance.Retired || len(calls[0].advance.Ops) != 0 || !reflect.DeepEqual(calls[0].advance.Value, observersProgressState{Lines: []string{"started"}}) || !calls[1].end || calls[1].endFailed != nil {
			t.Fatalf("calls %+v", calls)
		}
		if !endedAdopting {
			t.Fatal("the end did not run inside adopt")
		}
		mustClose(t, opened.harness)
		if got := progress.all(); len(got) != 2 {
			t.Fatalf("calls after close %d", len(got))
		}
	})

	t.Run("keeps no task document watch alive once its task ended", func(t *testing.T) {
		opened := openObserversWriter(t)
		// acquire starts a watch of the task's document, which only the Harness references.
		acquire := func() weak.Pointer[chord.Watch[durable.JsonObject]] {
			watch := must(opened.harness.WatchDocErased(testContext, observersProgress, opened.id))
			watch.Start(func(context.Context, chord.WatchChange[durable.JsonObject]) error { return nil })
			return weak.Make(watch.(*chord.Watch[durable.JsonObject]))
		}
		watch := acquire()
		// Control: the watch stays while its task runs, so the collection below is real.
		collectGarbage()
		if watch.Value() == nil {
			t.Fatal("a running task's watch was collected")
		}
		opened.finish()
		must(opened.harness.WaitForTask(testContext, opened.id))
		collectGarbage()
		if watch.Value() != nil {
			t.Fatal("the watch of an ended task is still alive")
		}
		// The Harness is the Session kernel; nothing stays pinned for the ended watch.
		if sizes := opened.harness.(*harnessImpl).DocumentCacheSizes(); sizes.Attached != 0 {
			t.Fatalf("attached %d", sizes.Attached)
		}
		mustClose(t, opened.harness)
	})

	t.Run("a watch never started when its task ends still ends with the Harness's close, keeping nothing of it", func(t *testing.T) {
		// acquire acquires a task document's watch, never starts it, ends its task, and closes the Harness.
		acquire := func() (durable.DocumentWatch[observersProgressState], weak.Pointer[harnessImpl]) {
			opened := openObserversWriter(t)
			watch := watchProgress(t, opened)
			opened.finish()
			must(opened.harness.WaitForTask(testContext, opened.id))
			// Its retirement is pending until a start that never comes: the watch is open.
			flush()
			if settled(watch.Closed()) {
				t.Fatal("the watch ended before the Harness closed")
			}
			mustClose(t, opened.harness)
			return watch, weak.Make(opened.harness.(*harnessImpl))
		}
		watch, harness := acquire()
		// Closed at the Harness's close, the pending retirement dropped, as for every watch.
		<-watch.Closed()
		if end := watch.End(); end.Reason != chord.WatchEnded {
			t.Fatalf("end %+v", end)
		}
		tkExpectPanic(t, "Watch is stopped", func() {
			watch.Start(func(context.Context, chord.WatchChange[observersProgressState]) error { return nil })
		})
		collectGarbage()
		if harness.Value() != nil {
			t.Fatal("the Harness is still alive")
		}
	})

	t.Run("a watch never started when its task ends fails with the Harness", func(t *testing.T) {
		opened := openObserversWriter(t)
		watch := watchProgress(t, opened)
		opened.finish()
		must(opened.harness.WaitForTask(testContext, opened.id))
		failure := errors.New("disk gone")
		opened.store.failNextCommit(failure)
		_, err := durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "note"})
		})
		if err == nil || !strings.Contains(err.Error(), "disk gone") {
			t.Fatalf("commit error %v", err)
		}
		<-watch.Closed()
		if end := watch.End(); end.Reason != chord.WatchFailed || !errors.Is(end.Error, failure) {
			t.Fatalf("end %+v", end)
		}
	})

	t.Run("a watch busy in an earlier call when its task ends fails with the Harness before its retirement call", func(t *testing.T) {
		opened := openObserversWriter(t)
		watch := watchProgress(t, opened)
		entered, release := deferred(), deferred()
		var mu sync.Mutex
		var values []observersProgressState
		watch.Start(func(_ context.Context, change chord.WatchChange[observersProgressState]) error {
			mu.Lock()
			values = append(values, change.Value)
			mu.Unlock()
			entered.resolve()
			return release.wait(testContext)
		})
		_, err := opened.harness.Commit(testContext, func(tx durable.Tx) (any, error) {
			lines, err := durable.TxDoc[observersProgressState](tx, observersProgress, opened.id)
			if err != nil {
				return nil, err
			}
			return nil, pushJSON(lines.Array("lines"), "more")
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = entered.wait(testContext)
		// The task ends while the listener is busy: its retirement waits behind the running call.
		opened.finish()
		must(opened.harness.WaitForTask(testContext, opened.id))
		failure := errors.New("disk gone")
		opened.store.failNextCommit(failure)
		_, err = durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "note"})
		})
		if err == nil || !strings.Contains(err.Error(), "disk gone") {
			t.Fatalf("commit error %v", err)
		}
		<-watch.Closed()
		if end := watch.End(); end.Reason != chord.WatchFailed || !errors.Is(end.Error, failure) {
			t.Fatalf("end %+v", end)
		}
		release.resolve()
		flush()
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(values, []observersProgressState{{Lines: []string{"started", "more"}}}) {
			t.Fatalf("values %+v", values)
		}
	})

	t.Run("a watch whose retirement call runs when its task ends ends only after that call returns", func(t *testing.T) {
		opened := openObserversWriter(t)
		watch := watchProgress(t, opened)
		entered, release := deferred(), deferred()
		var returned bool
		var mu sync.Mutex
		watch.Start(func(context.Context, chord.WatchChange[observersProgressState]) error {
			entered.resolve()
			err := release.wait(testContext)
			mu.Lock()
			returned = true
			mu.Unlock()
			return err
		})
		// An explicit retirement first: its call runs while the task ends, and nothing more is pending.
		if _, err := opened.harness.Commit(testContext, func(tx durable.Tx) (any, error) {
			return nil, durable.TxRetireDoc(tx, observersProgress, opened.id)
		}); err != nil {
			t.Fatal(err)
		}
		_ = entered.wait(testContext)
		opened.finish()
		must(opened.harness.WaitForTask(testContext, opened.id))
		flush()
		if settled(watch.Closed()) {
			t.Fatal("the watch ended before its retirement call returned")
		}
		release.resolve()
		<-watch.Closed()
		if end := watch.End(); end.Reason != chord.WatchEnded {
			t.Fatalf("end %+v", end)
		}
		mu.Lock()
		if !returned {
			t.Fatal("the retirement call did not return before the end")
		}
		mu.Unlock()
		mustClose(t, opened.harness)
	})

	t.Run("hands listeners changes that keep nothing of the Harness", func(t *testing.T) {
		// acquire watches a task document until its task ends, keeping every change, then closes the Harness.
		acquire := func() ([]chord.WatchChange[observersProgressState], weak.Pointer[harnessImpl]) {
			opened := openObserversWriter(t)
			watch := watchProgress(t, opened)
			var mu sync.Mutex
			var changes []chord.WatchChange[observersProgressState]
			watch.Start(func(_ context.Context, change chord.WatchChange[observersProgressState]) error {
				mu.Lock()
				defer mu.Unlock()
				changes = append(changes, change)
				return nil
			})
			if _, err := opened.harness.Commit(testContext, func(tx durable.Tx) (any, error) {
				lines, err := durable.TxDoc[observersProgressState](tx, observersProgress, opened.id)
				if err != nil {
					return nil, err
				}
				return nil, pushJSON(lines.Array("lines"), "more")
			}); err != nil {
				t.Fatal(err)
			}
			flush()
			opened.finish()
			must(opened.harness.WaitForTask(testContext, opened.id))
			<-watch.Closed()
			if end := watch.End(); end.Reason != chord.WatchEnded {
				t.Fatalf("end %+v", end)
			}
			mustClose(t, opened.harness)
			mu.Lock()
			defer mu.Unlock()
			return append([]chord.WatchChange[observersProgressState](nil), changes...), weak.Make(opened.harness.(*harnessImpl))
		}
		changes, harness := acquire()
		if len(changes) != 2 {
			t.Fatalf("%d changes", len(changes))
		}
		collectGarbage()
		if harness.Value() != nil {
			t.Fatal("the changes keep the Harness alive")
		}
		runtime.KeepAlive(changes)
	})
}
