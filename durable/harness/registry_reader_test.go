// Ports packages/durable/test/harness-registry-reader.test.ts (Pi e7a0df27), the cases that do not need a Chord host. Pi's
// cases compose the registry on `createHost`, `host.serve` and `self.contribute`, which the Go chord/next/host package does not
// have yet; here a registries.RegistryValue over a Registry stands for the composition, publishing on every install as the
// host's compose does. The cases that depend on the host's batch (a Chord call from the listener fails with invalid/reentrant;
// aborting the observation's context detaches every listener) are the host's, not registryReader's.

package harness

// pi: packages/durable/src/harness/registry-reader.ts

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// compositionValue is a registries.RegistryValue whose composition is a Registry's snapshot: every observer's Advance runs inside
// the publication that composed (the Registry.Subscribe listener), and fail delivers a failed composition to them.
type compositionValue struct {
	chord.NativeWatchable[durable.RegistrySnapshot] // Watch is not used by registryReader
	registry                                        Registry
	observeErr                                      error
	mu                                              sync.Mutex
	observers                                       []state.NativeObserver[durable.RegistrySnapshot]
}

func (value *compositionValue) Value() durable.RegistrySnapshot { return value.registry.Snapshot() }

func (value *compositionValue) Observe(observer state.NativeObserver[durable.RegistrySnapshot], ctx context.Context) (state.NativeObservation[durable.RegistrySnapshot], error) {
	if value.observeErr != nil {
		return state.NativeObservation[durable.RegistrySnapshot]{}, value.observeErr
	}
	value.mu.Lock()
	value.observers = append(value.observers, observer)
	value.mu.Unlock()
	unsubscribe := value.registry.Subscribe(func() {
		observer.Advance(chord.NativeWatchCall[durable.RegistrySnapshot]{Kind: chord.WatchCallValue, Value: value.registry.Snapshot()}, ctx)
	})
	return state.NativeObservation[durable.RegistrySnapshot]{Value: value.registry.Snapshot(), Detach: unsubscribe}, nil
}

func (value *compositionValue) fail(err error, ctx context.Context) {
	value.mu.Lock()
	observers := append([]state.NativeObserver[durable.RegistrySnapshot](nil), value.observers...)
	value.mu.Unlock()
	for _, observer := range observers {
		observer.Advance(chord.NativeWatchCall[durable.RegistrySnapshot]{Kind: chord.WatchCallFailed, Error: err}, ctx)
	}
}

func TestRegistryReader(t *testing.T) {
	t.Run("reads the composed snapshot; a listener is called synchronously, inside the publication that composed", func(t *testing.T) {
		registry := CreateRegistry()
		reader := NewRegistryReader(testContext, &compositionValue{registry: registry})
		if reader.Snapshot().Extension("app.plugin") != nil {
			t.Fatal("the snapshot holds an extension before it is installed")
		}
		var calls []string
		unsubscribe := reader.Subscribe(func() { calls = append(calls, "called") })
		done := registryTask("app.done")
		mustInstall(t, registry, new(durable.Extension{Name: "app.plugin", Tasks: []durable.AnyTask{done.Erased()}}))
		// Install has returned: the listener ran inside it, once.
		if len(calls) != 1 {
			t.Fatalf("calls after one install = %v, want one", calls)
		}
		if got := reader.Snapshot().Task("app.done"); got.Definition == nil {
			t.Fatal("the snapshot after the install has no app.done task")
		}
		unsubscribe()
		mustInstall(t, registry, new(durable.Extension{Name: "app.other"}))
		if len(calls) != 1 {
			t.Fatalf("a detached listener was called again: %v", calls)
		}
	})

	t.Run("a failed composition keeps the last snapshot and wakes nothing", func(t *testing.T) {
		registry := CreateRegistry()
		value := &compositionValue{registry: registry}
		reader := NewRegistryReader(testContext, value)
		woken := 0
		unsubscribe := reader.Subscribe(func() { woken++ })
		defer unsubscribe()
		mustInstall(t, registry, new(durable.Extension{Name: "app.plugin"}))
		value.fail(errors.New("compose failed"), testContext)
		if woken != 1 {
			t.Fatalf("woken after one value and one failed composition = %d, want 1: only a value call wakes", woken)
		}
		if reader.Snapshot().Extension("app.plugin") == nil {
			t.Fatal("a failed composition dropped the last snapshot")
		}
	})

	t.Run("subscribe panics with the error observe returns, as Pi's observe throws", func(t *testing.T) {
		failure := errors.New("registry has no composition")
		reader := NewRegistryReader(testContext, &compositionValue{registry: CreateRegistry(), observeErr: failure})
		defer func() {
			if got := recover(); got != failure {
				t.Fatalf("recovered %v, want %v", got, failure)
			}
		}()
		reader.Subscribe(func() {})
		t.Fatal("Subscribe returned")
	})

	t.Run("wakes a Harness's scheduler: a task blocked on its missing definition runs once a contribution brings it", func(t *testing.T) {
		registry := CreateRegistry()
		done := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, blockedState, durable.JsonValue, any]{
			Name: "app.done", Version: 1,
			Initial: func(durable.JsonValue) blockedState { return blockedState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, blockedState, durable.JsonValue, any]{
				"run": func(ctx context.Context, _ blockedRecord, runtime blockedRuntime) error {
					return runtime.Commit(ctx, func(durable.Tx, blockedRecord) (*durable.NextTaskState[blockedState, durable.JsonValue], error) {
						return completed[blockedState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(context.Context, blockedRecord, blockedRuntime) error { return nil },
		})
		opened, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{Models: ai.CreateModels(), Registry: NewRegistryReader(testContext, &compositionValue{registry: registry})})
		if err != nil {
			t.Fatal(err)
		}
		opened.Resume()
		id := tkStart(t, mustRoot(t, opened, nil), done.Erased())
		type waitResult struct {
			receipt durable.SettledTask[durable.JsonValue]
			err     error
		}
		waited := make(chan waitResult, 1)
		go func() {
			receipt, err := opened.WaitForTask(testContext, id)
			waited <- waitResult{receipt, err}
		}()
		flush()
		select {
		case <-waited:
			t.Fatal("the task ran before its definition was installed")
		default:
		}
		mustInstall(t, registry, new(durable.Extension{Name: "app.plugin", Tasks: []durable.AnyTask{done.Erased()}}))
		select {
		case result := <-waited:
			if result.err != nil {
				t.Fatal(result.err)
			}
			if outcome := tkOutcome(t, result.receipt); outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome %+v, want completed", outcome)
			}
		case <-time.After(10 * time.Second):
			closeHarness(t, opened)
			t.Fatal("the task did not run after its definition was installed: the registry reader did not wake the scheduler")
		}
		closeHarness(t, opened)
	})
}
