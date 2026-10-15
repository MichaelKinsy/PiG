package harness

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 1.2.0 test/harness-scheduler-indexes.test.ts "TaskRuntime.ownedTasks": the live tasks a task owns directly, in ID order,
// also those an earlier invocation created.

type probeInput struct {
	Name string `json:"name"`
}

type probeState struct {
	Phase string `json:"phase"`
}

type probeRecord = durable.RunningTask[probeInput, probeState, durable.JsonValue]
type probeRuntime = durable.TaskRuntime[probeInput, probeState, durable.JsonValue, any]

// ownedProbe records the IDs ownedTasks returns to each run of a probe once its look gate opens, then holds until its own gate
// opens.
type ownedProbe struct {
	mu       sync.Mutex
	owned    map[string][]durable.TaskId
	runtimes map[string]probeRuntime
	task     durable.Task[probeInput, probeState, durable.JsonValue, any]
}

func newOwnedProbe(fixtures *ownFixtures) *ownedProbe {
	probe := &ownedProbe{owned: map[string][]durable.TaskId{}, runtimes: map[string]probeRuntime{}}
	probe.task = durable.DefineTask(durable.TaskDefinition[probeInput, probeState, durable.JsonValue, any]{
		Name:    "test.index.probe",
		Version: 1,
		Initial: func(probeInput) probeState { return probeState{Phase: "look"} },
		Phases: map[string]durable.PhaseHandler[probeInput, probeState, durable.JsonValue, any]{
			"look": func(ctx context.Context, task probeRecord, runtime probeRuntime) error {
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
				return runtime.Commit(ctx, func(durable.Tx, probeRecord) (*durable.NextTaskState[probeState, durable.JsonValue], error) {
					return completed[probeState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(ctx context.Context, _ probeRecord, runtime probeRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, probeRecord) (*durable.NextTaskState[probeState, durable.JsonValue], error) {
				return abortedWith[probeState, durable.JsonValue](""), nil
			})
		},
	})
	return probe
}

func (probe *ownedProbe) ownedBy(name string) ([]durable.TaskId, bool) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	ids, ok := probe.owned[name]
	return ids, ok
}

func TestOwnedTasksListsTheLiveTasksATaskOwnsDirectlyInIdOrderAlsoAfterAReopen(t *testing.T) {
	// harness-scheduler-indexes.test.ts:203 it("lists the live tasks a task owns directly, in ID order, also after a reopen")
	t.Run("lists the live tasks a task owns directly, in ID order, also after a reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		fixtures := newOwnFixtures()
		probe := newOwnedProbe(fixtures)
		harness, _, _ := openTasks(t, openSqlite(t, path), []durable.AnyTask{fixtures.hold.Erased(), probe.task.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		// The probe owns: two held tasks, one that ends, a grandchild below the first, and a conversation with a task. The first
		// ends too, but holds its outcome as completing while its grandchild runs: still live, so listed.
		type created struct{ probe, first, ends, second durable.TaskId }
		ids := must(durable.Commit(testContext, root, func(tx durable.Tx) (created, error) {
			var c created
			var err error
			if c.probe, err = durable.CreateTask(tx, probe.task, probeInput{Name: "probe"}, durable.TaskOptions{Ownership: conversationOwned}); err != nil {
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
			_, err = durable.CreateTask(tx, fixtures.hold, holdInput{Name: "inside"}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &child.Id})
			return c, err
		}))
		fixtures.open("ends", "completed")
		must(harness.WaitForTask(testContext, ids.ends))
		fixtures.open("first", "completed")
		eventually(t, func() bool {
			record := must(harness.GetTask(testContext, ids.first))
			return record != nil && record.State.Status == durable.TaskCompleting
		})
		fixtures.open("look.probe", "completed")
		eventually(t, func() bool { _, ok := probe.ownedBy("probe"); return ok })
		if got, _ := probe.ownedBy("probe"); !slices.Equal(got, []durable.TaskId{ids.first, ids.second}) {
			t.Fatalf("ownedTasks = %v, want [%d %d]: the live tasks the probe owns directly, in ID order", got, ids.first, ids.second)
		}
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}

		// After a reopen the rerun sees the same tasks, which an earlier invocation created.
		fixtures = newOwnFixtures()
		probe = newOwnedProbe(fixtures)
		harness, _, _ = openTasks(t, openSqlite(t, path), []durable.AnyTask{fixtures.hold.Erased(), probe.task.Erased()})
		harness.Resume()
		fixtures.open("look.probe", "completed")
		eventually(t, func() bool { _, ok := probe.ownedBy("probe"); return ok })
		if got, _ := probe.ownedBy("probe"); !slices.Equal(got, []durable.TaskId{ids.first, ids.second}) {
			t.Fatalf("ownedTasks after a reopen = %v, want [%d %d]", got, ids.first, ids.second)
		}
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOwnedTasksRejectsOnceItsInvocationHasEnded(t *testing.T) {
	// harness-scheduler-indexes.test.ts:244 it("rejects once its invocation has ended")
	t.Run("rejects once its invocation has ended", func(t *testing.T) {
		fixtures := newOwnFixtures()
		probe := newOwnedProbe(fixtures)
		harness, _, _ := openTasks(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")), []durable.AnyTask{fixtures.hold.Erased(), probe.task.Erased()})
		root := mustRoot(t, harness, nil)
		harness.Resume()
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, probe.task, probeInput{Name: "probe"}, durable.TaskOptions{Ownership: conversationOwned})
		}))
		var runtime probeRuntime
		eventually(t, func() bool {
			probe.mu.Lock()
			defer probe.mu.Unlock()
			runtime = probe.runtimes["probe"]
			return runtime != nil
		})
		// Aborting joins the run invocation, which has ended by then.
		if _, err := harness.AbortTask(testContext, id); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.OwnedTasks(testContext); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
			t.Fatalf("OwnedTasks after the invocation ended = %v, want an invocation has ended error", err)
		}
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
	})
}
