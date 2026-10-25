// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Ports packages/durable/test/harness-blocked.test.ts (Pi 482c1381a0) over both engines: the crash-loop guard, the blocked
// reasons of the task graph, and the wake of a registry change. The two cases that hold the Session line or fail one commit
// (:102 and :222) run on the reference Harness only, in durable/harness (harness_blocked_test.go). Every case here runs on the reference Harness and on the core with
// Pi's expectations. A crash is a Harness whose process died: a copy of the store file as it stood, opened by the next Harness
// while the first is never closed, so no write-back of the stopped tasks reaches it (the test's `kill()` of Pi).

// reportLog collects the report of OnReport with its info, in order.
type infoLog struct {
	mu    sync.Mutex
	items []reported
}

type reported struct {
	err  error
	info durable.ReportInfo
}

func (log *infoLog) add(err error, info durable.ReportInfo) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.items = append(log.items, reported{err, info})
}

func (log *infoLog) all() []reported {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]reported(nil), log.items...)
}

func (log *infoLog) infos() []durable.ReportInfo {
	var out []durable.ReportInfo
	for _, item := range log.all() {
		out = append(out, item.info)
	}
	return out
}

// crashRig opens the same store file again and again, one Harness at a time, on one engine.
type crashRig struct {
	t        *testing.T
	core     bool
	path     string
	registry harness.Registry
	// settings is the host's live settings value. The Harness reads it at each use from its own goroutines, so a change replaces the
	// whole value (Pi assigns a field of the shared object, `settings.restartLimit = ...`, which on one thread races nothing).
	settings atomic.Pointer[harness.HarnessSettings]
	crashes  int
}

type opened struct {
	h     harness.Harness
	wakes *wakeLog
	infos *infoLog
}

func newCrashRig(t *testing.T, core bool, registry harness.Registry, settings harness.HarnessSettings) *crashRig {
	t.Helper()
	rig := &crashRig{t: t, core: core, path: filepath.Join(t.TempDir(), "session.sqlite"), registry: registry}
	rig.settings.Store(&settings)
	return rig
}

// kill is Pi's kill(): the process dies. The next open reads the store as it stood, and the dead Harness is never closed against
// it: its later writes, such as the pending records a close writes for the tasks it stops, go to the file the rig left behind.
func (rig *crashRig) kill() {
	rig.t.Helper()
	rig.crashes++
	next := filepath.Join(filepath.Dir(rig.path), "crash"+strconv.Itoa(rig.crashes)+".sqlite")
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(rig.path + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			rig.t.Fatal(err)
		}
		if err := os.WriteFile(next+suffix, data, 0o600); err != nil {
			rig.t.Fatal(err)
		}
	}
	rig.path = next
}

// open opens a Harness over the store. The reference engine's storage wraps the SQLite file; the core owns its own connection.
func (rig *crashRig) open(configure ...func(*harness.HarnessOptions)) opened {
	rig.t.Helper()
	wakes, infos := &wakeLog{}, &infoLog{}
	options := harness.HarnessOptions{
		Models:   ai.CreateModels(),
		Registry: rig.registry,
		Settings: rig.settings.Load,
		OnWake:   wakes.add,
		OnReport: infos.add,
	}
	for _, apply := range configure {
		apply(&options)
	}
	var h harness.Harness
	var err error
	if rig.core {
		h, err = driver.OpenHarness(rig.t.Context(), rig.path, options)
	} else {
		var store durable.Storage
		if store, err = sqlitenode.OpenNodeSqliteStorage(rig.path, sqlitenode.NodeSqliteStorageOptions{}); err != nil {
			rig.t.Fatal(err)
		}
		h, err = harness.OpenHarness(rig.t.Context(), store, options)
	}
	if err != nil {
		rig.t.Fatal(err)
	}
	rig.t.Cleanup(func() { _ = h.Close(context.Background()) })
	return opened{h: h, wakes: wakes, infos: infos}
}

// crashLoop is a task that hangs in its phase while crashing, as one killed every time would, and completes otherwise.
type crashLoopState struct {
	mu       sync.Mutex
	crashing bool
	started  int
}

