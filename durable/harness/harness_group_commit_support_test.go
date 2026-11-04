package harness

// Support of the port of packages/durable/test/harness-group-commit.test.ts: the module-level Hold, TwoPhases and Waits
// tasks with their named gates, run counts and recorded refusals, the Probe and Waiter tasks of "records a decision copies",
// and the calls a test issues from its own goroutine.
//
// Go mapping decisions (as the neighbouring ports, see harness_tasks_test.go and harness_cancellation_barrier_test.go):
//   - Upstream's module-level `gates`, `runs` and `refusals` maps, cleared in afterEach, are one gcFixtures per test.
//   - A call a test starts and awaits later (`const creating = c.commit(...)`) is gcGo: it runs on its own goroutine and gcGo
//     returns once the call entered (returned, or is parked in a wait), which is JavaScript running a call synchronously up
//     to its first await: it has taken its place on the Session line, and what the test starts next queues behind it.
//   - Upstream's `await flush()` (one macrotask turn) is gcFlush: it returns once every goroutine is parked in a wait
//     (awaitQuiescent), so what can run has run. The scheduler's own goroutines are not calls the test can wait for.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// gcFlush is `await flush()`: every goroutine has run up to a wait.
func gcFlush(t testing.TB) {
	t.Helper()
	awaitQuiescent(t)
}

// gcTimeout bounds every wait of a test on a call or a storage call that must arrive.
const gcTimeout = 30 * time.Second

// gcInput is the input of Hold, TwoPhases, Waits and Probe.
type gcInput struct {
	Name string `json:"name"`
}

// gcProbeState is the checkpoint of Probe: `{ phase: "hold"; n: number }`.
type gcProbeState struct {
	Phase string `json:"phase"`
	N     int    `json:"n"`
}

type (
	gcRecord[S any]  = durable.RunningTask[gcInput, S, durable.JsonValue]
	gcRuntime[S any] = durable.TaskRuntime[gcInput, S, durable.JsonValue, any]
	gcNext[S any]    = durable.NextTaskState[S, durable.JsonValue]
)

// gcCompleted is upstream's COMPLETED: the terminal state completed with a null result.
func gcCompleted[S any]() *gcNext[S] { return completed[S, durable.JsonValue](nil) }

// gcAborted is the terminal state aborted, without a reason.
func gcAborted[S any]() *gcNext[S] {
	return &gcNext[S]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}
}

// gcCommit commits next for the task, the state it returns.
func gcCommit[S any](ctx context.Context, runtime gcRuntime[S], next func() *gcNext[S]) error {
	return runtime.Commit(ctx, func(durable.Tx, gcRecord[S]) (*gcNext[S], error) { return next(), nil })
}

// gcFixtures holds the gates, run counts and recorded refusals of one test's tasks.
type gcFixtures struct {
	mu       sync.Mutex
	gates    map[string]*deferredGate
	runs     map[string]int
	refusals map[string]error
	runtimes map[string]gcRuntime[gcProbeState]

	hold      durable.Task[gcInput, stepState, durable.JsonValue, any]
	twoPhases durable.Task[gcInput, stepState, durable.JsonValue, any]
	waits     durable.Task[gcInput, stepState, durable.JsonValue, any]
	probe     durable.Task[gcInput, gcProbeState, durable.JsonValue, any]
	waiter    durable.Task[struct{}, stepState, durable.JsonValue, any]
}

func newGCFixtures() *gcFixtures {
	fixtures := &gcFixtures{
		gates:    map[string]*deferredGate{},
		runs:     map[string]int{},
		refusals: map[string]error{},
		runtimes: map[string]gcRuntime[gcProbeState]{},
	}
	fixtures.hold = fixtures.holdTask()
	fixtures.twoPhases = fixtures.twoPhasesTask()
	fixtures.waits = fixtures.waitsTask()
	fixtures.probe = fixtures.probeTask()
	fixtures.waiter = fixtures.waiterTask()
	return fixtures
}

// gate is the named gate, created closed.
func (fixtures *gcFixtures) gate(name string) *deferredGate {
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	found := fixtures.gates[name]
	if found == nil {
		found = deferred()
		fixtures.gates[name] = found
	}
	return found
}

// ran is how often the named phase ran.
func (fixtures *gcFixtures) ran(name string) int {
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	return fixtures.runs[name]
}

func (fixtures *gcFixtures) count(name string) {
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	fixtures.runs[name]++
}

