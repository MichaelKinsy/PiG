// Ports packages/durable/test/harness-task-graph.test.ts.

package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

type graphParentState struct {
	Phase string          `json:"phase"`
	Child *durable.TaskId `json:"child,omitempty"`
}

type graphChildInput struct {
	Late bool `json:"late"`
}

type graphGates struct{ child, late *deferredGate }

// graphFamily is a parent that creates a child task and a conversation it owns, waits for the child, creates a second child, and completes while that child is still live, so its outcome is held as completing (harness-task-graph.test.ts:25).
func graphFamily(gates graphGates) (durable.Task[durable.JsonValue, graphParentState, durable.JsonValue, any], durable.Task[graphChildInput, stepState, durable.JsonValue, any]) {
	type childRuntime = durable.TaskRuntime[graphChildInput, stepState, durable.JsonValue, any]
	type childRecord = durable.RunningTask[graphChildInput, stepState, durable.JsonValue]
	child := durable.DefineTask(durable.TaskDefinition[graphChildInput, stepState, durable.JsonValue, any]{
		Name:    "test.graph-child",
		Version: 1,
		Initial: func(graphChildInput) stepState { return stepState{Phase: "work"} },
		Phases: map[string]durable.PhaseHandler[graphChildInput, stepState, durable.JsonValue, any]{
			"work": func(ctx context.Context, task childRecord, runtime childRuntime) error {
				gate := gates.child
				if task.Input.Late {
					gate = gates.late
				}
				if err := gate.wait(ctx); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(durable.Tx, childRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(context.Context, childRecord, childRuntime) error { return nil },
	})
	type parentRuntime = durable.TaskRuntime[durable.JsonValue, graphParentState, durable.JsonValue, any]
	type parentRecord = durable.RunningTask[durable.JsonValue, graphParentState, durable.JsonValue]
	parent := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, graphParentState, durable.JsonValue, any]{
		Name:    "test.graph-parent",
		Version: 1,
		Initial: func(durable.JsonValue) graphParentState { return graphParentState{Phase: "spawn"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, graphParentState, durable.JsonValue, any]{
			"spawn": func(ctx context.Context, task parentRecord, runtime parentRuntime) error {
				ownership := durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: task.Id}
				// Two conversations in a commit that leaves the parent's record unchanged.
				if err := runtime.Commit(ctx, func(tx durable.Tx, _ parentRecord) (*durable.NextTaskState[graphParentState, durable.JsonValue], error) {
					for range 2 {
						if _, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownership}); err != nil {
							return nil, err
						}
					}
					return nil, nil
				}); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx durable.Tx, _ parentRecord) (*durable.NextTaskState[graphParentState, durable.JsonValue], error) {
					id, err := durable.CreateTask(tx, child, graphChildInput{Late: false}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: task.Id}})
					if err != nil {
						return nil, err
					}
					return &durable.NextTaskState[graphParentState, durable.JsonValue]{Status: durable.TaskWaiting, Checkpoint: &graphParentState{Phase: "join", Child: &id}, On: []durable.TaskId{id}, Policy: durable.JoinAllSettled}, nil
				})
			},
			"join": func(ctx context.Context, task parentRecord, runtime parentRuntime) error {
				return runtime.Commit(ctx, func(tx durable.Tx, _ parentRecord) (*durable.NextTaskState[graphParentState, durable.JsonValue], error) {
					if _, err := durable.CreateTask(tx, child, graphChildInput{Late: true}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: task.Id}}); err != nil {
						return nil, err
					}
					return &durable.NextTaskState[graphParentState, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &graphParentState{Phase: "finish"}}, nil
				})
			},
			"finish": func(ctx context.Context, _ parentRecord, runtime parentRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, parentRecord) (*durable.NextTaskState[graphParentState, durable.JsonValue], error) {
					return completed[graphParentState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(context.Context, parentRecord, parentRuntime) error { return nil },
	})
	return parent, child
}

// graphStatuses maps kind#id to node status, for compact assertions.
func graphStatuses(graph TaskGraph) map[string]string {
	statuses := map[string]string{}
	for _, node := range graph.Tasks {
		statuses[fmt.Sprintf("%s#%d", node.Kind, node.Id)] = string(node.State.Status)
	}
	return statuses
}

func sameGraph(a, b TaskGraph) bool {
	return reflect.ValueOf(a.Tasks).UnsafePointer() == reflect.ValueOf(b.Tasks).UnsafePointer()
}