func (s *crashLoopState) set(crashing bool) { s.mu.Lock(); s.crashing = crashing; s.mu.Unlock() }
func (s *crashLoopState) get() (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.crashing, s.started
}
func (s *crashLoopState) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started++
	return s.crashing
}

type loopState struct {
	Phase string `json:"phase"`
}

type (
	loopRuntime = durable.TaskRuntime[durable.JsonValue, loopState, durable.JsonValue, any]
	loopRecord  = durable.RunningTask[durable.JsonValue, loopState, durable.JsonValue]
	loopNext    = durable.NextTaskState[loopState, durable.JsonValue]
)

func crashLoopTask(state *crashLoopState, abort func(context.Context, loopRecord, loopRuntime) error) durable.Task[durable.JsonValue, loopState, durable.JsonValue, any] {
	if abort == nil {
		abort = func(context.Context, loopRecord, loopRuntime) error { return nil }
	}
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
		Name: "test.crash-loop", Version: 1,
		Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ loopRecord, runtime loopRuntime) error {
				if state.begin() {
					<-runtime.Signal().Done()
					return nil
				}
				return runtime.Commit(ctx, func(durable.Tx, loopRecord) (*loopNext, error) { return loopCompleted(nil), nil })
			},
		},
		Abort: abort,
	})
}

var errCannotMigrate = errors.New("cannot migrate")

// waitFinished2 is the receipt of a task, waiting for it (Harness.WaitForTask resumes scheduling).
func waitFinished2(t *testing.T, h harness.Harness, id durable.TaskId) durable.SettledTask[durable.JsonValue] {
	t.Helper()
	return waitSettled(t, h, id)
}

func mustGet(object durable.JsonObject, key string) any {
	value, _ := object.Get(key)
	return value
}

func loopCompleted(result durable.JsonValue) *loopNext {
	return &loopNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}
}

func installTasks(t *testing.T, registry harness.Registry, name string, tasks ...durable.AnyTask) {
	t.Helper()
	if err := registry.Install(&durable.Extension{Name: name, Tasks: tasks}); err != nil {
		t.Fatal(err)
	}
}