// refusal is why the named Waits task's wait was refused, nil when it was not.
func (fixtures *gcFixtures) refusal(name string) error {
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	return fixtures.refusals[name]
}

// probeRuntime is the runtime of the running probe name, nil before its phase ran.
func (fixtures *gcFixtures) probeRuntime(name string) gcRuntime[gcProbeState] {
	fixtures.mu.Lock()
	defer fixtures.mu.Unlock()
	return fixtures.runtimes[name]
}

// holdTask holds until its named gate opens, then completes; its abort handler ends it `aborted`.
func (fixtures *gcFixtures) holdTask() durable.Task[gcInput, stepState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[gcInput, stepState, durable.JsonValue, any]{
		Name:    "gc.hold",
		Version: 1,
		Initial: func(gcInput) stepState { return stepState{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[gcInput, stepState, durable.JsonValue, any]{
			"hold": func(ctx context.Context, task gcRecord[stepState], runtime gcRuntime[stepState]) error {
				fixtures.count(task.Input.Name)
				select {
				case <-fixtures.gate(task.Input.Name).done:
				case <-runtime.Signal().Done():
					return context.Cause(runtime.Signal())
				}
				return gcCommit(ctx, runtime, gcCompleted[stepState])
			},
		},
		Abort: func(ctx context.Context, _ gcRecord[stepState], runtime gcRuntime[stepState]) error {
			return gcCommit(ctx, runtime, gcAborted[stepState])
		},
	})
}

// twoPhasesTask commits its next phase, returns once `<name>.return` opens; the next phase counts its run and completes.
func (fixtures *gcFixtures) twoPhasesTask() durable.Task[gcInput, stepState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[gcInput, stepState, durable.JsonValue, any]{
		Name:    "gc.two-phases",
		Version: 1,
		Initial: func(gcInput) stepState { return stepState{Phase: "one"} },
		Phases: map[string]durable.PhaseHandler[gcInput, stepState, durable.JsonValue, any]{
			"one": func(ctx context.Context, task gcRecord[stepState], runtime gcRuntime[stepState]) error {
				if err := gcCommit(ctx, runtime, func() *gcNext[stepState] {
					return &gcNext[stepState]{Status: durable.TaskRunning, Checkpoint: &stepState{Phase: "two"}}
				}); err != nil {
					return err
				}
				return fixtures.gate(task.Input.Name + ".return").wait(ctx)
			},
			"two": func(ctx context.Context, task gcRecord[stepState], runtime gcRuntime[stepState]) error {
				fixtures.count(task.Input.Name + ".two")
				return gcCommit(ctx, runtime, gcCompleted[stepState])
			},
		},
		Abort: func(ctx context.Context, _ gcRecord[stepState], runtime gcRuntime[stepState]) error {
			return gcCommit(ctx, runtime, gcAborted[stepState])
		},
	})
}

// gcTaskIdOf is the task ID a memo holds: a JSON number.
func gcTaskIdOf(value durable.JsonValue) durable.TaskId {
	switch number := value.(type) {
	case float64:
		return durable.TaskId(number)
	case int64:
		return durable.TaskId(number)
	case int:
		return durable.TaskId(number)
	case durable.TaskId:
		return number
	}
	panic("memo is not a task ID")
}

// waitsTask, once its gate opens, waits on the task its memo "on" names; it records why a refused wait was refused, then
// completes.
func (fixtures *gcFixtures) waitsTask() durable.Task[gcInput, stepState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[gcInput, stepState, durable.JsonValue, any]{
		Name:    "gc.waits",
		Version: 1,
		Initial: func(gcInput) stepState { return stepState{Phase: "wait"} },
		Phases: map[string]durable.PhaseHandler[gcInput, stepState, durable.JsonValue, any]{
			"wait": func(ctx context.Context, task gcRecord[stepState], runtime gcRuntime[stepState]) error {
				if err := fixtures.gate(task.Input.Name).wait(ctx); err != nil {
					return err
				}
				on, _, err := runtime.Memo(ctx, "on")
				if err != nil {
					return err
				}
				waiting := func() *gcNext[stepState] {
					return &gcNext[stepState]{Status: durable.TaskWaiting, Checkpoint: &stepState{Phase: "done"}, On: []durable.TaskId{gcTaskIdOf(on)}, Policy: durable.JoinAllSettled}
				}
				if err := gcCommit(ctx, runtime, waiting); err != nil {
					fixtures.mu.Lock()
					fixtures.refusals[task.Input.Name] = err
					fixtures.mu.Unlock()
					return gcCommit(ctx, runtime, gcCompleted[stepState])
				}
				return nil
			},
			"done": func(ctx context.Context, _ gcRecord[stepState], runtime gcRuntime[stepState]) error {
				return gcCommit(ctx, runtime, gcCompleted[stepState])
			},
		},
		Abort: func(ctx context.Context, _ gcRecord[stepState], runtime gcRuntime[stepState]) error {
			return gcCommit(ctx, runtime, gcAborted[stepState])
		},
	})
}

