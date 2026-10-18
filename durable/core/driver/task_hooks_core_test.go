// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// coreHookSet is a task's handler set: Ping and Other are the members an extension may leave unset.
type coreHookSet struct {
	Ping  func() error
	Other func()
}

type (
	coreHookRuntime = durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, *coreHookSet]
	coreHookRecord  = durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue]
)

// coreHookTask is a one-phase task: its phase runs body and completes with body's error, so a faulted task carries what Each returned.
func coreHookTask(name string, body func(ctx context.Context, runtime coreHookRuntime) error) durable.AnyTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, *coreHookSet]{
		Name: name, Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "a"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, *coreHookSet]{
			"a": func(ctx context.Context, _ coreHookRecord, runtime coreHookRuntime) error {
				if err := body(ctx, runtime); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(durable.Tx, coreHookRecord) (*stepNext, error) { return completedWith(nil), nil })
			},
		},
		Abort: func(context.Context, coreHookRecord, coreHookRuntime) error { return nil },
	}).Erased()
}

type coreHookEnv struct {
	harness  harness.Harness
	registry harness.Registry
	root     harness.Conversation
	mu       sync.Mutex
	reports  []error
}

func (e *coreHookEnv) reported() []error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]error(nil), e.reports...)
}

func openCoreHooks(t *testing.T, task durable.AnyTask, settings func() *harness.HarnessSettings) *coreHookEnv {
	t.Helper()
	env := &coreHookEnv{registry: harness.CreateRegistry()}
	if err := env.registry.Install(&durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{task}}); err != nil {
		t.Fatal(err)
	}
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{
		Models: ai.CreateModels(), Registry: env.registry, Settings: settings,
		OnReport: func(err error, _ durable.ReportInfo) {
			env.mu.Lock()
			env.reports = append(env.reports, err)
			env.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	env.harness = h
	if env.root, err = h.Root(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	return env
}

func (e *coreHookEnv) install(t *testing.T, name, task string, handlers *coreHookSet) {
	t.Helper()
	if err := e.registry.Install(&durable.Extension{Name: name, Hooks: []durable.HookRegistration{{Task: task, Handlers: handlers}}}); err != nil {
		t.Fatal(err)
	}
}

type coreCalls struct {
	mu   sync.Mutex
	list []string
}

func (c *coreCalls) add(call string) { c.mu.Lock(); c.list = append(c.list, call); c.mu.Unlock() }
func (c *coreCalls) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.list...)
}

// HookRunner.each (packages/durable/src/types.ts:158-163, scheduler.ts:1131-1147) on the native core: every handler set of the task's name that has the
// named member runs in extension order; a set without it is skipped (scheduler.ts:1135 `typeof handler !== "function"`); an ordinary error or panic from
// invoke is reported through OnReport and the next set runs.
// mutation-checked against taskHooks.Each (driver/taskruntime.go): dropping the unset-member skip invokes the set that has no Ping, stopping at the first
// error leaves "third" uncalled, and never reporting leaves OnReport empty.
func TestCoreTaskHooksSkipUnsetMembersAndReportOrdinaryErrors(t *testing.T) {
	boom := errors.New("boom")
	calls := &coreCalls{}
	var eachErr error
	task := coreHookTask("test.core-hooks-each", func(_ context.Context, runtime coreHookRuntime) error {
		eachErr = runtime.Hooks().Each("ping", func(hooks *coreHookSet) error { calls.add("invoked"); return hooks.Ping() })
		return nil
	})
	env := openCoreHooks(t, task, nil)
	env.install(t, "first", "test.core-hooks-each", &coreHookSet{Ping: func() error { calls.add("first"); return boom }})
	env.install(t, "only-other", "test.core-hooks-each", &coreHookSet{Other: func() {}})
	env.install(t, "panics", "test.core-hooks-each", &coreHookSet{Ping: func() error { calls.add("panics"); panic("handler panic") }})
	env.install(t, "third", "test.core-hooks-each", &coreHookSet{Ping: func() error { calls.add("third"); return nil }})
	id := startTask(t, env.root, task)
	env.harness.Resume()
	waitSettled(t, env.harness, id)
	if eachErr != nil {
		t.Fatalf("Each returned %v for ordinary handler errors, want nil", eachErr)
	}
	want := []string{"invoked", "first", "invoked", "panics", "invoked", "third"}
	if got := calls.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v: the set without Ping is skipped and an error does not stop the next set", got, want)
	}
	reports := env.reported()
	if len(reports) != 2 || !errors.Is(reports[0], boom) {
		t.Fatalf("reports = %v, want the returned error and the panic, in order", reports)
	}
}

