package harness

import (
	"context"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func mountRaceMissingTask(name string) stepTask {
	return tkOneStep(name, func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		return tkComplete(ctx, runtime)
	})
}

// drainTaskGraphMount waits for scheduler background refreshes (registry -> notifyBlockedLocked -> refreshBlocked),
// queued ReadOnLine jobs, and watch deliveries to settle.
func drainTaskGraphMount(harness Harness) {
	impl := harness.(*harnessImpl)
	impl.tasks.background.Wait()
	drained(harness)
}

func assertNoTaskBlocked(t *testing.T, graph TaskGraph, kind string) {
	t.Helper()
	for _, key := range slices.Sorted(maps.Keys(graph.Tasks)) {
		node := graph.Tasks[key]
		if node.Kind == kind && node.Blocked != "" {
			t.Fatalf("task %s (kind %s) still shown blocked: %q", key, node.Kind, node.Blocked)
		}
	}
}

func diffTaskGraphs(got, want TaskGraph) []string {
	var diffs []string
	allKeys := map[string]struct{}{}
	for k := range got.Tasks {
		allKeys[k] = struct{}{}
	}
	for k := range want.Tasks {
		allKeys[k] = struct{}{}
	}
	for _, k := range slices.Sorted(maps.Keys(allKeys)) {
		g, okG := got.Tasks[k]
		w, okW := want.Tasks[k]
		if !okG {
			diffs = append(diffs, fmt.Sprintf("task %s: missing from long-lived graph (fresh=%+v)", k, w))
			continue
		}
		if !okW {
			diffs = append(diffs, fmt.Sprintf("task %s: extra in long-lived graph (got=%+v)", k, g))
			continue
		}
		if g.Id != w.Id {
			diffs = append(diffs, fmt.Sprintf("task %s: Id got=%d want=%d", k, g.Id, w.Id))
		}
		if g.Kind != w.Kind {
			diffs = append(diffs, fmt.Sprintf("task %s: Kind got=%q want=%q", k, g.Kind, w.Kind))
		}
		if g.ConversationId != w.ConversationId {
			diffs = append(diffs, fmt.Sprintf("task %s: ConversationId got=%d want=%d", k, g.ConversationId, w.ConversationId))
		}
		if !reflect.DeepEqual(g.Owner, w.Owner) {
			diffs = append(diffs, fmt.Sprintf("task %s: Owner got=%v want=%v", k, g.Owner, w.Owner))
		}
		if g.Background != w.Background {
			diffs = append(diffs, fmt.Sprintf("task %s: Background got=%v want=%v", k, g.Background, w.Background))
		}
		if g.AbortRequested != w.AbortRequested {
			diffs = append(diffs, fmt.Sprintf("task %s: AbortRequested got=%v want=%v", k, g.AbortRequested, w.AbortRequested))
		}
		if !reflect.DeepEqual(g.State, w.State) {
			diffs = append(diffs, fmt.Sprintf("task %s: State got=%+v want=%+v", k, g.State, w.State))
		}
		if !reflect.DeepEqual(g.Conversations, w.Conversations) {
			diffs = append(diffs, fmt.Sprintf("task %s: Conversations got=%v want=%v", k, g.Conversations, w.Conversations))
		}
		if g.Blocked != w.Blocked {
			diffs = append(diffs, fmt.Sprintf("task %s: Blocked got=%q want=%q", k, g.Blocked, w.Blocked))
		}
	}
	return diffs
}

func formatDiffs(diffs []string, max int) string {
	if len(diffs) > max {
		diffs = diffs[:max]
	}
	out := ""
	for _, d := range diffs {
		out += "  " + d + "\n"
	}
	return out
}