// probeTask holds until aborted, its runtime exposed; it fails once its gate opens. Its abort handler holds too.
func (fixtures *gcFixtures) probeTask() durable.Task[gcInput, gcProbeState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[gcInput, gcProbeState, durable.JsonValue, any]{
		Name:    "gc.probe",
		Version: 1,
		Initial: func(gcInput) gcProbeState { return gcProbeState{Phase: "hold"} },
		Phases: map[string]durable.PhaseHandler[gcInput, gcProbeState, durable.JsonValue, any]{
			"hold": func(ctx context.Context, task gcRecord[gcProbeState], runtime gcRuntime[gcProbeState]) error {
				fixtures.mu.Lock()
				fixtures.runtimes[task.Input.Name] = runtime
				fixtures.mu.Unlock()
				select {
				case <-fixtures.gate(task.Input.Name).done:
				case <-runtime.Signal().Done():
					return nil
				}
				return gcCommit(ctx, runtime, func() *gcNext[gcProbeState] {
					return &gcNext[gcProbeState]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{
						Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "gate"},
					}}
				})
			},
		},
		Abort: func(_ context.Context, _ gcRecord[gcProbeState], runtime gcRuntime[gcProbeState]) error {
			<-runtime.Signal().Done()
			return nil
		},
	})
}

// waiterTask creates probes `x` and `y` and waits on them `failFast`.
func (fixtures *gcFixtures) waiterTask() durable.Task[struct{}, stepState, durable.JsonValue, any] {
	type record = durable.RunningTask[struct{}, stepState, durable.JsonValue]
	type runtime = durable.TaskRuntime[struct{}, stepState, durable.JsonValue, any]
	return durable.DefineTask(durable.TaskDefinition[struct{}, stepState, durable.JsonValue, any]{
		Name:    "gc.fail-fast",
		Version: 1,
		Initial: func(struct{}) stepState { return stepState{Phase: "spawn"} },
		Phases: map[string]durable.PhaseHandler[struct{}, stepState, durable.JsonValue, any]{
			"spawn": func(ctx context.Context, task record, runtime runtime) error {
				return runtime.Commit(ctx, func(tx durable.Tx, _ record) (*gcNext[stepState], error) {
					var on []durable.TaskId
					for _, name := range []string{"x", "y"} {
						id, err := durable.CreateTask(tx, fixtures.probe, gcInput{Name: name}, durable.TaskOptions{Ownership: scaleOwnedBy(task.Id)})
						if err != nil {
							return nil, err
						}
						on = append(on, id)
					}
					return &gcNext[stepState]{Status: durable.TaskWaiting, Checkpoint: &stepState{Phase: "done"}, On: on, Policy: durable.JoinFailFast}, nil
				})
			},
			"done": func(ctx context.Context, _ record, runtime runtime) error {
				return runtime.Commit(ctx, func(durable.Tx, record) (*gcNext[stepState], error) { return gcCompleted[stepState](), nil })
			},
		},
		Abort: func(_ context.Context, _ record, runtime runtime) error {
			<-runtime.Signal().Done()
			return nil
		},
	})
}

// gcCall is one call a test issued from its own goroutine.
type gcCall[T any] struct {
	done  chan struct{}
	value T
	err   error
}

// gcGo runs call on a new goroutine and returns once it entered.
func gcGo[T any](call func() (T, error)) *gcCall[T] {
	started := &gcCall[T]{done: make(chan struct{})}
	callAsync(func() {
		defer close(started.done)
		started.value, started.err = call()
	})
	return started
}

// result blocks until the call returned and returns what it produced.
func (started *gcCall[T]) result(t *testing.T) (T, error) {
	t.Helper()
	select {
	case <-started.done:
		return started.value, started.err
	case <-time.After(gcTimeout):
		t.Fatal("timed out waiting for a call to return")
		panic("unreachable")
	}
}

