// Scheduler behaviours of parked work that Pi's harness-tool-gates and harness-park-children tests reach through a tool
// call (Pi 473515f9c, spec suspendable §2.5, §5.2, §9): registry publications, an open, and a sleep committed under a
// replaced registry wake a call parked at its gates; the children of a parked call survive a restart; the close signal
// outlives an abort mark. The tool slice ports those tests over the real tool task; these run the scheduler alone, with the
// two predicates the Harness hands it replaced by the test's, and are not ports.

package harness

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// parkedKinds is the task kinds of a test, replaced for the tool predicates.
const (
	parkGatedKind = "test.gated"
	parkPlainKind = "test.plain"
)

// parkSignal is the key a parked test task sleeps on; nothing delivers it.
var parkSignal = durable.SignalKey{Signal: "test.park", Key: "never"}

// parkTask runs its gate phase every time it is reserved: it counts the run, calls onRun, and sleeps on a signal no one sends,
// at the same checkpoint.
func parkTask(kind string, runs *atomic.Int32, onRun func(run int32)) durable.AnyTask {
	return tkOneStep(kind, func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		run := runs.Add(1)
		if onRun != nil {
			onRun(run)
		}
		return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
			return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "run"}, Signals: []durable.SignalKey{parkSignal}}, nil
		})
	}).Erased()
}

// gatedKind is the Gated predicate of a test: a sleeping task of kind.
func gatedKind(kind string) func(*TaskSchedulerOptions) {
	return func(options *TaskSchedulerOptions) {
		options.Gated = func(record anyTaskRecord) bool {
			return record.Kind == kind && record.State.Status == durable.TaskSleeping
		}
	}
}

// openAdjusted opens a Harness as openTasks does, with the scheduler's options adjusted before the scheduler opens: its
// registry subscription, its recovery, and its first pass see them.
func openAdjusted(t *testing.T, store durable.Storage, registry Registry, adjust func(*TaskSchedulerOptions), options ...openTasksOptions) Harness {
	t.Helper()
	var option openTasksOptions
	if len(options) > 0 {
		option = options[0]
	}
	reports := &reportLog{}
	harness := newHarness(testContext, store, HarnessOptions{Models: ai.CreateModels(), Registry: registry, OnReport: reports.add, Now: option.now, OnWake: option.onWake})
	adjust(&harness.tasks.options)
	if err := harness.tasks.Open(testContext); err != nil {
		t.Fatal(err)
	}
	return harness
}

func parkRegistry(t *testing.T, tasks ...durable.AnyTask) Registry {
	t.Helper()
	registry := CreateRegistry()
	mustInstall(t, registry, &durable.Extension{Name: "tasks", Tasks: tasks})
	return registry
}

func TestGatedCallAndKeepsChildrenPredicates(t *testing.T) {
	record := func(kind string, status durable.TaskStatus, phase string) anyTaskRecord {
		state := durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: status}
		if phase != "" {
			checkpoint := durable.JsonValue(map[string]any{"phase": phase})
			state.Checkpoint = &checkpoint
		}
		return anyTaskRecord{Kind: kind, State: state}
	}
	for _, test := range []struct {
		name         string
		record       anyTaskRecord
		gated, keeps bool
	}{
		{"a tool call asleep at its gates", record("pi.tool", durable.TaskSleeping, "call"), true, true},
		{"a tool call not started yet", record("pi.tool", durable.TaskPending, "call"), false, true},
		{"a tool call parked in execute", record("pi.tool", durable.TaskSleeping, "parked"), false, true},
		{"a tool call waiting in execute", record("pi.tool", durable.TaskWaiting, "parked"), false, true},
		{"a tool call cut off while running", record("pi.tool", durable.TaskPending, "execute"), false, false},
		{"a tool call holding its outcome", record("pi.tool", durable.TaskCompleting, ""), false, false},
		{"another task asleep at a call phase", record("test.other", durable.TaskSleeping, "call"), false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := gatedCall(test.record); got != test.gated {
				t.Errorf("gatedCall = %v, want %v", got, test.gated)
			}
			if got := keepsChildren(test.record); got != test.keeps {
				t.Errorf("keepsChildren = %v, want %v", got, test.keeps)
			}
		})
	}
}

