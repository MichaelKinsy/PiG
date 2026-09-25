package pico3

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Upstream: packages/agent/test/harness/pico3/busy.test.ts, all eight cases.
func TestBusyOverflowTerminalNotice(t *testing.T) {
	models := newFake(fakeOptions{respond: echoScript})
	models.model.Capabilities.ContextWindow = 50
	models.model.Capabilities.MaxOutputTokens = 10
	env := openEnv(t, openOptions{models: models, root: &RootSpec{Rewindable: JsonObject{"model": testModel, "keepRecent": 1000}}})
	input := env.wait(env.send(env.root, strings.Repeat("x", 2000)))
	equal(t, input.Reason, "failed", "overflow reason")
	equal(t, input.Detail, "overflow", "overflow detail")
	if !slices.ContainsFunc(env.entries(), func(entry Entry) bool { return entry.Kind == "pi.notice" }) {
		t.Fatal("terminal notice missing")
	}
	equal(t, arr(env.sticky(), "inbox"), []any{}, "inbox drained")
	equal(t, env.sticky()["turn"], JsonObject{"tools": []any{}}, "turn reset")
}

func TestBusyIdleJobNotice(t *testing.T) {
	host := newFakeHost()
	env := openEnv(t, openOptions{processHost: host})
	job := createTestTask(t, env, Kinds.Job, JsonObject{"command": "x", "args": []any{}, "cwd": "/", "notify": true, "rerun": false})
	task := env.untilPhase("pi.job", "running")
	host.exit(str(task.Checkpoint, "key"), 0)
	equal(t, env.untilTerminal(job.Id).Outcome.Status, OutcomeCompleted, "job completion")
	if !slices.ContainsFunc(env.entries(), func(entry Entry) bool { return entry.Kind == "pi.notice" }) {
		t.Fatal("idle job notice missing")
	}
	equal(t, arr(env.sticky(), "inbox"), []any{}, "background job is not busy")
}

func TestBusySuccessorPlacesQueuedJobNotice(t *testing.T) {
	gate := &testGate{}
	host := newFakeHost()
	slow := newTool("slow", toolOptions{gate: gate})
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{slow.ToolDeclaration}, processHost: host})
	input := env.send(env.root, "tool:slow")
	gate.Arrivals(t, 1)
	job := createTestTask(t, env, Kinds.Job, JsonObject{"command": "x", "args": []any{}, "cwd": "/", "notify": true, "rerun": false})
	running := env.untilPhase("pi.job", "running")
	host.exit(str(running.Checkpoint, "key"), 0)
	env.untilTerminal(job.Id)
	equal(t, len(arr(env.sticky(), "inbox")), 1, "notice queued while tool runs")
	gate.Open()
	env.wait(input)
	equal(t, arr(env.sticky(), "inbox"), []any{}, "posttools drains notice")
	var kinds []string
	for _, entry := range env.entries() {
		kinds = append(kinds, entry.Kind)
	}
	notice, result, last := slices.Index(kinds, "pi.notice"), slices.Index(kinds, "pi.tool_result"), -1
	for i, kind := range kinds {
		if kind == "pi.assistant" {
			last = i
		}
	}
	if notice <= result || notice >= last {
		t.Fatalf("notice must follow tool result and precede continuation: %v", kinds)
	}
}

func TestBusyFinalClosureDrainsWrite(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	input := env.send(env.root, "A")
	gate.Arrivals(t, 1)
	write := must(env.root.Write(bg, NewEntry{Kind: "note"}))
	gate.Open()
	env.wait(input)
	equal(t, env.input(write).Status, InputDone, "queued write settled")
	equal(t, arr(env.sticky(), "inbox"), []any{}, "nothing stranded")
}

func TestBusyNextInflightIsContractFault(t *testing.T) {
	reports := make(chan error, 4)
	kind := &Kind{Name: "roles", Inflight: []string{"fly"}, Initial: func(context.Context, Task, *Runtime) (Step, error) {
		return Step{Next: Checkpoint{"phase": "fly"}}, nil
	}, Phases: map[string]PhaseHandler{
		"fly": func(context.Context, Task, *Runtime) (Step, error) {
			return done(Failed(JsonObject{"reason": "declared"})), nil
		},
		"ok": func(context.Context, Task, *Runtime) (Step, error) { return done(Completed(nil)), nil }}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}, onReport: func(err error) { reports <- err }})
	task := env.untilTerminal(createTestTask(t, env, kind, nil).Id)
	equal(t, task.Outcome.Status, OutcomeFaulted, "contract fault outcome")
	if !strings.Contains(task.Outcome.Error, "in-flight phase fly") {
		t.Fatal(task.Outcome.Error)
	}
	select {
	case err := <-reports:
		if _, ok := errors.AsType[*TaskContractFault](err); !ok {
			t.Fatalf("report %T: %v", err, err)
		}
	default:
		t.Fatal("contract fault not reported")
	}
}