func TestTaskGraphMountRace(t *testing.T) {
	const iterations = 200
	const kind = "test.mount-race-missing"

	missing := mountRaceMissingTask(kind)
	ext := &durable.Extension{Name: "mount-race-ext", Tasks: []durable.AnyTask{missing.Erased()}}
	opened := tkOpenRoot(t, nil)
	defer mustClose(t, opened.harness)

	var obsMu sync.Mutex
	var latest TaskGraph
	obs := must(opened.harness.ObserveTaskGraph(testContext, observerFuncs[TaskGraph]{
		advance: func(_ context.Context, change chord.WatchCall[TaskGraph]) {
			obsMu.Lock()
			latest = change.Value
			obsMu.Unlock()
		},
		end: func(durable.WatchEnd) {},
	}))
	obsMu.Lock()
	latest = obs.Value()
	obsMu.Unlock()
	defer obs.Detach()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range iterations {
			tkStart(t, opened.root, missing.Erased())
		}
	}()
	go func() {
		defer wg.Done()
		for range iterations {
			if err := opened.registry.Install(ext); err != nil {
				t.Errorf("Install: %v", err)
				return
			}
			watch, err := opened.harness.WatchTaskGraph(testContext)
			if err != nil {
				t.Errorf("WatchTaskGraph: %v", err)
				return
			}
			if _, err := watch.Stop(); err != nil {
				t.Errorf("watch.Stop: %v", err)
				return
			}
			opened.registry.Uninstall(ext)
		}
	}()
	wg.Wait()

	// Leave the definition installed and let all background refreshes and deliveries settle.
	if err := opened.registry.Install(ext); err != nil {
		t.Fatalf("final Install: %v", err)
	}
	drainTaskGraphMount(opened.harness)

	obsMu.Lock()
	got := latest
	obsMu.Unlock()
	if len(got.Tasks) != iterations {
		t.Fatalf("got %d tasks, want %d", len(got.Tasks), iterations)
	}
	assertNoTaskBlocked(t, got, kind)
}

func TestTaskGraphReasonIsFreshAfterRegistryChanges(t *testing.T) {
	const iterations = 400
	const kind = "test.mount-race-fresh-reason"

	missing := mountRaceMissingTask(kind)
	ext := &durable.Extension{Name: "mount-race-fresh-ext", Tasks: []durable.AnyTask{missing.Erased()}}
	opened := tkOpenRoot(t, nil)
	defer mustClose(t, opened.harness)

	var obsMu sync.Mutex
	var latest TaskGraph
	obs := must(opened.harness.ObserveTaskGraph(testContext, observerFuncs[TaskGraph]{
		advance: func(_ context.Context, change chord.WatchCall[TaskGraph]) {
			obsMu.Lock()
			latest = change.Value
			obsMu.Unlock()
		},
		end: func(durable.WatchEnd) {},
	}))
	obsMu.Lock()
	latest = obs.Value()
	obsMu.Unlock()
	defer obs.Detach()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range iterations {
			tkStart(t, opened.root, missing.Erased())
		}
	}()
	go func() {
		defer wg.Done()
		for range iterations {
			if err := opened.registry.Install(ext); err != nil {
				t.Errorf("Install: %v", err)
				return
			}
			opened.registry.Uninstall(ext)
		}
	}()
	wg.Wait()

	// End with the definition installed and everything quiet.
	if err := opened.registry.Install(ext); err != nil {
		t.Fatalf("final Install: %v", err)
	}
	drainTaskGraphMount(opened.harness)

	obsMu.Lock()
	got := latest
	obsMu.Unlock()

	assertNoTaskBlocked(t, got, kind)

	// Detach the long-lived observer so the next observer triggers a fresh build from storage
	// rather than reusing the existing mount.value.
	obs.Detach()

	fresh := must(opened.harness.ObserveTaskGraph(testContext, observerFuncs[TaskGraph]{
		advance: func(context.Context, chord.WatchCall[TaskGraph]) {},
		end:     func(durable.WatchEnd) {},
	}))
	want := fresh.Value()
	fresh.Detach()

	if diffs := diffTaskGraphs(got, want); len(diffs) > 0 {
		t.Fatalf("long-lived graph differs from fresh build (%d diffs, first 10):\n%s",
			len(diffs), formatDiffs(diffs, 10))
	}
}

