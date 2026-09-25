package pico3

import (
	"context"
	"errors"
	"testing"
)

func createTestTask(t *testing.T, env *testEnv, kind *Kind, input JsonValue) TaskRef {
	t.Helper()
	return must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (TaskRef, error) {
		return tx.CreateTask(kind, input, TaskOptions{ConversationId: new(Id(1)), Background: true})
	}))
}

func TestPluginInvalidJSONFailsWithoutFault(t *testing.T) {
	env := openEnv(t, openOptions{plugins: map[string]PluginHandler{"invalid": func(context.Context, JsonValue, *ToolApi) (JsonValue, error) { return make(chan int), nil }}})
	ref := createTestTask(t, env, Kinds.Plugin, JsonObject{"handler": "invalid", "input": nil})
	task := env.untilTerminal(ref.Id)
	equal(t, task.Outcome.Status, OutcomeFailed, "declared outcome")
	equal(t, str(asObject(task.Outcome.Failure), "reason"), "threw", "failure reason")
}

func TestSuspendAndRecoverSafeTool(t *testing.T) {
	gate := &testGate{}
	tool := newTool("recoverable", toolOptions{replay: "safe", gate: gate})
	env := openEnv(t, openOptions{backend: "jsonl", tools: []*ToolDeclaration{tool.ToolDeclaration}})
	input := env.send(env.root, "tool:recoverable")
	task := env.untilPhase("pi.tool", "started")
	gate.Arrivals(t, 1)
	check(t, env.h.Suspend(bg))
	env.closed = true
	replacement := newTool("recoverable", toolOptions{replay: "safe"})
	reopened := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, tools: []*ToolDeclaration{replacement.ToolDeclaration}})
	reopened.idle()
	equal(t, replacement.calls.Load(), int64(1), "replayed once")
	equal(t, must(reopened.h.GetTask(bg, task.Id)).Outcome.Status, OutcomeCompleted, "recovered task")
	equal(t, reopened.input(input.Id).Status, InputDone, "recovered input")
}

func TestHoldUsesReplacementKind(t *testing.T) {
	env := openEnv(t, openOptions{})
	release := must(env.h.Hold())
	kind := &Kind{Name: "held", Initial: func(context.Context, Task, *Runtime) (Step, error) { return done(Completed("old")), nil }}
	off := must(env.h.RegisterTaskKind(kind))
	ref := createTestTask(t, env, kind, nil)
	_, err := env.root.Write(bg, NewEntry{Kind: "while.held"})
	check(t, err)
	equal(t, must(env.h.GetTask(bg, ref.Id)).Status, TaskPending, "held task")
	off()
	replacement := &Kind{Name: "held", Initial: func(context.Context, Task, *Runtime) (Step, error) { return done(Completed("replacement")), nil }}
	must(env.h.RegisterTaskKind(replacement))
	release()
	release()
	equal(t, env.untilTerminal(ref.Id).Outcome.Result, "replacement", "replacement code")
}

func TestRuntimeExpiresAfterPhaseAndAbortPanic(t *testing.T) {
	for _, panicAbort := range []bool{false, true} {
		t.Run(map[bool]string{false: "phase", true: "abort panic"}[panicAbort], func(t *testing.T) {
			gate := &testGate{}
			captured := make(chan *Runtime, 1)
			kind := &Kind{Name: "lifetime", Phases: map[string]PhaseHandler{"later": func(ctx context.Context, _ Task, _ *Runtime) (Step, error) {
				return done(Completed(nil)), gate.Wait(ctx)
			}}, Initial: func(_ context.Context, _ Task, rt *Runtime) (Step, error) {
				if !panicAbort {
					captured <- rt
				}
				return Step{Next: Checkpoint{"phase": "later"}}, nil
			}, Abort: func(_ context.Context, _ Task, rt *Runtime) (AbortClosure, error) {
				captured <- rt
				panic("abort failed")
			}}
			env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
			ref := createTestTask(t, env, kind, nil)
			gate.Arrivals(t, 1)
			if panicAbort {
				_, err := env.h.AbortTask(bg, ref.Id)
				check(t, err)
				env.untilTerminal(ref.Id)
			}
			rt := <-captured
			_, err := rt.Commit(bg, func(context.Context, *Tx, Task) (any, error) { return nil, nil })
			if _, ok := errors.AsType[*Forbidden](err); !ok {
				t.Fatalf("expired runtime: %v", err)
			}
			gate.Open()
		})
	}
}

