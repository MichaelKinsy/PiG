package harness

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

type hookRunnerHooks struct{ Ping func() error }

type hookRunnerState struct {
	Phase string `json:"phase"`
}

type (
	hookRunnerRuntime = durable.TaskRuntime[durable.JsonValue, hookRunnerState, durable.JsonValue, *hookRunnerHooks]
	hookRunnerRecord  = durable.RunningTask[durable.JsonValue, hookRunnerState, durable.JsonValue]
)

// hookRunnerTask is a one-phase task whose phase runs body with its runtime and completes with its error, so a Faulted outcome carries what Each returned.
func hookRunnerTask(body func(ctx context.Context, runtime hookRunnerRuntime) error) durable.AnyTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, hookRunnerState, durable.JsonValue, *hookRunnerHooks]{
		Name:    "test.hook-runner",
		Version: 1,
		Initial: func(durable.JsonValue) hookRunnerState { return hookRunnerState{Phase: "a"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, hookRunnerState, durable.JsonValue, *hookRunnerHooks]{
			"a": func(ctx context.Context, _ hookRunnerRecord, runtime hookRunnerRuntime) error {
				if err := body(ctx, runtime); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(durable.Tx, hookRunnerRecord) (*durable.NextTaskState[hookRunnerState, durable.JsonValue], error) {
					return completed[hookRunnerState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: tkNoopAbort[durable.JsonValue, hookRunnerState, durable.JsonValue, *hookRunnerHooks],
	})
}

// Pi HookRunner.each: packages/durable/src/types.ts:158-163 and scheduler.ts:1131-1147: every handler set of the task's name runs in extension order, and an
// ordinary throw from invoke is reported (OnReport) while the next handler runs. Pi skips a handler set without the named member inside each
// (scheduler.ts:1135); Go has no keyed member access, so invoke selects the member and skips the unset one (durable/types.go HookRunner), as here.
func TestHookRunnerEachReportsAnOrdinaryErrorAndRunsTheNextHandler(t *testing.T) {
	boom := errors.New("boom")
	calls := &syncList[string]{}
	var eachErr error
	task := hookRunnerTask(func(_ context.Context, runtime hookRunnerRuntime) error {
		eachErr = runtime.Hooks().Each("ping", func(hooks *hookRunnerHooks) error {
			if hooks == nil || hooks.Ping == nil {
				return nil
			}
			return hooks.Ping()
		})
		return nil
	})
	opened := tkOpenRoot(t, []durable.AnyTask{task})
	addHooks(t, opened.registry, task, &hookRunnerHooks{Ping: func() error { calls.add("first"); return boom }})
	addHooks(t, opened.registry, task, &hookRunnerHooks{})
	addHooks(t, opened.registry, task, &hookRunnerHooks{Ping: func() error { calls.add("third"); return nil }})
	id := tkStart(t, opened.root, task)
	opened.harness.Resume()
	expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeCompleted(nil))
	if eachErr != nil {
		t.Fatalf("Each returned %v for an ordinary handler error, want nil", eachErr)
	}
	if got, want := calls.all(), []string{"first", "third"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("handler calls = %v, want %v: the failing handler must not stop the next one", got, want)
	}
	reports := opened.reports.all()
	if len(reports) != 1 || !errors.Is(reports[0], boom) {
		t.Fatalf("reports = %v, want exactly the first handler's error", reports)
	}
	mustClose(t, opened.harness)
}

// A panicking handler is an ordinary throw too (scheduler.ts:1139-1146 catches whatever invoke throws): it is reported and the next handler runs.
func TestHookRunnerEachTreatsAPanickingHandlerAsAnOrdinaryError(t *testing.T) {
	calls := &syncList[string]{}
	var eachErr error
	task := hookRunnerTask(func(_ context.Context, runtime hookRunnerRuntime) error {
		eachErr = runtime.Hooks().Each("ping", func(hooks *hookRunnerHooks) error { return hooks.Ping() })
		return nil
	})
	opened := tkOpenRoot(t, []durable.AnyTask{task})
	addHooks(t, opened.registry, task, &hookRunnerHooks{Ping: func() error { calls.add("panics"); panic("handler panic") }})
	addHooks(t, opened.registry, task, &hookRunnerHooks{Ping: func() error { calls.add("after"); return nil }})
	id := tkStart(t, opened.root, task)
	opened.harness.Resume()
	expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeCompleted(nil))
	if eachErr != nil || !reflect.DeepEqual(calls.all(), []string{"panics", "after"}) {
		t.Fatalf("Each returned %v with calls %v, want nil and both handlers called", eachErr, calls.all())
	}
	if len(opened.reports.all()) != 1 {
		t.Fatalf("reports = %v, want the panic reported once", opened.reports.all())
	}
	mustClose(t, opened.harness)
}

// scheduler.ts:1122-1123,1133: each awaits agent(), which rejects once the invocation has ended, so no handler runs after the end.
func TestHookRunnerEachRejectsAfterTheInvocationEnds(t *testing.T) {
	calls := &syncList[string]{}
	captured := &syncValue[hookRunnerRuntime]{}
	task := hookRunnerTask(func(_ context.Context, runtime hookRunnerRuntime) error {
		captured.put(runtime)
		// Resolve the agent inside the invocation so the rejection below comes from the ended check, not from a failed resolution.
		return runtime.Hooks().Each("ping", func(*hookRunnerHooks) error { return nil })
	})
	opened := tkOpenRoot(t, []durable.AnyTask{task})
	addHooks(t, opened.registry, task, &hookRunnerHooks{Ping: func() error { calls.add("late"); return nil }})
	id := tkStart(t, opened.root, task)
	opened.harness.Resume()
	expectOutcome(t, tkWaitOutcome(t, opened.harness, id), outcomeCompleted(nil))
	runtime, _ := captured.get()
	// The step after the phase ends the invocation (scheduler.ts:1100-1107); the end marks it ended before it cancels the signal.
	<-runtime.Signal().Done()
	err := runtime.Hooks().Each("ping", func(hooks *hookRunnerHooks) error { return hooks.Ping() })
	tkContainsError(t, err, "invocation has ended")
	if got := calls.all(); len(got) != 0 {
		t.Fatalf("handler calls = %v after the invocation ended, want none", got)
	}
	if reports := opened.reports.all(); len(reports) != 0 {
		t.Fatalf("reports = %v, want none: the ended invocation rejects before any handler", reports)
	}
	mustClose(t, opened.harness)
}

// scheduler.ts:1141: once the invocation is signalled (abortTask, harness close), the error from invoke propagates instead of being reported and the
// remaining handlers do not run.
func TestHookRunnerEachPropagatesTheErrorOnceTheInvocationIsSignalled(t *testing.T) {
	boom := errors.New("after the signal")
	calls := &syncList[string]{}
	entered := deferred()
	var eachErr error
	task := hookRunnerTask(func(ctx context.Context, runtime hookRunnerRuntime) error {
		// The agent resolves at first use and stays fixed for the phase, so Each below does not depend on the cancelled invocation context.
		if _, err := runtime.Agent(ctx); err != nil {
			return err
		}
		entered.resolve()
		<-runtime.Signal().Done()
		eachErr = runtime.Hooks().Each("ping", func(hooks *hookRunnerHooks) error { return hooks.Ping() })
		return eachErr
	})
	opened := tkOpenRoot(t, []durable.AnyTask{task})
	addHooks(t, opened.registry, task, &hookRunnerHooks{Ping: func() error { calls.add("first"); return boom }})
	addHooks(t, opened.registry, task, &hookRunnerHooks{Ping: func() error { calls.add("second"); return nil }})
	id := tkStart(t, opened.root, task)
	opened.harness.Resume()
	if err := entered.wait(testContext); err != nil {
		t.Fatal(err)
	}
	aborting := tkMarkDurably(t, opened.harness, id)
	<-aborting
	_ = tkWaitOutcome(t, opened.harness, id)
	if !errors.Is(eachErr, boom) {
		t.Fatalf("Each returned %v after the signal, want the handler's error", eachErr)
	}
	if got := calls.all(); !reflect.DeepEqual(got, []string{"first"}) {
		t.Fatalf("handler calls = %v, want only the first: the error stops the iteration", got)
	}
	if reports := opened.reports.all(); len(reports) != 0 {
		t.Fatalf("reports = %v, want none: a signalled error propagates instead", reports)
	}
	mustClose(t, opened.harness)
}