func TestConversationViewMountRace(t *testing.T) {
	const iterations = 600
	const committers = 2
	const watchers = 2

	harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
	defer closeHarness(t, harness)

	first := must(root.Watch(testContext, nil))
	defer func() { _, _ = first.Stop() }()

	var wg sync.WaitGroup
	wg.Add(committers + watchers)
	for c := range committers {
		go func(id int) {
			defer wg.Done()
			for i := range iterations {
				commitValue(t, root, func(tx durable.Tx) (any, error) {
					live, err := durable.TxDoc(tx, LiveDoc, root.Id())
					if err != nil {
						return nil, err
					}
					return nil, live.Set("step", id*iterations+i)
				})
			}
		}(c)
	}
	for range watchers {
		go func() {
			defer wg.Done()
			for range iterations {
				watch, err := root.Watch(testContext, nil)
				if err != nil {
					t.Errorf("Watch: %v", err)
					return
				}
				if _, err := watch.Stop(); err != nil {
					t.Errorf("watch.Stop: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestTaskGraphObserverMayDetachInsideAdvance(t *testing.T) {
	task := mountRaceMissingTask("test.detach-inside-advance")
	opened := tkOpenRoot(t, []durable.AnyTask{task.Erased()})
	timedOut := false
	defer func() {
		if timedOut {
			return
		}
		mustClose(t, opened.harness)
	}()

	var calls atomic.Int32
	var detachFn func()
	var detachMu sync.Mutex

	obs := must(opened.harness.ObserveTaskGraph(testContext, observerFuncs[TaskGraph]{
		advance: func(context.Context, chord.WatchCall[TaskGraph]) {
			calls.Add(1)
			detachMu.Lock()
			fn := detachFn
			detachMu.Unlock()
			if fn != nil {
				fn()
			}
		},
		end: func(durable.WatchEnd) {},
	}))
	detachMu.Lock()
	detachFn = obs.Detach
	detachMu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// First commit triggers Advance, which detaches the observer re-entrantly.
		tkStart(t, opened.root, task.Erased())
		// Subsequent commits must not reach the detached observer.
		for range 5 {
			tkStart(t, opened.root, task.Erased())
		}
		drainTaskGraphMount(opened.harness)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		timedOut = true
		t.Fatal("an observer that detaches itself inside Advance deadlocked the delivery")
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("Advance called %d times after self-Detach, want 1", got)
	}
}

func TestTaskGraphReasonReadBeforeARefreshIsNotPublishedAfterIt(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	line := session.CreateSession(store)
	defer func() { _ = line.Close(ctx) }()

	var (
		installed        atomic.Bool
		listenerMu       sync.Mutex
		listener         func()
		refreshSeenOnce  sync.Once
		refreshSeen      = make(chan struct{})
		refreshDone      = make(chan struct{})
		refreshTriggered atomic.Bool
		refreshTimedOut  atomic.Value // string
		createdID        atomic.Int64
	)

	view := NewTaskGraphView(line, TaskGraphBlocking{
		Blocked: func(subject BlockedSubject) durable.BlockedReason {
			if subject.Kind == "probe" {
				// Only the writer's first call for the new task triggers the refresh and pauses.
				// Use CompareAndSwap (not sync.Once) so the refresh's concurrent Blocked call
				// does not block behind the writer's pause. With the guard, the writer computes
				// again; its second (and every later) call reads installed and returns the fresh
				// reason without pausing again.
				if refreshTriggered.CompareAndSwap(false, true) {
					installed.Store(true)
					listenerMu.Lock()
					fn := listener
					listenerMu.Unlock()
					go func() {
						defer close(refreshDone)
						if fn != nil {
							fn()
						}
					}()
					// Signal (1): wait for the refresh's own Blocked call for this subject so we know
					// its line job is in and has read the record.
					select {
					case <-refreshSeen:
					case <-time.After(5 * time.Second):
						refreshTimedOut.Store("the refresh's job did not read the record within 5 s")
						return durable.BlockedMissingTask
					}
					// Signal (2): wait for the session's line jobs to return to zero so we know
					// the refresh's job has finished looking at mount.value and left the line.
					deadline := time.Now().Add(5 * time.Second)
					for line.LineJobs() != 0 {
						if time.Now().After(deadline) {
							refreshTimedOut.Store("the refresh's job did not leave the line within 5 s")
							return durable.BlockedMissingTask
						}
						time.Sleep(time.Millisecond)
					}
					return durable.BlockedMissingTask
				}
				refreshSeenOnce.Do(func() { close(refreshSeen) })
			}
			if !installed.Load() {
				return durable.BlockedMissingTask
			}
			return ""
		},
		SubscribeBlocked: func(fn func()) func() {
			listenerMu.Lock()
			listener = fn
			listenerMu.Unlock()
			return func() {}
		},
	})

	var (
		graphMu sync.Mutex
		latest  TaskGraph
	)
	obs, err := view.Observe(ctx, observerFuncs[TaskGraph]{
		advance: func(_ context.Context, change chord.WatchCall[TaskGraph]) {
			graphMu.Lock()
			latest = change.Value
			graphMu.Unlock()
		},
		end: func(durable.WatchEnd) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Detach()

	_, err = line.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		conv, err := tx.CreateConversation(durable.CreateConversationOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless},
		})
		if err != nil {
			return nil, err
		}
		convID := conv.Id
		id, err := tx.CreateTaskErased(graphProbeTask.Erased(), float64(1), durable.TaskOptions{
			Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnedByConversation},
			ConversationId: &convID,
		})
		if err != nil {
			return nil, err
		}
		createdID.Store(int64(id))
		return id, nil
	}, session.TransactionScope{})
	if err != nil {
		t.Fatal(err)
	}

	_, _ = line.ReadOnLine(func(durable.Storage) (any, error) { return nil, nil })
	line.WaitDeliveries()

	if !refreshTriggered.Load() {
		t.Fatalf("expected refresh listener to be triggered during writer's Blocked call")
	}
	select {
	case <-refreshDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("refresh listener did not return after drain within 5 s")
	}
	if msg, _ := refreshTimedOut.Load().(string); msg != "" {
		t.Fatal(msg)
	}

	key := taskKey(durable.TaskId(createdID.Load()))
	graphMu.Lock()
	node, ok := latest.Tasks[key]
	graphMu.Unlock()
	if !ok {
		t.Fatalf("task %s missing from observer's graph", key)
	}
	if node.Blocked != "" {
		t.Fatalf("task %s shown blocked with the stale reason %q", key, node.Blocked)
	}
}

func TestTaskGraphMountLockReleasedOnSameNodePanic(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	line := session.CreateSession(store)

	view := NewTaskGraphView(line, TaskGraphBlocking{
		Blocked:          func(BlockedSubject) durable.BlockedReason { return "" },
		SubscribeBlocked: func(func()) func() { return func() {} },
	})

	obs, err := view.Observe(ctx, observerFuncs[TaskGraph]{
		advance: func(context.Context, chord.WatchCall[TaskGraph]) {},
		end:     func(durable.WatchEnd) {},
	})
	if err != nil {
		t.Fatal(err)
	}

	view.mu.Lock()
	mount := view.mount
	view.mu.Unlock()
	if mount == nil {
		t.Fatal("expected mount to be created")
	}

	// Build a live sleeping task record whose Until is NaN so sameNode panics while marshaling JSON.
	// Insert a prior node so advanceTaskGraph enters sameNode(next, *previous) with mount.mu held.
	const taskID durable.TaskId = 1
	key := taskKey(taskID)
	nan := math.NaN()
	record := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
		Id:             taskID,
		Kind:           "probe",
		Version:        1,
		ConversationId: 1,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status: durable.TaskSleeping,
			Until:  &nan,
		},
	}

	mount.mu.Lock()
	mount.value = TaskGraph{Tasks: map[string]TaskGraphNode{
		key: {Id: taskID, Kind: "probe", ConversationId: 1, State: TaskGraphState{Status: durable.TaskPending}},
	}}
	mount.mu.Unlock()

	pub := session.ObservedCommit{
		Changes: []durable.CommitChange{durable.TaskWrite{Value: record}},
	}

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		advanceTaskGraph(ctx, mount, nil, pub, func(BlockedSubject) durable.BlockedReason { return "" }, func(error) {})
	}()
	if recovered == nil {
		t.Fatal("expected advanceTaskGraph to panic in sameNode for non-JSON sleeping Until")
	}

	// Verify mount.mu is not left held after the panic by attaching a second observer within a strict 5s bound.
	attachDone := make(chan error, 1)
	go func() {
		obs2, err := view.Observe(ctx, observerFuncs[TaskGraph]{
			advance: func(context.Context, chord.WatchCall[TaskGraph]) {},
			end:     func(durable.WatchEnd) {},
		})
		if err == nil {
			obs2.Detach()
		}
		attachDone <- err
	}()

	select {
	case err := <-attachDone:
		if err != nil {
			t.Fatalf("second Observe after panic: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mount.mu remained locked after sameNode panic: second Observe did not complete within 5 s")
	}

	obs.Detach()
	closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = line.Close(closeCtx)
}
