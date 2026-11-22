package harness

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
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
	defer mustClose(t, opened.harness)

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

	// First commit triggers Advance, which detaches the observer re-entrantly.
	tkStart(t, opened.root, task.Erased())
	// Subsequent commits must not reach the detached observer.
	for range 5 {
		tkStart(t, opened.root, task.Erased())
	}
	drainTaskGraphMount(opened.harness)

	if got := calls.Load(); got != 1 {
		t.Fatalf("Advance called %d times after self-Detach, want 1", got)
	}
}
