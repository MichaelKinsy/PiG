// Ports packages/durable/test/task-support.ts.

package harness

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
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

// wait blocks until the gate settles or ctx is cancelled.
func (gate *deferredGate) wait(ctx context.Context) error {
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

// eventually flushes scheduling turns until check holds (200 attempts, as upstream).
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	for range 200 {
		if check() {
			return
		}
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
}

type reportLog struct {
	mu      sync.Mutex
	reports []error
}

func (log *reportLog) add(err error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.reports = append(log.reports, err)
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
		Models:   ai.CreateModels(),
		Registry: registry,
		OnReport: reports.add,
		Now:      option.now,
		Settings: option.settings,
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

// queueOnLine runs start and waits until the operation it began has queued one more job on the Session line, which a held line job keeps queued, or has already settled without one (a cached read).
func queueOnLine(t *testing.T, harness Harness, start func() <-chan struct{}) {
	t.Helper()
	impl := harness.(*harnessImpl)
	jobs := impl.LineJobs()
	done := start()
	eventually(t, func() bool { return impl.LineJobs() > jobs || settled(done) })
}

// queueOnLineIn runs start and waits until a goroutine running operation (a stack substring such as
// "harnessImpl).Inspect") waits on the Session line behind a held job, or until the operation settled. Unlike
// queueOnLine it cannot mistake other line work, such as the scheduler's, for the operation's own job.
func queueOnLineIn(t *testing.T, operation string, start func() <-chan struct{}) {
	t.Helper()
	done := start()
	eventually(t, func() bool {
		return settled(done) || goroutinesMatching(func(stack string) bool {
			header, _, _ := strings.Cut(stack, "\n")
			return strings.Contains(header, "[chan receive") && strings.Contains(stack, "durable/session.enqueue[") && strings.Contains(stack, operation)
		}) > 0
	})
}

func goroutinesMatching(match func(stack string) bool) int {
	buffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			buffer = buffer[:n]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	count := 0
	for stack := range strings.SplitSeq(string(buffer), "\n\n") {
		if match(stack) {
			count++
		}
	}
	return count
}