// wait blocks until the call returned and fails the test with its error.
func (started *gcCall[T]) wait(t *testing.T) T {
	t.Helper()
	value, err := started.result(t)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// settled reports whether the call returned after a flush; a call that has not is parked.
func (started *gcCall[T]) settled(t testing.TB) bool {
	t.Helper()
	gcFlush(t)
	select {
	case <-started.done:
		return true
	default:
		return false
	}
}

// gcEntered blocks until the held storage call arrived.
func gcEntered(t *testing.T, held gate) {
	t.Helper()
	ctx, cancel := context.WithTimeout(testContext, gcTimeout)
	defer cancel()
	if err := held.entered.wait(ctx); err != nil {
		t.Fatal("the storage call did not arrive")
	}
}

// gcAwaited blocks until the gate settles, and fails the test with what did not happen when it does not within gcTimeout.
func gcAwaited(t *testing.T, gate *deferredGate, what string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(testContext, gcTimeout)
	defer cancel()
	if err := gate.wait(ctx); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// gcStatus is the status of the task, "" when absent.
func gcStatus(t *testing.T, harness Harness, id durable.TaskId) string {
	t.Helper()
	record, err := harness.GetTask(testContext, id)
	if err != nil {
		t.Fatal(err)
	}
	if record == nil {
		return ""
	}
	return string(record.State.Status)
}

// gcCheckpoint is the checkpoint of the task as JSON members.
func gcCheckpoint(t *testing.T, harness Harness, id durable.TaskId) map[string]any {
	t.Helper()
	record, err := harness.GetTask(testContext, id)
	if err != nil || record == nil || record.State.Checkpoint == nil {
		t.Fatalf("task %d has no checkpoint: %v", id, err)
	}
	members, _ := jsonOf(t, *record.State.Checkpoint).(map[string]any)
	return members
}

// gcTaskSet is a set of task IDs that a commit observer fills and a test reads.
type gcTaskSet struct {
	mu  sync.Mutex
	ids map[durable.TaskId]bool
}

func (set *gcTaskSet) add(id durable.TaskId) {
	set.mu.Lock()
	defer set.mu.Unlock()
	if set.ids == nil {
		set.ids = map[durable.TaskId]bool{}
	}
	set.ids[id] = true
}

func (set *gcTaskSet) has(id durable.TaskId) bool {
	set.mu.Lock()
	defer set.mu.Unlock()
	return set.ids[id]
}

// gcFollowStoredTasks follows the stored commits and collects the IDs of the task records they write that keep accepts
// (upstream's followCommits with `publication.kind !== "stored"` ignored).
func gcFollowStoredTasks(harness Harness, keep func(record anyTaskRecord) bool) *gcTaskSet {
	set := &gcTaskSet{}
	followCommits(harness, func(_ context.Context, publication session.ObservedCommit) {
		if publication.Memory {
			return
		}
		for _, change := range publication.Changes {
			if write, ok := change.(durable.TaskWrite); ok && keep(write.Value) {
				set.add(write.Value.Id)
			}
		}
	})
	return set
}

// gcCreatedBy is a task ID a commit callback created, read by another goroutine.
type gcCreatedBy struct{ id atomic.Int64 }

func (created *gcCreatedBy) set(id durable.TaskId) { created.id.Store(int64(id)) }

// publishedBy reports whether the task was created and its record is in set.
func (created *gcCreatedBy) publishedIn(set *gcTaskSet) bool {
	id := durable.TaskId(created.id.Load())
	return id != 0 && set.has(id)
}

// gcReadAfter is upstream's `session.readOnLine(async () => { const gate = session.touch(key); if (gate !== undefined)
// await gate; return created !== undefined && published.has(created); })`: a read on the line that waits for the
// unpublished writers of key, started on its own goroutine at its place on the line.
func gcReadAfter(harness Harness, key string, created *gcCreatedBy, published *gcTaskSet) *gcCall[bool] {
	impl := harness.(*harnessImpl)
	return gcGo(func() (bool, error) {
		value, err := impl.ReadOnLine(func(durable.Storage) (any, error) {
			if err := impl.AwaitTouch(testContext, key); err != nil {
				return nil, err
			}
			return created.publishedIn(published), nil
		})
		if err != nil {
			return false, err
		}
		return value.(bool), nil
	})
}