func TestJobReconcilesKnownAndForgottenProcesses(t *testing.T) {
	for _, forgotten := range []bool{false, true} {
		t.Run(map[bool]string{false: "exited", true: "unknown"}[forgotten], func(t *testing.T) {
			host := newFakeHost()
			env := openEnv(t, openOptions{backend: "jsonl", processHost: host})
			ref := createTestTask(t, env, Kinds.Job, JsonObject{"command": "x", "args": []any{}, "cwd": "/", "notify": true, "rerun": false})
			task := env.untilPhase("pi.job", "running")
			key := str(task.Checkpoint, "key")
			reopen := env.crash()
			if forgotten {
				host.forget(key)
			} else {
				host.exit(key, 7)
			}
			next := reopen()
			terminal := next.untilTerminal(ref.Id)
			equal(t, host.Starts(), 1, "no duplicate spawn")
			if forgotten {
				equal(t, str(asObject(terminal.Outcome.Failure), "reason"), "interrupted", "forgotten process")
			} else {
				equal(t, terminal.Outcome.Status, OutcomeCompleted, "exit outcome")
				equal(t, asObject(terminal.Outcome.Result)["exitCode"], 7, "exit code")
			}
		})
	}
}

func TestLineContextRejectsReentry(t *testing.T) {
	env := openEnv(t, openOptions{})
	var captured context.Context
	type callerKey struct{}
	caller := context.WithValue(bg, callerKey{}, "caller-value")
	_, err := env.root.Commit(caller, func(ctx context.Context, _ *Tx) (any, error) {
		equal(t, ctx.Value(callerKey{}), "caller-value", "propagated context")
		captured = ctx
		return nil, nil
	})
	check(t, err)
	checks := []func() error{
		func() error {
			_, err := env.root.Commit(captured, func(context.Context, *Tx) (any, error) { return nil, nil })
			return err
		},
		func() error { _, err := env.root.Write(captured, NewEntry{Kind: "note"}); return err },
		func() error { _, err := env.root.Watch(captured); return err },
		func() error { _, err := env.h.Root(captured); return err },
		func() error { _, err := env.h.GetTask(captured, 999999); return err },
		func() error { _, err := env.h.WaitForTask(captured, 999999); return err },
		func() error { return env.root.WaitForIdle(captured) },
	}
	for index, attempt := range checks {
		var nested *NestedLineOperation
		if err := attempt(); !errors.As(err, &nested) {
			t.Fatalf("operation %d: %v", index, err)
		}
	}
}

func TestPluginTaskAPIAndOwnedConversation(t *testing.T) {
	childKind := quickKind(t, "child", func(Task) JsonValue { return "child result" })
	env := openEnv(t, openOptions{taskKinds: []*Kind{childKind}, plugins: map[string]PluginHandler{"parent": func(ctx context.Context, _ JsonValue, api *ToolApi) (JsonValue, error) {
		ref, err := api.Task(ctx, childKind, nil, TaskOptions{Background: true})
		if err != nil {
			return nil, err
		}
		settled, err := api.WaitForTask(ctx, ref)
		if err != nil {
			return nil, err
		}
		observed, err := api.GetTask(ctx, ref)
		if err != nil {
			return nil, err
		}
		if observed.Id != ref.Id || settled.Outcome.Result != "child result" {
			return nil, errors.New("task API lost result")
		}
		child, err := api.Conversation(ctx, OwnedConversationSpec{Rewindable: JsonObject{"model": testModel}})
		if err != nil {
			return nil, err
		}
		input, err := child.Send(ctx, SendInput{Content: "owned"})
		if err != nil {
			return nil, err
		}
		answer, err := input.Wait(ctx)
		if err != nil {
			return nil, err
		}
		record, err := input.Result(ctx)
		if err != nil {
			return nil, err
		}
		if answer.Status != InputDone || record.Id != input.Id {
			return nil, errors.New("owned input did not settle")
		}
		return JsonObject{"child": child.Id}, nil
	}}})
	ref := createTestTask(t, env, Kinds.Plugin, JsonObject{"handler": "parent", "input": nil})
	task := env.untilTerminal(ref.Id)
	equal(t, task.Outcome.Status, OutcomeCompleted, "parent plugin")
	equal(t, len(task.Owns), 1, "owned conversation")
}
