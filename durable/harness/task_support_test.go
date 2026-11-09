// Ports packages/durable/test/task-support.ts.

package harness

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/lineadmission"
	"github.com/MichaelKinsy/PiG/telemetry"
)

type stepState struct {
	Phase string `json:"phase"`
}

type stepTask = durable.Task[durable.JsonValue, stepState, durable.JsonValue, any]
type stepRuntime = durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, any]
type stepRecord = durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue]

// deferredGate is task-support.ts deferred(): resolved or rejected once.
type deferredGate struct {
	once sync.Once
	done chan struct{}
	err  error
}

func deferred() *deferredGate { return &deferredGate{done: make(chan struct{})} }

func (gate *deferredGate) resolve() { gate.once.Do(func() { close(gate.done) }) }

// reject settles the gate with err, which every wait returns.
func (gate *deferredGate) reject(err error) {
	gate.once.Do(func() {
		gate.err = err
		close(gate.done)
	})
}

// wait blocks until the gate settles or ctx is cancelled.
func (gate *deferredGate) wait(ctx context.Context) error {
	// A test's await: a watch listener that waits here has run the synchronous part of its call, as an upstream listener that
	// awaits a promise has (chord.DeliveryEntered).
	chord.DeliveryEntered()
	select {
	case <-gate.done:
		return gate.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// abortedBy is task-support.ts aborted(signal): it fails with the signal's cause once the signal ends.
func abortedBy(signal context.Context) error {
	<-signal.Done()
	return context.Cause(signal)
}

// flush is session-support.ts flush(): let pending work run for one scheduling turn.
func flush() { time.Sleep(time.Millisecond) }

// eventually is task-support.ts eventually: check, then await flush(), up to 200 times. A turn is awaitQuiescent, then flush().
// The barrier returns once every other goroutine has run up to a wait, so the work a check waits for (a reopen, a resume, a
// delivery) has finished or is parked before the next attempt, however slowly a starved scheduler runs it. A goroutine parked
// on a timer counts as idle to the barrier, so flush() keeps what a Pi turn also is, a macrotask (setTimeout) of wall time: a
// handover that sleeps 10 ms after its commit still gets the turn spacing Pi gives it.
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	for range 200 {
		if check() {
			return
		}
		awaitQuiescent(t)
		flush()
	}
	t.Fatal("Condition was not reached")
}

// settled reports whether done has been closed after pending work flushes.
func settled(done <-chan struct{}) bool {
	flush()
	select {
	case <-done:
		return true
	default:
		return false
	}
}

type openTasksOptions struct {
	registry Registry
	now      func() float64
	settings func() *HarnessSettings
	onWake   func(Wake)
	setTimer durable.SetTimer
	// telemetry is HarnessOptions.Telemetry.
	telemetry telemetry.TelemetryContext
}

type reportLog struct {
	mu      sync.Mutex
	reports []error
	infos   []durable.ReportInfo
	// arrived is closed by the next add and replaced after it; awaitInfo waits on it.
	arrived chan struct{}
}

func (log *reportLog) add(report durable.Report) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.reports = append(log.reports, report.Error)
	log.infos = append(log.infos, report.ReportInfo)
	if log.arrived != nil {
		close(log.arrived)
		log.arrived = nil
	}
}

// awaitInfo returns the info of every report so far once one of them satisfies match, waiting for the report that does. A task
// raises its report on the goroutine whose commit call just returned, so a reader that came from another wait of the Harness
// has no order against it; upstream's event loop gives one, as the continuation that raises it runs before the awaits that
// follow the reader's wait. ctx ends the wait.
func (log *reportLog) awaitInfo(ctx context.Context, match func(durable.ReportInfo) bool) ([]durable.ReportInfo, error) {
	for {
		log.mu.Lock()
		if slices.ContainsFunc(log.infos, match) {
			infos := slices.Clone(log.infos)
			log.mu.Unlock()
			return infos, nil
		}
		if log.arrived == nil {
			log.arrived = make(chan struct{})
		}
		arrived := log.arrived
		log.mu.Unlock()
		select {
		case <-arrived:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

// allInfos is the info of every report, in order (task-support.ts openTasks infos).
func (log *reportLog) allInfos() []durable.ReportInfo {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]durable.ReportInfo(nil), log.infos...)
}

func (log *reportLog) all() []error {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]error(nil), log.reports...)
}

