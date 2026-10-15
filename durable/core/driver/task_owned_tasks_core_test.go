// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Pi 4ac0bd8c/e37cdc42 test/harness-scheduler-indexes.test.ts "TaskRuntime.ownedTasks" over the real core: the driver's
// TaskRuntime.OwnedTasks asks the core's ownedTasks api op, which lists the live tasks the invoking task owns directly, in ID
// order, also those an earlier invocation created (scheduler.ts ownedTasks).

// ownedCoreProbe records the IDs OwnedTasks returns to each run of a probe once its look gate opens, then holds until its own
// gate opens.
type ownedCoreProbe struct {
	mu       sync.Mutex
	owned    map[string][]durable.TaskId
	runtimes map[string]holdRuntime
	task     durable.Task[holdInput, stepState, durable.JsonValue, any]
}

func newOwnedCoreProbe(fixtures *ownFixtures) *ownedCoreProbe {
	probe := &ownedCoreProbe{owned: map[string][]durable.TaskId{}, runtimes: map[string]holdRuntime{}}
	probe.task = durable.DefineTask(durable.TaskDefinition[holdInput, stepState, durable.JsonValue, any]{
		Name:    "test.index.probe",
		Version: 1,
		Initial: func(holdInput) stepState { return stepState{Phase: "look"} },
		Phases: map[string]durable.PhaseHandler[holdInput, stepState, durable.JsonValue, any]{
			"look": func(ctx context.Context, task holdRecord, runtime holdRuntime) error {
				probe.mu.Lock()
				probe.runtimes[task.Input.Name] = runtime
				probe.mu.Unlock()
				select {
				case <-fixtures.gate("look." + task.Input.Name).done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
				records, err := runtime.OwnedTasks(ctx)
				if err != nil {
					return err
				}
				ids := make([]durable.TaskId, 0, len(records))
				for _, record := range records {
					ids = append(ids, record.Id)
				}
				probe.mu.Lock()
				probe.owned[task.Input.Name] = ids
				probe.mu.Unlock()
				select {
				case <-fixtures.gate(task.Input.Name).done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
				return runtime.Commit(ctx, func(durable.Tx, holdRecord) (*holdNext, error) {
					return &holdNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ holdRecord, runtime holdRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, holdRecord) (*holdNext, error) { return abortedOutcome(), nil })
		},
	})
	return probe
}

func (probe *ownedCoreProbe) ownedBy(name string) ([]durable.TaskId, bool) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	ids, ok := probe.owned[name]
	return ids, ok
}

func openOwnedCoreHarness(t *testing.T, path string, tasks ...durable.AnyTask) harness.Harness {
	t.Helper()
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: tasks}); err != nil {
		t.Fatal(err)
	}
	h, err := driver.OpenHarness(t.Context(), path, harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

func TestCoreOwnedTasksListsTheLiveTasksATaskOwnsDirectlyInIdOrderAlsoAfterAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	fixtures := newOwnFixtures()
	probe := newOwnedCoreProbe(fixtures)
	h := openOwnedCoreHarness(t, path, fixtures.hold.Erased(), probe.task.Erased())
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Resume()
	// The probe owns: two held tasks, one that ends, a grandchild below the first, and a conversation with a task. The first
	// ends too, but holds its outcome as completing while its grandchild runs: still live, so listed.
	type created struct{ probe, first, ends, second durable.TaskId }
	ids, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (created, error) {
		var c created
		var err error
		if c.probe, err = durable.CreateTask(tx, probe.task, holdInput{Name: "probe"}, durable.TaskOptions{Ownership: ownedByConversation}); err != nil {
			return c, err
		}
		owned := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: c.probe}}
		if c.first, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "first"}, owned); err != nil {
			return c, err
		}
		if c.ends, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "ends"}, owned); err != nil {
			return c, err
		}
		if c.second, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "second"}, owned); err != nil {
			return c, err
		}
		if _, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "grandchild"}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: c.first}}); err != nil {
			return c, err
		}
		child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: c.probe}})
		if err != nil {
			return c, err
		}
		_, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "inside"}, durable.TaskOptions{Ownership: ownedByConversation, ConversationId: &child.Id})
		return c, err
	})
	if err != nil {
		t.Fatal(err)
	}
	fixtures.open("ends", "completed")
	fixtures.open("first", "completed")
	waitSettled(t, h, ids.ends)
	pollUntil(t, "the first task holds its outcome as completing", func() bool {
		record, err := h.GetTask(t.Context(), ids.first)
		return err == nil && record != nil && record.State.Status == durable.TaskCompleting
	})
	fixtures.open("look.probe", "completed")
	pollUntil(t, "the probe listed its tasks", func() bool { _, ok := probe.ownedBy("probe"); return ok })
	if got, _ := probe.ownedBy("probe"); !slices.Equal(got, []durable.TaskId{ids.first, ids.second}) {
		t.Fatalf("ownedTasks = %v, want [%d %d]: the live tasks the probe owns directly, in ID order", got, ids.first, ids.second)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// After a reopen the rerun sees the same tasks, which an earlier invocation created.
	fixtures = newOwnFixtures()
	probe = newOwnedCoreProbe(fixtures)
	h = openOwnedCoreHarness(t, path, fixtures.hold.Erased(), probe.task.Erased())
	h.Resume()
	fixtures.open("look.probe", "completed")
	pollUntil(t, "the rerun probe listed its tasks", func() bool { _, ok := probe.ownedBy("probe"); return ok })
	if got, _ := probe.ownedBy("probe"); !slices.Equal(got, []durable.TaskId{ids.first, ids.second}) {
		t.Fatalf("ownedTasks after a reopen = %v, want [%d %d]", got, ids.first, ids.second)
	}
}

func TestCoreOwnedTasksRejectsOnceItsInvocationHasEnded(t *testing.T) {
	fixtures := newOwnFixtures()
	probe := newOwnedCoreProbe(fixtures)
	h, root := openTasksHarness(t, fixtures.hold.Erased(), probe.task.Erased())
	h.Resume()
	id, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, probe.task, holdInput{Name: "probe"}, durable.TaskOptions{Ownership: ownedByConversation})
	})
	if err != nil {
		t.Fatal(err)
	}
	var runtime holdRuntime
	pollUntil(t, "the probe runs", func() bool {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		runtime = probe.runtimes["probe"]
		return runtime != nil
	})
	// Aborting joins the run invocation, which has ended by then.
	if _, err := h.AbortTask(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.OwnedTasks(t.Context()); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
		t.Fatalf("OwnedTasks after the invocation ended = %v, want an invocation has ended error", err)
	}
}
