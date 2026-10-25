// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// harness-task-graph.test.ts through driver.OpenHarness over the real core.

func graphJSON(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func graphStatuses(graph harness.TaskGraph) map[string]any {
	out := map[string]any{}
	for _, node := range graph.Tasks {
		out[fmt.Sprintf("%s#%d", node.Kind, node.Id)] = string(node.State.Status)
	}
	return out
}

func taskKey(id durable.TaskId) string { return strconv.FormatInt(int64(id), 10) }

// harness-task-graph.test.ts "builds from committed tasks, shows surviving tasks as pending after reopen, and marks aborts".
func TestCoreTaskGraphBuildsFromCommittedTasksShowsSurvivorsPendingAfterReopenAndMarksAborts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite")
	gate := make(chan struct{})
	work := defineSteps("test.graph-work", func(ctx context.Context, rt stepRuntime) error {
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return abortedNext("test"), nil })
	}, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			select {
			case <-gate:
			case <-rt.Signal().Done():
				return context.Cause(rt.Signal())
			}
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
		},
	})
	open := func() harness.Harness {
		registry := harness.CreateRegistry()
		if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{work.Erased()}}); err != nil {
			t.Fatal(err)
		}
		h, err := driver.OpenHarness(t.Context(), path, harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close(context.Background()) })
		return h
	}
	first := open()
	firstRoot, err := first.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type created struct {
		foreground, background durable.TaskId
		owned                  durable.ConversationId
	}
	ids, err := durable.Commit(t.Context(), firstRoot, func(tx durable.Tx) (created, error) {
		rootId := firstRoot.Id()
		owned := durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}
		foreground, err := tx.CreateTaskErased(work.Erased(), nil, durable.TaskOptions{Ownership: owned, ConversationId: &rootId})
		if err != nil {
			return created{}, err
		}
		background, err := tx.CreateTaskErased(work.Erased(), nil, durable.TaskOptions{Ownership: owned, ConversationId: &rootId, Background: true})
		if err != nil {
			return created{}, err
		}
		conversation, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: foreground}})
		return created{foreground, background, conversation.Id}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	foregroundKey, backgroundKey := fmt.Sprintf("test.graph-work#%d", ids.foreground), fmt.Sprintf("test.graph-work#%d", ids.background)
	first.Resume()
	pollUntil(t, "both tasks running", func() bool {
		inspection, err := first.Inspect(t.Context())
		if err != nil || len(inspection.Tasks) == 0 {
			return false
		}
		for _, task := range inspection.Tasks {
			if task.State.Kind != harness.TaskInspectionRunning {
				return false
			}
		}
		return true
	})
	running, err := startedTaskGraph(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	if got := graphStatuses(running.Value()); !reflect.DeepEqual(got, map[string]any{foregroundKey: "running", backgroundKey: "running"}) {
		t.Fatalf("running graph %v", got)
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Acquired after reopen: built from the committed records and owner edges; open reconciled running to pending.
	h := open()
	watch, err := h.WatchTaskGraph(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := graphStatuses(watch.Value()); !reflect.DeepEqual(got, map[string]any{foregroundKey: "pending", backgroundKey: "pending"}) {
		t.Fatalf("reopened graph %v", got)
	}
	if got := watch.Value().Tasks[taskKey(ids.foreground)].Conversations; !reflect.DeepEqual(got, []durable.ConversationId{ids.owned}) {
		t.Fatalf("owned conversations %v, want [%d]", got, ids.owned)
	}
	if !watch.Value().Tasks[taskKey(ids.background)].Background {
		t.Fatal("background flag lost")
	}

	// Exact frames: replaying their operations from the acquisition revision gives each delivered value.
	var mu sync.Mutex
	replica := graphJSON(t, watch.Value())
	var frames [][]durable.Op
	var replayErr error
	watch.Start(func(_ context.Context, value harness.TaskGraph, ops []durable.Op) error {
		mu.Lock()
		defer mu.Unlock()
		converted := make([]durable.Op, len(ops))
		for i, op := range graphJSON(t, ops).([]any) {
			converted[i] = op.([]any)
		}
		// delta applies to ordered JSON objects (delta/index.ts walks object members in insertion order); the replica
		// compares as plain JSON.
		var next any
		ordered, err := delta.DecodeJson(mustJSON(replica))
		if err == nil {
			next, err = delta.ApplyImmutable(ordered, converted)
		}
		if err == nil {
			next = graphJSON(t, next)
		}
		if err == nil && !reflect.DeepEqual(next, graphJSON(t, value)) {
			err = fmt.Errorf("replica %v, delivered %v", next, graphJSON(t, value))
		}
		if err != nil && replayErr == nil {
			replayErr = err
		}
		replica = next
		frames = append(frames, ops)
		return nil
	})
	h.Resume()
	pollUntil(t, "the background task running", func() bool {
		node, ok := watch.Value().Tasks[taskKey(ids.background)]
		return ok && node.State.Status == durable.TaskRunning
	})
	if marked, err := h.AbortTask(t.Context(), ids.background); err != nil || marked != "marked" {
		t.Fatalf("abort %q %v", marked, err)
	}
	waitSettled(t, h, ids.background)
	pollUntil(t, "the final frame", func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(frames) == 0 {
			return false
		}
		last := graphJSON(t, frames[len(frames)-1]).([]any)
		return reflect.DeepEqual(last, []any{[]any{"d", []any{"tasks", taskKey(ids.background)}}})
	})
	mu.Lock()
	if replayErr != nil {
		t.Fatal(replayErr)
	}
	recorded := graphJSON(t, frames).([]any)
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
	var keys []string
	for key := range finalReplica.(map[string]any)["tasks"].(map[string]any) {
		keys = append(keys, key)
	}
	if !reflect.DeepEqual(keys, []string{taskKey(ids.foreground)}) {
		t.Fatalf("final replica tasks %v", keys)
	}
	if _, err := watch.Stop(); err != nil {
		t.Fatal(err)
	}
	close(gate)
	waitSettled(t, h, ids.foreground)
}

type spawnInput struct {
	At durable.EntryId `json:"at"`
}

// harness-task-graph.test.ts "lists owned conversations in ID order whatever order one commit creates them in".
func TestCoreTaskGraphListsOwnedConversationsInIDOrder(t *testing.T) {
	gate := make(chan struct{})
	var mu sync.Mutex
	var createdIds []durable.ConversationId
	type spawnRuntime = durable.TaskRuntime[spawnInput, stepState, durable.JsonValue, any]
	type spawnRecord = durable.RunningTask[spawnInput, stepState, durable.JsonValue]
	spawner := durable.DefineTask(durable.TaskDefinition[spawnInput, stepState, durable.JsonValue, any]{
		Name: "test.graph-spawner", Version: 1,
		Initial: func(spawnInput) stepState { return stepState{Phase: "spawn"} },
		Phases: map[string]durable.PhaseHandler[spawnInput, stepState, durable.JsonValue, any]{
			"spawn": func(ctx context.Context, task spawnRecord, rt spawnRuntime) error {
				if err := rt.Commit(ctx, func(tx durable.Tx, _ spawnRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					ownership := durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: task.Id}
					forked, err := tx.ForkConversation(rt.ConversationId(), task.Input.At, durable.CreateConversationOptions{Ownership: ownership})
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
				select {
				case <-gate:
				case <-ctx.Done():
					return context.Cause(ctx)
				}
				return rt.Commit(ctx, func(durable.Tx, spawnRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completedWith(nil), nil
				})
			},
		},
		Abort: func(context.Context, spawnRecord, spawnRuntime) error { return nil },
	})
	h, root := openTasksHarness(t, spawner.Erased())
	graph, err := startedTaskGraph(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	rootId := root.Id()
	id, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.TaskId, error) {
		entry, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		if err != nil {
			return 0, err
		}
		return tx.CreateTaskErased(spawner.Erased(), spawnInput{At: entry.Id}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &rootId})
	})
	if err != nil {
		t.Fatal(err)
	}
	h.Resume()
	pollUntil(t, "both owned conversations listed", func() bool { return len(graph.Value().Tasks[taskKey(id)].Conversations) == 2 })
	advanced := graph.Value()
	mu.Lock()
	want := slices.Sorted(slices.Values(createdIds))
	mu.Unlock()
	if got := advanced.Tasks[taskKey(id)].Conversations; !reflect.DeepEqual(got, want) {
		t.Fatalf("owned conversations %v, want %v (ID order)", got, want)
	}
	_, _ = graph.Stop()
	rebuilt, err := startedTaskGraph(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(graphJSON(t, rebuilt.Value()), graphJSON(t, advanced)) {
		t.Fatalf("rebuilt graph %v, advanced %v", graphJSON(t, rebuilt.Value()), graphJSON(t, advanced))
	}
	_, _ = rebuilt.Stop()
	close(gate)
	waitSettled(t, h, id)
}

// startedTaskGraph opens a watch of the task graph and starts it with a listener that ignores its frames: a started watch
// keeps its value current (harness-task-graph.test.ts, harness-blocked.test.ts).
func startedTaskGraph(ctx context.Context, opener interface {
	WatchTaskGraph(context.Context) (harness.TaskGraphWatch, error)
}) (harness.TaskGraphWatch, error) {
	watch, err := opener.WatchTaskGraph(ctx)
	if err != nil {
		return nil, err
	}
	watch.Start(func(context.Context, harness.TaskGraph, []durable.Op) error { return nil })
	return watch, nil
}