// graphValue reads a state's value under the Session's delivery order: once delivered, the state holds the revision.
func graphValue(harness Harness, state durable.AttachedReplicatedState[TaskGraph]) TaskGraph {
	harness.(*harnessImpl).WaitDeliveries()
	return state.Value()
}

func TestTaskGraphView(t *testing.T) {
	t.Run("follows every live task through its statuses, owner edges, and owned conversations", func(t *testing.T) {
		childGate, lateGate := deferred(), deferred()
		parentTask, childTask := graphFamily(graphGates{child: childGate, late: lateGate})
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{parentTask, childTask})
		root := mustRoot(t, harness, nil)
		var mu sync.Mutex
		var seen []TaskGraph
		observe := func() durable.AttachedReplicatedState[TaskGraph] {
			opened := must(harness.TaskGraph(testContext))
			if _, err := opened.Subscribe(func(value TaskGraph, _ context.Context, delivery chord.ReplicatedStateDelivery) {
				if delivery.Kind == chord.DeliveryUpdate {
					mu.Lock()
					seen = append(seen, value)
					mu.Unlock()
				}
			}); err != nil {
				t.Fatal(err)
			}
			return opened
		}
		graph := observe()
		expectSameJSON(t, graph.Value(), map[string]any{"tasks": map[string]any{}})
		// The advanced value equals a fresh build from Storage once the last observer left.
		rebuild := func() {
			advanced := graphValue(harness, graph)
			graph.Dispose()
			graph = observe()
			if sameGraph(graph.Value(), advanced) {
				t.Fatal("rebuilt graph shares the dropped revision")
			}
			expectSameJSON(t, graph.Value(), advanced)
		}

		rootId := root.Id()
		parent := commitValue(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, parentTask, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &rootId})
		})
		expectSameJSON(t, graphValue(harness, graph).Tasks[taskKey(parent)], map[string]any{
			"id":             float64(parent),
			"kind":           "test.graph-parent",
			"conversationId": float64(root.Id()),
			"background":     false,
			"abortRequested": false,
			"state":          map[string]any{"status": "pending", "phase": "spawn"},
			"conversations":  []any{},
		})

		harness.Resume()
		childId := func() string {
			for _, node := range graphValue(harness, graph).Tasks {
				if node.Kind == "test.graph-child" {
					return taskKey(node.Id)
				}
			}
			return ""
		}
		eventually(t, func() bool {
			value := graphValue(harness, graph)
			return len(value.Tasks) == 2 && graphStatuses(value)["test.graph-child#"+childId()] == "running"
		})
		firstNode := graphValue(harness, graph).Tasks[childId()]
		first := firstNode.Id
		parentNode := graphValue(harness, graph).Tasks[taskKey(parent)]
		expectSameJSON(t, parentNode.State, map[string]any{"status": "waiting", "phase": "join", "on": []any{float64(first)}, "policy": "allSettled"})
		if len(parentNode.Conversations) != 2 || !slices.IsSorted(parentNode.Conversations) {
			t.Fatalf("conversations %v", parentNode.Conversations)
		}
		owned := parentNode.Conversations[0]
		if firstNode.Owner == nil || *firstNode.Owner != parent || firstNode.ConversationId != root.Id() {
			t.Fatalf("first child %+v", firstNode)
		}
		if must(harness.Conversation(testContext, owned)) == nil {
			t.Fatal("owned conversation is absent")
		}
		rebuild()

		childGate.resolve()
		// The parent completes while the late child lives: its outcome is held.
		eventually(t, func() bool {
			node, ok := graphValue(harness, graph).Tasks[taskKey(parent)]
			return ok && node.State.Status == durable.TaskCompleting
		})
		expectSameJSON(t, graphValue(harness, graph).Tasks[taskKey(parent)].State, map[string]any{"status": "completing", "outcome": "completed"})
		if _, present := graphValue(harness, graph).Tasks[taskKey(first)]; present {
			t.Fatal("the first child is still listed")
		}
		// Owned conversations stay listed while the owner lives.
		expectSameJSON(t, graphValue(harness, graph).Tasks[taskKey(parent)].Conversations, parentNode.Conversations)
		// The late child is reserved by a scheduler pass on its own goroutine; upstream's single thread finishes that
		// pass before the next await. Rebuilding while it is still pending would compare against a fresh read of the
		// running child, so wait until it runs.
		eventually(t, func() bool {
			for _, node := range graphValue(harness, graph).Tasks {
				if node.Kind == "test.graph-child" && node.State.Status != durable.TaskRunning {
					return false
				}
			}
			return len(graphValue(harness, graph).Tasks) == 2
		})
		rebuild()

		lateGate.resolve()
		must(harness.WaitForTask(testContext, parent))
		expectSameJSON(t, graphValue(harness, graph), map[string]any{"tasks": map[string]any{}})
		mu.Lock()
		revisions := slices.Clone(seen)
		mu.Unlock()
		// Every revision is one commit that changed a node; none repeats its predecessor.
		for index := 1; index < len(revisions); index++ {
			if reflect.DeepEqual(jsonOf(t, revisions[index]), jsonOf(t, revisions[index-1])) {
				t.Fatalf("revision %d repeats its predecessor", index)
			}
		}
		// The commit that created the two conversations published one revision setting them.
		if !slices.ContainsFunc(revisions, func(value TaskGraph) bool {
			node, ok := value.Tasks[taskKey(parent)]
			return ok && len(node.Conversations) == 2
		}) {
			t.Fatal("no revision lists both owned conversations")
		}
		graph.Dispose()
		mustClose(t, harness)
	})

	t.Run("builds from committed tasks, shows surviving tasks as pending after reopen, and marks aborts", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		gate := deferred()
		type workRuntime = durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, any]
		type workRecord = durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue]
		work := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.graph-work",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "work"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"work": func(ctx context.Context, _ workRecord, runtime workRuntime) error {
					if err := gate.wait(runtime.Signal()); err != nil {
						return err
					}
					return runtime.Commit(ctx, func(durable.Tx, workRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ workRecord, runtime workRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, workRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return abortedWith[stepState, durable.JsonValue]("test"), nil
				})
			},
		})
		openSqlite := func() durable.Storage {
			store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return store
		}
		firstHarness, _, _ := openTasks(t, openSqlite(), []durable.AnyTask{work})
		firstRoot := mustRoot(t, firstHarness, nil)
		type created struct {
			foreground, background durable.TaskId
			owned                  durable.ConversationId
		}
		ids := commitValue(t, firstRoot, func(tx durable.Tx) (created, error) {
			rootId := firstRoot.Id()
			conversationOwned := durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}
			foreground, err := durable.CreateTask(tx, work, nil, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &rootId})
			if err != nil {
				return created{}, err
			}
			background, err := durable.CreateTask(tx, work, nil, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &rootId, Background: true})
			if err != nil {
				return created{}, err
			}
			owned, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: foreground}})
			return created{foreground, background, owned.Id}, err
		})
		foregroundKey, backgroundKey := "test.graph-work#"+taskKey(ids.foreground), "test.graph-work#"+taskKey(ids.background)
		firstHarness.Resume()
		eventually(t, func() bool {
			for _, task := range inspectOf(t, firstHarness).Tasks {
				if task.State.Kind != TaskInspectionRunning {
					return false
				}
			}
			return true
		})
		running := must(firstHarness.TaskGraph(testContext))
		expectSameJSON(t, graphStatuses(running.Value()), map[string]any{foregroundKey: "running", backgroundKey: "running"})
		mustClose(t, firstHarness)

		// Acquired after reopen: built from the committed records and owner edges; open reconciled running to pending.
		harness, _, _ := openTasks(t, openSqlite(), []durable.AnyTask{work})
		watch := must(harness.WatchTaskGraph(testContext))
		expectSameJSON(t, graphStatuses(watch.Value()), map[string]any{foregroundKey: "pending", backgroundKey: "pending"})
		expectSameJSON(t, watch.Value().Tasks[taskKey(ids.foreground)].Conversations, []any{float64(ids.owned)})
		if !watch.Value().Tasks[taskKey(ids.background)].Background {
			t.Fatal("background flag lost")
		}

		// Exact frames: replaying their operations from the acquisition revision gives each delivered value.
		var mu sync.Mutex
		replica := orderedJSONOf(t, watch.Value())
		var frames [][]durable.Op
		var replayErr error
		watch.Start(func(_ context.Context, value TaskGraph, ops []durable.Op) error {
			mu.Lock()
			defer mu.Unlock()
			converted := make([]durable.Op, len(ops))
			for i, op := range orderedJSONOf(t, ops).([]any) {
				converted[i] = op.([]any)
			}
			next, err := delta.ApplyImmutable(replica, converted)
			if err == nil && !reflect.DeepEqual(jsonOf(t, next), jsonOf(t, value)) {
				err = fmt.Errorf("replica %v, delivered %v", next, jsonOf(t, value))
			}
			if err != nil && replayErr == nil {
				replayErr = err
			}
			replica = next
			frames = append(frames, ops)
			return nil
		})
		harness.Resume()
		eventually(t, func() bool {
			harness.(*harnessImpl).WaitDeliveries()
			node, ok := watch.Value().Tasks[taskKey(ids.background)]
			return ok && node.State.Status == durable.TaskRunning
		})
		if marked := must(harness.AbortTask(testContext, ids.background)); marked != "marked" {
			t.Fatalf("abort %s", marked)
		}
		must(harness.WaitForTask(testContext, ids.background))
		harness.(*harnessImpl).WaitDeliveries()
		mu.Lock()
		if replayErr != nil {
			t.Fatal(replayErr)
		}
		recorded := jsonOf(t, frames).([]any)
		finalReplica := replica
		mu.Unlock()
		if !slices.ContainsFunc(recorded, func(frame any) bool {
			ops := frame.([]any)
			if len(ops) != 1 {
				return false
			}
			op := ops[0].([]any)
			node, isNode := op[2].(map[string]any)
			return op[0] == "s" && reflect.DeepEqual(op[1], []any{"tasks", taskKey(ids.background)}) && isNode && node["abortRequested"] == true
		}) {
			t.Fatalf("no frame marks the abort: %v", recorded)
		}
		expectSameJSON(t, recorded[len(recorded)-1], []any{[]any{"d", []any{"tasks", taskKey(ids.background)}}})
		var keys []string
		for key := range finalReplica.(*delta.JsonObject).Value("tasks").(*delta.JsonObject).All() {
			keys = append(keys, key)
		}
		expectStrings(t, keys, []string{taskKey(ids.foreground)})
		must(watch.Stop())
		gate.resolve()
		must(harness.WaitForTask(testContext, ids.foreground))
		mustClose(t, harness)
	})

	t.Run("lists owned conversations in ID order whatever order one commit creates them in", func(t *testing.T) {
		gate := deferred()
		var mu sync.Mutex
		var createdIds []durable.ConversationId
		type spawnInput struct {
			At durable.EntryId `json:"at"`
		}
		type spawnRuntime = durable.TaskRuntime[spawnInput, stepState, durable.JsonValue, any]
		type spawnRecord = durable.RunningTask[spawnInput, stepState, durable.JsonValue]
		spawner := durable.DefineTask(durable.TaskDefinition[spawnInput, stepState, durable.JsonValue, any]{
			Name:    "test.graph-spawner",
			Version: 1,
			Initial: func(spawnInput) stepState { return stepState{Phase: "spawn"} },
			Phases: map[string]durable.PhaseHandler[spawnInput, stepState, durable.JsonValue, any]{
				"spawn": func(ctx context.Context, task spawnRecord, runtime spawnRuntime) error {
					if err := runtime.Commit(ctx, func(tx durable.Tx, _ spawnRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						ownership := durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: task.Id}
						forked, err := tx.ForkConversation(runtime.ConversationId(), task.Input.At, durable.CreateConversationOptions{Ownership: ownership})
						if err != nil {
							return nil, err
						}
						fresh, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownership})
						if err != nil {
							return nil, err
						}
						mu.Lock()
						createdIds = append(createdIds, forked.Id, fresh.Id)
						mu.Unlock()
						return nil, nil
					}); err != nil {
						return err
					}
					if err := gate.wait(ctx); err != nil {
						return err
					}
					return runtime.Commit(ctx, func(durable.Tx, spawnRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(context.Context, spawnRecord, spawnRuntime) error { return nil },
		})
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{spawner})
		root := mustRoot(t, harness, nil)
		graph := must(harness.TaskGraph(testContext))
		id := commitValue(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			entry, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
			if err != nil {
				return 0, err
			}
			rootId := root.Id()
			return durable.CreateTask(tx, spawner, spawnInput{At: entry.Id}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &rootId})
		})
		harness.Resume()
		eventually(t, func() bool { return len(graphValue(harness, graph).Tasks[taskKey(id)].Conversations) == 2 })
		advanced := graphValue(harness, graph)
		mu.Lock()
		want := slices.Sorted(slices.Values(createdIds))
		mu.Unlock()
		expectSameJSON(t, advanced.Tasks[taskKey(id)].Conversations, want)
		graph.Dispose()
		rebuilt := must(harness.TaskGraph(testContext))
		expectSameJSON(t, rebuilt.Value(), advanced)
		rebuilt.Dispose()
		gate.resolve()
		must(harness.WaitForTask(testContext, id))
		mustClose(t, harness)
	})

	t.Run("registers nothing for an acquisition cancelled while it waits for the line", func(t *testing.T) {
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, nil)
		root := mustRoot(t, harness, nil)
		_, childTask := graphFamily(graphGates{child: deferred(), late: deferred()})
		rootId := root.Id()
		commitValue(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, childTask, graphChildInput{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &rootId})
		})
		held := store.holdCommits()
		blocking := asyncErr(func() error {
			_, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
				_, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "blocker"})
				return nil, err
			})
			return err
		})
		_ = held.entered.wait(testContext)
		cancelled, cancel := cancelledContext(errors.New("cancelled"))
		var attaching *future[struct{}]
		queueOnLine(t, harness, func() <-chan struct{} {
			attaching = asyncErr(func() error {
				_, err := harness.WatchTaskGraph(cancelled)
				return err
			})
			return attaching.done
		})
		cancel()
		held.release()
		if _, err := blocking.wait(); err != nil {
			t.Fatal(err)
		}
		_, err := attaching.wait()
		expectError(t, err, "cancelled")
		// No observer kept the mount: each new observer builds a new revision.
		first := must(harness.TaskGraph(testContext))
		value := first.Value()
		first.Dispose()
		second := must(harness.TaskGraph(testContext))
		if sameGraph(second.Value(), value) {
			t.Fatal("a cancelled acquisition kept the mount")
		}
		expectSameJSON(t, second.Value(), value)
		second.Dispose()
		mustClose(t, harness)
	})

	t.Run("publishes no revision for a commit that changes no node, and shares one mount between observers", func(t *testing.T) {
		gate, reached := deferred(), deferred()
		memo := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
			Name:    "test.graph-memo",
			Version: 1,
			Initial: func(durable.JsonValue) stepState { return stepState{Phase: "work"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
				"work": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
					if _, err := runtime.MemoCandidate(ctx, "seen", true); err != nil {
						return err
					}
					reached.resolve()
					if err := gate.wait(ctx); err != nil {
						return err
					}
					return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
						return completed[stepState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(context.Context, stepRecord, stepRuntime) error { return nil },
		})
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{memo})
		root := mustRoot(t, harness, nil)
		rootId := root.Id()
		id := commitValue(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, memo, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &rootId})
		})
		first := must(harness.TaskGraph(testContext))
		second := must(harness.TaskGraph(testContext))
		if !sameGraph(second.Value(), first.Value()) {
			t.Fatal("observers do not share one mount revision")
		}
		var mu sync.Mutex
		var updates []string
		if _, err := first.Subscribe(func(value TaskGraph, _ context.Context, delivery chord.ReplicatedStateDelivery) {
			if delivery.Kind != chord.DeliveryUpdate {
				return
			}
			status := "gone"
			if node, ok := value.Tasks[taskKey(id)]; ok {
				status = string(node.State.Status)
			}
			mu.Lock()
			updates = append(updates, status)
			mu.Unlock()
		}); err != nil {
			t.Fatal(err)
		}
		snapshot := func() []string {
			harness.(*harnessImpl).WaitDeliveries()
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(updates)
		}
		harness.Resume()
		_ = reached.wait(testContext)
		// Reservation changed the node; the memo commit did not.
		expectStrings(t, snapshot(), []string{"running"})
		gate.resolve()
		must(harness.WaitForTask(testContext, id))
		expectStrings(t, snapshot(), []string{"running", "gone"})
		last := first.Value()
		first.Dispose()
		second.Dispose()
		// No observer is left, so the mount was dropped: a new observer builds a new revision.
		rebuilt := must(harness.TaskGraph(testContext))
		expectSameJSON(t, rebuilt.Value(), last)
		if sameGraph(rebuilt.Value(), last) {
			t.Fatal("rebuilt graph shares the dropped revision")
		}
		rebuilt.Dispose()
		mustClose(t, harness)
	})
}