func TestRegistryPublicationWakesASleepParkedAtAGate(t *testing.T) {
	gated, plain := &atomic.Int32{}, &atomic.Int32{}
	gatedTask, plainTask := parkTask(parkGatedKind, gated, nil), parkTask(parkPlainKind, plain, nil)
	registry := parkRegistry(t, gatedTask, plainTask)
	harness := openAdjusted(t, storage.NewMemoryStorage(), registry, gatedKind(parkGatedKind))
	root := mustRoot(t, harness, nil)
	gatedId := tkStart(t, root, gatedTask)
	plainId := tkStart(t, root, plainTask)
	harness.Resume()
	eventually(t, func() bool {
		return gated.Load() == 1 && plain.Load() == 1 && sigStatus(t, harness, gatedId) == durable.TaskSleeping && sigStatus(t, harness, plainId) == durable.TaskSleeping
	})
	awaitSchedulerSettled(t, harness.(*harnessImpl).tasks)
	// Another extension installed: the call parked at its gate is asked again, and sleeps again; the other sleeper is left alone.
	mustInstall(t, registry, &durable.Extension{Name: "other"})
	eventually(t, func() bool { return gated.Load() == 2 && sigStatus(t, harness, gatedId) == durable.TaskSleeping })
	awaitSchedulerSettled(t, harness.(*harnessImpl).tasks)
	if got := plain.Load(); got != 1 {
		t.Fatalf("the sleeper that is not parked at a gate ran %d times, want 1", got)
	}
	if got := gated.Load(); got != 2 {
		t.Fatalf("the gated sleeper ran %d times, want 2", got)
	}
	mustClose(t, harness)
}

func TestOpenWakesASleepParkedAtAGate(t *testing.T) {
	for _, test := range []struct {
		name string
		// adjust is applied at the second open.
		adjust func(*TaskSchedulerOptions)
		want   int32
	}{
		{"a call parked at a gate runs again", gatedKind(parkGatedKind), 2},
		{"a sleep not parked at a gate stays asleep", func(*TaskSchedulerOptions) {}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := sqlitePath(t)
			runs := &atomic.Int32{}
			task := parkTask(parkGatedKind, runs, nil)
			first := openAdjusted(t, tkOpenSqlite(t, path), parkRegistry(t, task), func(*TaskSchedulerOptions) {})
			id := tkStart(t, mustRoot(t, first, nil), task)
			first.Resume()
			eventually(t, func() bool { return runs.Load() == 1 && sigStatus(t, first, id) == durable.TaskSleeping })
			mustClose(t, first)
			// Reopened and resumed: no signal, no deadline; the gates asked again are the registry's of this process.
			second := openAdjusted(t, tkOpenSqlite(t, path), parkRegistry(t, task), test.adjust)
			second.Resume()
			eventually(t, func() bool { return runs.Load() >= test.want && sigStatus(t, second, id) == durable.TaskSleeping })
			awaitSchedulerSettled(t, second.(*harnessImpl).tasks)
			if got := runs.Load(); got != test.want {
				t.Fatalf("ran %d times, want %d", got, test.want)
			}
			mustClose(t, second)
		})
	}
}

func TestSleepCommittedUnderAReplacedRegistryWakesAtOnce(t *testing.T) {
	runs := &atomic.Int32{}
	var registry Registry
	failures := &syncList[error]{}
	task := parkTask(parkGatedKind, runs, func(run int32) {
		// The registry is replaced while the first run's gate decides: its sleep is committed under the old snapshot, and
		// the sleep is not yet there for the publication to wake.
		if run == 1 {
			if err := registry.Install(&durable.Extension{Name: "other"}); err != nil {
				failures.add(err)
			}
		}
	})
	registry = parkRegistry(t, task)
	harness := openAdjusted(t, storage.NewMemoryStorage(), registry, gatedKind(parkGatedKind))
	id := tkStart(t, mustRoot(t, harness, nil), task)
	harness.Resume()
	// Asked again with nothing else happening: the second run is the wake.
	eventually(t, func() bool { return runs.Load() == 2 && sigStatus(t, harness, id) == durable.TaskSleeping })
	awaitSchedulerSettled(t, harness.(*harnessImpl).tasks)
	if got := runs.Load(); got != 2 {
		t.Fatalf("ran %d times, want 2: the second run took its snapshot after the publication", got)
	}
	if err := errors.Join(failures.all()...); err != nil {
		t.Fatal(err)
	}
	mustClose(t, harness)
}

// A publication before a progress step is in the snapshot the step takes: a sleep committed after it is not committed under a
// replaced registry.
func TestPublicationBeforeAProgressStepIsNotAReplacedRegistry(t *testing.T) {
	var registry Registry
	first, second := &atomic.Int32{}, &atomic.Int32{}
	failures := &syncList[error]{}
	task := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    parkGatedKind,
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "one"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"one": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				first.Add(1)
				if err := registry.Install(&durable.Extension{Name: "other"}); err != nil {
					failures.add(err)
				}
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return runningState[stepState, durable.JsonValue](stepState{Phase: "two"}), nil
				})
			},
			"two": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				second.Add(1)
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &stepState{Phase: "two"}, Signals: []durable.SignalKey{parkSignal}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue]("test"), nil
			})
		},
	}).Erased()
	registry = parkRegistry(t, task)
	harness := openAdjusted(t, storage.NewMemoryStorage(), registry, gatedKind(parkGatedKind))
	id := tkStart(t, mustRoot(t, harness, nil), task)
	harness.Resume()
	eventually(t, func() bool { return second.Load() >= 1 && sigStatus(t, harness, id) == durable.TaskSleeping })
	awaitSchedulerSettled(t, harness.(*harnessImpl).tasks)
	if first.Load() != 1 || second.Load() != 1 {
		t.Fatalf("phases ran %d and %d times, want 1 and 1: the sleep was asked again for a publication its snapshot had seen", first.Load(), second.Load())
	}
	if err := errors.Join(failures.all()...); err != nil {
		t.Fatal(err)
	}
	mustClose(t, harness)
}