func rootOf(t *testing.T, h harness.Harness) harness.Conversation {
	t.Helper()
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func startOwned(t *testing.T, conversation harness.Conversation, task durable.AnyTask, options ...durable.TaskOptions) durable.TaskId {
	t.Helper()
	option := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}}
	if len(options) > 0 {
		option = options[0]
	}
	id, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (durable.TaskId, error) {
		return tx.CreateTaskErased(task, nil, option)
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func taskOf(t *testing.T, h harness.Harness, id durable.TaskId) *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	t.Helper()
	record, err := h.GetTask(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func graphNode(graph harness.TaskGraphWatch, id durable.TaskId) (harness.TaskGraphNode, bool) {
	node, ok := graph.Value().Tasks[strconv.FormatInt(int64(id), 10)]
	return node, ok
}

func TestBlockedTasks(t *testing.T) {
	// harness-blocked.test.ts:156 "parks a task cut off restartLimit times in a row, shows why, and runs it again on retryTask()"
	t.Run("parks a task cut off restartLimit times in a row, shows why, and runs it again on retryTask()", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			state := &crashLoopState{crashing: true}
			registry := harness.CreateRegistry()
			installTasks(t, registry, "loop", crashLoopTask(state, nil).Erased())
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{RestartLimit: new(3.0)})
			current := rig.open()
			root := rootOf(t, current.h)
			id := startOwned(t, root, crashLoopTask(state, nil).Erased())
			// Each open runs the phase, and each crash cuts it off: three restarts.
			for run := 1; run <= 3; run++ {
				current.h.Resume()
				pollUntil(t, "the phase started", func() bool { _, started := state.get(); return started == run })
				rig.kill() // a crash: a close would not count
				current = rig.open()
			}
			h := current.h
			if record := taskOf(t, h, id); record == nil || record.Restarts != 3 {
				t.Fatalf("restarts %+v, want 3", record)
			}
			want := []durable.ReportInfo{{Type: durable.ReportTaskParked, Task: &durable.ReportTask{Id: id, Kind: "test.crash-loop", ConversationId: root.Id()}, Extension: "loop"}}
			if got := current.infos.infos(); !reflect.DeepEqual(got, want) {
				t.Fatalf("report infos %+v, want %+v", got, want)
			}
			inspection, err := h.Inspect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var inspected harness.TaskInspectionState
			for _, task := range inspection.Tasks {
				if task.Record.Id == id {
					inspected = task.State
				}
			}
			if inspected.Kind != harness.TaskInspectionBlocked || inspected.Reason != "restart_limit" {
				t.Fatalf("inspection %+v, want blocked: restart_limit", inspected)
			}
			graph, err := startedTaskGraph(t.Context(), h)
			if err != nil {
				t.Fatal(err)
			}
			if node, ok := graphNode(graph, id); !ok || node.Blocked != "restart_limit" {
				t.Fatalf("graph node %+v, want blocked: restart_limit", node)
			}
			// Parked work needs no process: nothing is active.
			pollUntil(t, "the wake idle", func() bool { return !h.Wake().Active })
			expectWake(t, h.Wake(), false)
			h.Resume()
			// A scheduling pass runs within a few turns; it leaves the parked task out.
			settleScheduling(t, h)
			if _, started := state.get(); started != 3 {
				t.Fatalf("the parked task started again: %d starts, want 3", started)
			}

			state.set(false)
			if err := h.RetryTask(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if outcome := waitFinished2(t, h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome %+v, want completed", outcome)
			}
			if _, started := state.get(); started != 4 {
				t.Fatalf("starts %d, want 4", started)
			}
			_, _ = graph.Stop()
		})
	})

	// harness-blocked.test.ts:200 "counts no restart for a task a close stopped"
	t.Run("counts no restart for a task a close stopped", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			state := &crashLoopState{crashing: true}
			registry := harness.CreateRegistry()
			installTasks(t, registry, "loop", crashLoopTask(state, nil).Erased())
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{RestartLimit: new(1.0)})
			current := rig.open()
			id := startOwned(t, rootOf(t, current.h), crashLoopTask(state, nil).Erased())
			current.h.Resume()
			pollUntil(t, "the phase started", func() bool { _, started := state.get(); return started == 1 })
			if err := current.h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			current = rig.open()
			record := taskOf(t, current.h, id)
			if record == nil || record.State.Status != durable.TaskPending || record.Restarts != 0 {
				t.Fatalf("task %+v, want pending without a restart count", record)
			}
			state.set(false)
			if outcome := waitFinished2(t, current.h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome %+v, want completed", outcome)
			}
		})
	})

	// harness-blocked.test.ts:249 "faults a task whose abort handler is cut off restartLimit times"
	t.Run("faults a task whose abort handler is cut off restartLimit times", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			var aborts atomic.Int32
			stuck := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.stuck-abort", Version: 1,
				Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
					"run": func(_ context.Context, _ loopRecord, runtime loopRuntime) error {
						<-runtime.Signal().Done()
						return nil
					},
				},
				Abort: func(_ context.Context, _ loopRecord, runtime loopRuntime) error {
					aborts.Add(1)
					<-runtime.Signal().Done()
					return nil
				},
			})
			registry := harness.CreateRegistry()
			installTasks(t, registry, "stuck", stuck.Erased())
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{RestartLimit: new(1.0)})
			current := rig.open()
			id := startOwned(t, rootOf(t, current.h), stuck.Erased())
			current.h.Resume()
			go func() { _, _ = current.h.AbortTask(context.Background(), id) }()
			pollUntil(t, "the abort handler started", func() bool { return aborts.Load() == 1 })
			rig.kill() // a crash in the abort handler
			current = rig.open()
			settled := waitFinished2(t, current.h, id)
			outcome := settled.State.Outcome
			if outcome == nil || outcome.Status != durable.OutcomeFaulted || outcome.Error == nil {
				t.Fatalf("outcome %+v, want faulted", outcome)
			}
			if detail, ok := outcome.Error.Detail.(durable.JsonObject); !ok || mustGet(detail, "reason") != "restart_limit" {
				t.Fatalf("outcome detail %+v, want reason restart_limit", outcome.Error.Detail)
			}
			// The receipt keeps no restart count.
			if settled.Restarts != 0 {
				t.Fatalf("receipt restarts %d, want none", settled.Restarts)
			}
			if aborts.Load() != 1 {
				t.Fatalf("abort handler ran %d times, want once", aborts.Load())
			}
			pollUntil(t, "the report", func() bool { return len(current.infos.all()) > 0 })
			infos := current.infos.infos()
			if len(infos) != 1 || infos[0].Type != durable.ReportTaskFaulted || infos[0].Task == nil || infos[0].Task.Id != id || infos[0].Reason != "restart_limit" {
				t.Fatalf("report infos %+v, want one task_faulted of the task with reason restart_limit", infos)
			}
		})
	})

	// harness-blocked.test.ts:290 "runs a parked task once a raised restartLimit no longer parks it"
	t.Run("runs a parked task once a raised restartLimit no longer parks it", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			state := &crashLoopState{crashing: true}
			registry := harness.CreateRegistry()
			installTasks(t, registry, "loop", crashLoopTask(state, nil).Erased())
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{RestartLimit: new(1.0)})
			current := rig.open()
			id := startOwned(t, rootOf(t, current.h), crashLoopTask(state, nil).Erased())
			current.h.Resume()
			pollUntil(t, "the phase started", func() bool { _, started := state.get(); return started == 1 })
			rig.kill() // a crash: a close would not count
			current = rig.open()
			graph, err := startedTaskGraph(t.Context(), current.h)
			if err != nil {
				t.Fatal(err)
			}
			current.h.Resume()
			pollUntil(t, "the graph shows the park", func() bool { node, ok := graphNode(graph, id); return ok && node.Blocked == "restart_limit" })
			// Let a scheduling pass leave the parked task out.
			settleScheduling(t, current.h)
			state.set(false)
			// The host's live settings change; the next pass runs the task.
			rig.settings.Store(&harness.HarnessSettings{RestartLimit: new(math.Inf(1))})
			current.h.Resume()
			if outcome := waitFinished2(t, current.h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome %+v, want completed", outcome)
			}
			if _, started := state.get(); started != 2 {
				t.Fatalf("starts %d, want 2", started)
			}
			_, _ = graph.Stop()
		})
	})

	// harness-blocked.test.ts:321 "still runs the abort handler of a parked task"
	t.Run("still runs the abort handler of a parked task", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			state := &crashLoopState{crashing: true}
			var mu sync.Mutex
			var aborted []durable.TaskId
			abort := func(ctx context.Context, task loopRecord, runtime loopRuntime) error {
				mu.Lock()
				aborted = append(aborted, task.Id)
				mu.Unlock()
				return runtime.Commit(ctx, func(durable.Tx, loopRecord) (*loopNext, error) {
					reason := ""
					return &loopNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted, Reason: &reason}}, nil
				})
			}
			loop := crashLoopTask(state, abort)
			registry := harness.CreateRegistry()
			installTasks(t, registry, "loop", loop.Erased())
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{RestartLimit: new(1.0)})
			current := rig.open()
			id := startOwned(t, rootOf(t, current.h), loop.Erased())
			current.h.Resume()
			pollUntil(t, "the phase started", func() bool { _, started := state.get(); return started == 1 })
			rig.kill() // a crash: a close would not count
			current = rig.open()
			if _, err := current.h.AbortTask(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if outcome := waitFinished2(t, current.h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeAborted {
				t.Fatalf("outcome %+v, want aborted", outcome)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(aborted, []durable.TaskId{id}) {
				t.Fatalf("abort handler ran for %v, want [%d]", aborted, id)
			}
			if _, started := state.get(); started != 1 {
				t.Fatalf("starts %d, want 1", started)
			}
		})
	})

	// harness-blocked.test.ts:350 "does not count the same checkpoint committed again as progress"
	t.Run("does not count the same checkpoint committed again as progress", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			var started atomic.Int32
			intent := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.intent", Version: 1,
				Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
					"run": func(ctx context.Context, task loopRecord, runtime loopRuntime) error {
						started.Add(1)
						// Intent before an effect, then the effect never returns: the checkpoint is unchanged.
						if err := runtime.Commit(ctx, func(durable.Tx, loopRecord) (*loopNext, error) {
							return &loopNext{Status: durable.TaskRunning, Checkpoint: &loopState{Phase: "run"}}, nil
						}); err != nil {
							return err
						}
						<-runtime.Signal().Done()
						return nil
					},
				},
				Abort: func(context.Context, loopRecord, loopRuntime) error { return nil },
			})
			registry := harness.CreateRegistry()
			installTasks(t, registry, "intent", intent.Erased())
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{RestartLimit: new(2.0)})
			current := rig.open()
			id := startOwned(t, rootOf(t, current.h), intent.Erased())
			for run := int32(1); run <= 2; run++ {
				current.h.Resume()
				pollUntil(t, "the phase started", func() bool { return started.Load() == run })
				pollUntil(t, "the task running", func() bool {
					record, err := current.h.GetTask(t.Context(), id)
					return err == nil && record != nil && record.State.Status == durable.TaskRunning
				})
				rig.kill() // a crash: a close would not count
				current = rig.open()
			}
			if record := taskOf(t, current.h, id); record == nil || record.Restarts != 2 {
				t.Fatalf("restarts %+v, want 2", record)
			}
			inspection, err := current.h.Inspect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if state := inspection.Tasks[0].State; state.Kind != harness.TaskInspectionBlocked || state.Reason != "restart_limit" {
				t.Fatalf("inspection %+v, want blocked: restart_limit", state)
			}
		})
	})

	// harness-blocked.test.ts:389 "starts the count over when the task makes progress"
	t.Run("starts the count over when the task makes progress", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			var crashing atomic.Bool
			crashing.Store(true)
			var started atomic.Int32
			steps := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.steps", Version: 1,
				Initial: func(durable.JsonValue) loopState { return loopState{Phase: "one"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
					"one": func(ctx context.Context, _ loopRecord, runtime loopRuntime) error {
						started.Add(1)
						if crashing.Load() {
							<-runtime.Signal().Done()
							return nil
						}
						return runtime.Commit(ctx, func(durable.Tx, loopRecord) (*loopNext, error) {
							return &loopNext{Status: durable.TaskRunning, Checkpoint: &loopState{Phase: "two"}}, nil
						})
					},
					"two": func(_ context.Context, _ loopRecord, runtime loopRuntime) error {
						started.Add(1)
						<-runtime.Signal().Done()
						return nil
					},
				},
				Abort: func(context.Context, loopRecord, loopRuntime) error { return nil },
			})
			registry := harness.CreateRegistry()
			installTasks(t, registry, "steps", steps.Erased())
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{RestartLimit: new(2.0)})
			current := rig.open()
			id := startOwned(t, rootOf(t, current.h), steps.Erased())
			current.h.Resume()
			pollUntil(t, "the phase started", func() bool { return started.Load() == 1 })
			rig.kill() // a crash: a close would not count
			current = rig.open()
			if record := taskOf(t, current.h, id); record == nil || record.Restarts != 1 {
				t.Fatalf("restarts %+v, want 1", record)
			}
			// Progress: phase one commits phase two, which resets the count.
			crashing.Store(false)
			current.h.Resume()
			pollUntil(t, "both phases started", func() bool { return started.Load() == 3 })
			pollUntil(t, "the count reset", func() bool { record := taskOf(t, current.h, id); return record != nil && record.Restarts == 0 })
		})
	})

	// harness-blocked.test.ts:431 "tells the graph and the host when a migration fails"
	t.Run("tells the graph and the host when a migration fails", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			run := map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
				"run": func(context.Context, loopRecord, loopRuntime) error { return nil },
			}
			abort := func(context.Context, loopRecord, loopRuntime) error { return nil }
			v1 := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.migrating", Version: 1, Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} }, Phases: run, Abort: abort,
			})
			v2 := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.migrating", Version: 2, Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} }, Phases: run, Abort: abort,
				Migrate: func(durable.JsonValue, durable.JsonValue, int) (durable.JsonValue, loopState, error) {
					return nil, loopState{}, errCannotMigrate
				},
			})
			first := harness.CreateRegistry()
			installTasks(t, first, "v1", v1.Erased())
			rig := newCrashRig(t, core, first, harness.HarnessSettings{})
			current := rig.open()
			// Created, never run: scheduling stays off.
			id := startOwned(t, rootOf(t, current.h), v1.Erased())
			if err := current.h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			second := harness.CreateRegistry()
			installTasks(t, second, "v2", v2.Erased())
			rig.registry = second
			current = rig.open()
			graph, err := startedTaskGraph(t.Context(), current.h)
			if err != nil {
				t.Fatal(err)
			}
			current.h.Resume()
			pollUntil(t, "the graph shows the failed migration", func() bool { node, ok := graphNode(graph, id); return ok && node.Blocked == "migration_failed" })
			pollUntil(t, "the wake idle", func() bool { return !current.h.Wake().Active })
			expectWake(t, current.h.Wake(), false)
			pollUntil(t, "the host told it is idle", func() bool { last, ok := current.wakes.last(); return ok && !last.Active })
			_, _ = graph.Stop()
		})
	})

	// harness-blocked.test.ts:471 "shows an abandoned task waiting for its definition as blocked"
	t.Run("shows an abandoned task waiting for its definition as blocked", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			abandoned := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.abandoned", Version: 1,
				Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
					"run": func(context.Context, loopRecord, loopRuntime) error { return nil },
				},
				Abort: func(context.Context, loopRecord, loopRuntime) error { return nil },
			})
			registry := harness.CreateRegistry()
			installTasks(t, registry, "abandoned", abandoned.Erased())
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{})
			current := rig.open()
			id := startOwned(t, rootOf(t, current.h), abandoned.Erased(), durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, AbandonOnRestart: new(true)})
			if err := current.h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			// The next open marks it `restart`; without its definition, it waits for one to clean up.
			rig.registry = harness.CreateRegistry()
			current = rig.open()
			graph, err := startedTaskGraph(t.Context(), current.h)
			if err != nil {
				t.Fatal(err)
			}
			current.h.Resume()
			pollUntil(t, "the restart mark", func() bool {
				record := taskOf(t, current.h, id)
				return record != nil && record.AbortReason == durable.AbortedByRestart
			})
			pollUntil(t, "the graph shows the missing definition", func() bool { node, ok := graphNode(graph, id); return ok && node.Blocked == "missing_task" })
			_, _ = graph.Stop()
		})
	})

	// harness-blocked.test.ts:498 "shows a task without a definition as blocked, and wakes the host once a registry change unblocks it"
	t.Run("shows a task without a definition as blocked, and wakes the host once a registry change unblocks it", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			missing := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.missing", Version: 1,
				Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
					"run": func(ctx context.Context, _ loopRecord, runtime loopRuntime) error {
						return runtime.Commit(ctx, func(durable.Tx, loopRecord) (*loopNext, error) { return loopCompleted(nil), nil })
					},
				},
				Abort: func(context.Context, loopRecord, loopRuntime) error { return nil },
			})
			registry := harness.CreateRegistry()
			rig := newCrashRig(t, core, registry, harness.HarnessSettings{})
			current := rig.open()
			graph, err := startedTaskGraph(t.Context(), current.h)
			if err != nil {
				t.Fatal(err)
			}
			id := startOwned(t, rootOf(t, current.h), missing.Erased())
			current.h.Resume()
			pollUntil(t, "the graph shows the missing definition", func() bool { node, ok := graphNode(graph, id); return ok && node.Blocked == "missing_task" })
			pollUntil(t, "the wake idle", func() bool { return !current.h.Wake().Active })
			installTasks(t, registry, "late", missing.Erased())
			// The graph follows the registry; the host hears it can run work now.
			// graph.value.tasks[id]?.blocked === undefined: the node may already be gone, the task having completed.
			pollUntil(t, "the graph unblocked", func() bool { node, _ := graphNode(graph, id); return node.Blocked == "" })
			pollUntil(t, "the host told it is active", func() bool {
				for _, wake := range current.wakes.all() {
					if wake.Active {
						return true
					}
				}
				return false
			})
			if outcome := waitFinished2(t, current.h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome %+v, want completed", outcome)
			}
			_, _ = graph.Stop()
		})
	})
}
