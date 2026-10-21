// Ports the two cases of packages/durable/test/harness-blocked.test.ts (Pi 482c1381a0) that hold the Session line or fail one
// commit, which the core's single owner goroutine cannot do from a host callback; the other ten run over both engines in
// durable/core/driver (harness_blocked_core_test.go).

package harness

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

type blockedState struct {
	Phase string `json:"phase"`
}

type (
	blockedRuntime = durable.TaskRuntime[durable.JsonValue, blockedState, durable.JsonValue, any]
	blockedRecord  = durable.RunningTask[durable.JsonValue, blockedState, durable.JsonValue]
)

// failOnceCommit is a Storage whose next Commit fails, as on a platform reset.
type failOnceCommit struct {
	durable.Storage
	mu     sync.Mutex
	failed bool
	err    error
}

func (s *failOnceCommit) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.mu.Lock()
	fail := !s.failed
	s.failed = true
	s.mu.Unlock()
	if fail {
		return 0, s.err
	}
	return s.Storage.Commit(ctx, writes)
}

func TestBlockedTasksOnTheReference(t *testing.T) {
	// harness-blocked.test.ts:102 "keeps the host's wake active until open's recovery pass has landed"
	t.Run("keeps the host's wake active until open's recovery pass has landed", func(t *testing.T) {
		held := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, blockedState, durable.JsonValue, any]{
			Name: "test.held", Version: 1,
			Initial: func(durable.JsonValue) blockedState { return blockedState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, blockedState, durable.JsonValue, any]{
				"run": func(context.Context, blockedRecord, blockedRuntime) error { return nil },
			},
			Abort: func(context.Context, blockedRecord, blockedRuntime) error { return nil },
		})
		registry := CreateRegistry()
		addTask(t, registry, held.Erased(), "held")
		path := filepath.Join(t.TempDir(), "session.sqlite")
		first, _, _ := openTasks(t, tkOpenSqlite(t, path), nil, openTasksOptions{registry: registry})
		id := tkCreateIn(t, first, held.Erased())
		mustClose(t, first)
		// A crash left the task holding its outcome: the next open's recovery pass makes it terminal.
		raw := tkOpenSqlite(t, path)
		record, err := raw.Task(testContext, id)
		if err != nil || record == nil {
			t.Fatalf("Task(%d) = %v, %v", id, record, err)
		}
		completed := durable.JsonValue(nil)
		completing := *record
		completing.Memos = nil
		completing.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskCompleting, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &completed}}
		if _, err := raw.Commit(testContext, []durable.StorageWrite{durable.TaskWrite{Value: completing}}); err != nil {
			t.Fatal(err)
		}
		if err := raw.Close(testContext); err != nil {
			t.Fatal(err)
		}

		// That pass's commit fails, as on a platform reset.
		failing := &failOnceCommit{Storage: tkOpenSqlite(t, path), err: errors.New("reset")}
		var mu sync.Mutex
		var wakes []Wake
		harness, err := OpenHarness(testContext, failing, HarnessOptions{Models: ai.CreateModels(), Registry: registry, OnWake: func(wake Wake) {
			mu.Lock()
			wakes = append(wakes, wake)
			mu.Unlock()
		}})
		if err != nil {
			t.Fatal(err)
		}
		<-harness.Closed()
		if end := harness.End(); end.Reason != durable.SessionEndFailed {
			t.Fatalf("session end %+v, want failed", end)
		}
		// The host was told work is underway, and never that it ended: its recovery alarm stays.
		mu.Lock()
		defer mu.Unlock()
		if len(wakes) != 1 || !wakes[0].Active || wakes[0].At != nil {
			t.Fatalf("wakes %+v, want [{active: true}]", wakes)
		}
	})

	// harness-blocked.test.ts:222 "keeps an abort mark admitted before close in the record close writes back"
	t.Run("keeps an abort mark admitted before close in the record close writes back", func(t *testing.T) {
		started := deferred()
		loop := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, blockedState, durable.JsonValue, any]{
			Name: "test.crash-loop", Version: 1,
			Initial: func(durable.JsonValue) blockedState { return blockedState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, blockedState, durable.JsonValue, any]{
				"run": func(_ context.Context, _ blockedRecord, runtime blockedRuntime) error {
					started.resolve()
					<-runtime.Signal().Done()
					return nil
				},
			},
			Abort: func(context.Context, blockedRecord, blockedRuntime) error { return nil },
		})
		registry := CreateRegistry()
		addTask(t, registry, loop.Erased(), "loop")
		path := filepath.Join(t.TempDir(), "session.sqlite")
		harness, _, _ := openTasks(t, tkOpenSqlite(t, path), nil, openTasksOptions{registry: registry})
		root := mustRoot(t, harness, nil)
		id := tkStart(t, root, loop.Erased())
		harness.Resume()
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		// Hold the line; the abort mark is admitted behind it, then close begins.
		gate := deferred()
		holding := make(chan error, 1)
		go func() {
			_, err := root.Commit(testContext, func(durable.Tx) (any, error) { return nil, gate.wait(testContext) })
			holding <- err
		}()
		eventually(t, func() bool { return harness.(*harnessImpl).LineJobs() > 0 })
		aborting := tkMarkAdmitted(t, harness, id)
		closing := tkCloseSealed(t, harness)
		// The invocation ends while the line is held; its write-back waits behind the abort mark.
		flush()
		gate.resolve()
		if err := <-holding; err != nil {
			t.Fatal(err)
		}
		if result := <-aborting; result.err != nil || result.result != "marked" {
			t.Fatalf("AbortTask %+v, want marked", result)
		}
		if err := tkWaitErr(t, closing); err != nil {
			t.Fatal(err)
		}
		raw := tkOpenSqlite(t, path)
		defer func() { _ = raw.Close(context.Background()) }()
		record, err := raw.Task(testContext, id)
		if err != nil || record == nil || !record.AbortRequested || record.State.Status != durable.TaskPending {
			t.Fatalf("task %+v (%v), want the abort mark kept and pending", record, err)
		}
	})
}

// tkMarkAdmitted starts AbortTask and returns once its request is queued on the line, behind the commit holding it.
func tkMarkAdmitted(t *testing.T, harness Harness, id durable.TaskId) <-chan tkAbortResult {
	t.Helper()
	aborting := make(chan tkAbortResult, 1)
	jobs := harness.(*harnessImpl).LineJobs()
	go func() {
		result, err := harness.AbortTask(testContext, id)
		aborting <- tkAbortResult{result: result, err: err}
	}()
	eventually(t, func() bool { return harness.(*harnessImpl).LineJobs() > jobs })
	return aborting
}

// A wake taken at a change is owed to OnWake even when the Session failed, sealing the scheduler, before the goroutine
// that publishes it ran (scheduler.ts #scheduleWake's microtask ran before any later commit could fail the Session).
func TestPublishWakeOwedAfterSeal(t *testing.T) {
	var got []Wake
	harness, _, _ := openTasks(t, tkOpenSqlite(t, filepath.Join(t.TempDir(), "s.sqlite")), nil, openTasksOptions{onWake: func(w Wake) { got = append(got, w) }})
	scheduler := harness.(*harnessImpl).tasks
	mustClose(t, harness)
	got = nil
	at := 1e18
	scheduler.publishWake(0, &Wake{Active: true, At: &at})
	if len(got) != 1 || !got[0].Active {
		t.Fatalf("wakes %+v, want the one taken before the seal", got)
	}
}