// scheduler.ts:1141: once the invocation is signalled the error from invoke propagates instead of being reported and the remaining sets do not run.
// mutation-checked: swallowing the error after the signal (reporting it and continuing) leaves Each returning nil and "second" called.
func TestCoreTaskHooksPropagateAnErrorOnceTheInvocationIsSignalled(t *testing.T) {
	boom := errors.New("after the signal")
	calls := &coreCalls{}
	entered := make(chan struct{})
	var eachErr error
	task := coreHookTask("test.core-hooks-signal", func(ctx context.Context, runtime coreHookRuntime) error {
		// The agent resolves at first use and stays fixed for the phase, so Each below does not depend on the cancelled invocation context.
		if _, err := runtime.Agent(ctx); err != nil {
			return err
		}
		close(entered)
		<-runtime.Signal().Done()
		eachErr = runtime.Hooks().Each("ping", func(hooks *coreHookSet) error { return hooks.Ping() })
		return eachErr
	})
	env := openCoreHooks(t, task, nil)
	env.install(t, "first", "test.core-hooks-signal", &coreHookSet{Ping: func() error { calls.add("first"); return boom }})
	env.install(t, "second", "test.core-hooks-signal", &coreHookSet{Ping: func() error { calls.add("second"); return nil }})
	id := startTask(t, env.root, task)
	env.harness.Resume()
	<-entered
	if _, err := env.harness.AbortTask(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	waitSettled(t, env.harness, id)
	if !errors.Is(eachErr, boom) {
		t.Fatalf("Each returned %v after the signal, want the handler's error", eachErr)
	}
	if got := calls.all(); !reflect.DeepEqual(got, []string{"first"}) {
		t.Fatalf("calls = %v, want only the first: the error stops the iteration", got)
	}
	// scheduler.ts:1141: the signalled error is not reported as the hook's failure; the only report is the task's own fault,
	// which its abort handler left without an outcome (scheduler.ts #step reports task_faulted).
	for _, report := range env.reported() {
		if want := fmt.Sprintf("Abort handler of task %d returned without a terminal outcome", id); report.Error() != want {
			t.Fatalf("reports = %v, want only %q: a signalled error propagates instead", env.reported(), want)
		}
	}
}

// scheduler.ts:1132: each awaits the phase agent first, so a failed agent resolution (a settings getter that throws) rejects each with that error before
// any handler runs.
// mutation-checked: ignoring the agent resolution error runs the handlers on an unresolved agent and returns nil.
func TestCoreTaskHooksRejectWithAFailedAgentResolution(t *testing.T) {
	calls := &coreCalls{}
	var eachErr error
	task := coreHookTask("test.core-hooks-agent", func(_ context.Context, runtime coreHookRuntime) error {
		eachErr = runtime.Hooks().Each("ping", func(hooks *coreHookSet) error { return hooks.Ping() })
		return nil
	})
	// The core reads the settings once when it opens the Harness, so the getter starts to throw only after that.
	var broken atomic.Bool
	env := openCoreHooks(t, task, func() *harness.HarnessSettings {
		if broken.Load() {
			panic(errors.New("settings broke"))
		}
		return nil
	})
	broken.Store(true)
	env.install(t, "handler", "test.core-hooks-agent", &coreHookSet{Ping: func() error { calls.add("handler"); return nil }})
	id := startTask(t, env.root, task)
	env.harness.Resume()
	waitSettled(t, env.harness, id)
	if eachErr == nil || !strings.Contains(eachErr.Error(), "settings broke") {
		t.Fatalf("Each returned %v, want the settings error", eachErr)
	}
	if got := calls.all(); len(got) != 0 {
		t.Fatalf("calls = %v, want none: the agent never resolved", got)
	}
}