func TestBusyInflightCheckpointEntersOnlyAfterReopen(t *testing.T) {
	gate := &testGate{}
	var mu sync.Mutex
	var entered []string
	record := func(s string) { mu.Lock(); entered = append(entered, s); mu.Unlock() }
	kind := &Kind{Name: "fly", Inflight: []string{"fly"}, Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			return nil, tx.Checkpoint(Checkpoint{"phase": "fly"})
		})
		if err != nil {
			return Step{}, err
		}
		record("initial")
		return done(Completed(nil)), gate.Wait(ctx)
	}, Phases: map[string]PhaseHandler{"fly": func(context.Context, Task, *Runtime) (Step, error) { record("fly"); return done(Completed(nil)), nil }}}
	env := openEnv(t, openOptions{backend: "jsonl", taskKinds: []*Kind{kind}})
	ref := createTestTask(t, env, kind, nil)
	gate.Arrivals(t, 1)
	env.crash()
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, taskKinds: []*Kind{kind}})
	next.untilTerminal(ref.Id)
	mu.Lock()
	defer mu.Unlock()
	equal(t, entered, []string{"initial", "fly"}, "inflight phase entry")
}

func TestBusyInvalidKindsRemainReadableAndFaulted(t *testing.T) {
	reports := make(chan error, 8)
	cases := []struct {
		name, want string
		initial    PhaseHandler
	}{
		{"thrower", "kaboom", func(context.Context, Task, *Runtime) (Step, error) { return Step{}, errors.New("kaboom") }},
		{"badstep", "invalid step", func(context.Context, Task, *Runtime) (Step, error) { return Step{}, nil }},
		{"baddone", "invalid completion", func(context.Context, Task, *Runtime) (Step, error) { return done(Completion{Status: "weird"}), nil }},
		{"badphase", "unknown phase zzz", func(context.Context, Task, *Runtime) (Step, error) {
			return Step{Next: Checkpoint{"phase": "zzz"}}, nil
		}},
	}
	var kinds []*Kind
	for _, tc := range cases {
		kinds = append(kinds, &Kind{Name: tc.name, Initial: tc.initial, Phases: map[string]PhaseHandler{"x": func(context.Context, Task, *Runtime) (Step, error) { return done(Completed(nil)), nil }}})
	}
	env := openEnv(t, openOptions{taskKinds: kinds, onReport: func(err error) { reports <- err }})
	for i, kind := range kinds {
		ref := createTestTask(t, env, kind, nil)
		task := env.untilTerminal(ref.Id)
		equal(t, task.Outcome.Status, OutcomeFaulted, kind.Name)
		if !strings.Contains(task.Outcome.Error, cases[i].want) {
			t.Fatalf("%s error %q", kind.Name, task.Outcome.Error)
		}
		equal(t, must(env.h.GetTask(bg, ref.Id)).Status, TaskTerminal, "fault remains readable")
	}
	equal(t, len(reports), len(cases), "each fault reported")
	equal(t, resultOf(must(env.h.GetTask(bg, 1))), JsonObject(nil), "fault has no declared result")
}

func TestBusyAtomicTransitionBuilder(t *testing.T) {
	var tries atomic.Int64
	kind := &Kind{Name: "builder", Initial: func(context.Context, Task, *Runtime) (Step, error) {
		return Step{Build: func(context.Context, *Tx, Task) (Transition, error) {
			if tries.Add(1) < 3 {
				return Transition{Retry: true}, nil
			}
			return Transition{Checkpoint: Checkpoint{"phase": "x"}}, nil
		}}, nil
	}, Phases: map[string]PhaseHandler{
		"x": func(context.Context, Task, *Runtime) (Step, error) {
			return Step{Build: func(context.Context, *Tx, Task) (Transition, error) {
				completion := Completed(JsonObject{"done": true})
				return Transition{Completion: &completion}, nil
			}}, nil
		}}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
	task := env.untilTerminal(createTestTask(t, env, kind, nil).Id)
	equal(t, tries.Load(), int64(3), "builder retries")
	equal(t, task.Outcome.Status, OutcomeCompleted, "builder completion")
	equal(t, task.Outcome.Result, JsonObject{"done": true}, "atomic completion result")
}