func TestKeptChildrenSurviveARestart(t *testing.T) {
	for _, test := range []struct {
		name  string
		keeps bool
	}{
		{"the children of an owner that keeps them are not abandoned", true},
		{"the children of any other owner are abandoned", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := storage.NewMemoryStorage()
			ownerId, err := store.MintId()
			if err != nil {
				t.Fatal(err)
			}
			childId, err := store.MintId()
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := durable.JsonValue(map[string]any{"phase": "run"})
			sleeping := durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskSleeping, Checkpoint: &checkpoint, Signals: []durable.SignalKey{parkSignal}}
			owner := durable.TaskId(ownerId)
			commitWrites(t, store,
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
				durable.TaskWrite{Value: anyTaskRecord{Id: owner, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test.owner", Version: 1, State: sleeping}},
				durable.TaskWrite{Value: anyTaskRecord{Id: durable.TaskId(childId), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test.child", Version: 1, Owner: &owner, AbandonOnRestart: true, State: sleeping}},
			)
			registry := parkRegistry(t,
				tkOneStep("test.owner", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) }).Erased(),
				tkOneStep("test.child", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) }).Erased(),
			)
			harness := openAdjusted(t, store, registry, func(options *TaskSchedulerOptions) {
				options.KeepsAbandoned = func(record anyTaskRecord) bool { return test.keeps && record.Kind == "test.owner" }
			})
			scheduler := harness.(*harnessImpl).tasks
			harness.Resume()
			// The first pass marks what restart abandons; it is done once nothing is left to abandon.
			pollUntil(t, func() bool {
				scheduler.mu.Lock()
				defer scheduler.mu.Unlock()
				return scheduler.abandoned.Len() == 0
			})
			awaitSchedulerSettled(t, scheduler)
			child := tkTaskRecord(t, harness, durable.TaskId(childId))
			if test.keeps {
				if child.AbortRequested || child.State.Status != durable.TaskSleeping {
					t.Fatalf("child %s, abort mark %v: it should survive", child.State.Status, child.AbortRequested)
				}
			} else {
				expectOutcome(t, tkWaitOutcome(t, harness, durable.TaskId(childId)), outcomeAborted("test"))
			}
			if held := tkTaskRecord(t, harness, owner); held.AbortRequested {
				t.Fatal("the owner is abort-marked: it is not abandoned")
			}
			mustClose(t, harness)
		})
	}
}

func TestCloseSignalOutlivesAnAbortMark(t *testing.T) {
	started, afterMark := deferred(), deferred()
	var duringAbort, afterClose atomic.Value
	type observed struct{ err error }
	task := tkOneStep("test.close-signal", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		closed := closeSignal(runtime)
		started.resolve()
		select {
		case <-runtime.Signal().Done():
		case <-time.After(10 * time.Second):
			return errors.New("the abort mark never signalled the run")
		}
		// An abort mark cancels the run's own signal, not the Harness's close signal.
		duringAbort.Store(observed{closed.Err()})
		afterMark.resolve()
		select {
		case <-closed.Done():
		case <-time.After(10 * time.Second):
			return errors.New("the close never signalled the cleanup")
		}
		afterClose.Store(observed{context.Cause(closed)})
		return nil
	}).Erased()
	opened := tkOpenRoot(t, []durable.AnyTask{task})
	id := tkStart(t, opened.root, task)
	opened.harness.Resume()
	if err := started.wait(testContext); err != nil {
		t.Fatal(err)
	}
	aborting := tkMarkDurably(t, opened.harness, id)
	if err := afterMark.wait(testContext); err != nil {
		t.Fatal(err)
	}
	if seen, ok := duringAbort.Load().(observed); !ok || seen.err != nil {
		t.Fatalf("the close signal ended with the abort mark: %v", seen.err)
	}
	mustClose(t, opened.harness)
	if seen, ok := afterClose.Load().(observed); !ok || !errors.Is(seen.err, ErrClosed) {
		t.Fatalf("the close signal ended with %v, want %v", seen.err, ErrClosed)
	}
	<-aborting
}