// openTasks opens a Harness whose registry holds tasks; failures passed to OnReport are collected.
func openTasks(t *testing.T, storage durable.Storage, tasks []durable.AnyTask, options ...openTasksOptions) (Harness, Registry, *reportLog) {
	t.Helper()
	var option openTasksOptions
	if len(options) > 0 {
		option = options[0]
	}
	registry := option.registry
	if registry == nil {
		registry = CreateRegistry()
	}
	if len(tasks) > 0 {
		mustInstall(t, registry, new(durable.Extension{Name: "tasks", Tasks: tasks}))
	}
	reports := &reportLog{}
	harness, err := OpenHarness(testContext, storage, HarnessOptions{
		Models:    ai.CreateModels(),
		Registry:  registry,
		OnReport:  reports.add,
		Now:       option.now,
		Settings:  option.settings,
		OnWake:    option.onWake,
		SetTimer:  option.setTimer,
		Telemetry: option.telemetry,
	})
	if err != nil {
		t.Fatal(err)
	}
	return harness, registry, reports
}

// completed is the next state that completes a task with result.
func completed[S, R any](result R) *durable.NextTaskState[S, R] {
	return &durable.NextTaskState[S, R]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[R]{Status: durable.OutcomeCompleted, Result: &result}}
}

// abortedWith is the next state that aborts a task.
func abortedWith[S, R any](reason string) *durable.NextTaskState[S, R] {
	return &durable.NextTaskState[S, R]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[R]{Status: durable.OutcomeAborted, Reason: &reason}}
}

// countingRegistry is a registry reader that counts live subscriptions.
type countingRegistry struct {
	registry Registry
	mu       sync.Mutex
	count    int
}

func countingReader(registry Registry) *countingRegistry {
	return &countingRegistry{registry: registry}
}

func (reader *countingRegistry) Snapshot() durable.RegistrySnapshot {
	return reader.registry.Snapshot()
}

func (reader *countingRegistry) Subscribe(listener func()) func() {
	reader.mu.Lock()
	reader.count++
	reader.mu.Unlock()
	unsubscribe := reader.registry.Subscribe(listener)
	var once sync.Once
	return func() {
		once.Do(func() {
			reader.mu.Lock()
			reader.count--
			reader.mu.Unlock()
			unsubscribe()
		})
	}
}

func (reader *countingRegistry) subscriptions() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.count
}

var errUnreachable = errors.New("unreachable")

// queueOnLineAdmitted runs start with a context that reports when the Session line admits its call, and waits for that or for the
// operation to settle: the call then holds its position behind the jobs ahead of it, as a TypeScript call that extended
// the line's chain synchronously does (internal/lineadmission).
func queueOnLineAdmitted(t *testing.T, start func(ctx context.Context) <-chan struct{}) {
	t.Helper()
	queueOnLineAdmittedFrom(t, testContext, start)
}

// queueOnLineAdmittedFrom is queueOnLineAdmitted for an operation whose context derives from base.
func queueOnLineAdmittedFrom(t *testing.T, base context.Context, start func(ctx context.Context) <-chan struct{}) {
	t.Helper()
	admitted := make(chan struct{})
	done := start(lineadmission.With(base, func() { close(admitted) }))
	select {
	case <-admitted:
	case <-done:
	case <-time.After(gcTimeout):
		t.Fatal("the operation was neither admitted to the Session line nor settled")
	}
}
