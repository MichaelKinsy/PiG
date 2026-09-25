package pico3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDescribeCannotProjectPrivateSlotMemos(t *testing.T) {
	gate := &testGate{}
	kind := &Kind{Name: "private-slot", Slot: func(JsonValue) JsonObject { return JsonObject{"progress": 0} }, Describe: func(_ Task, slot JsonObject) JsonValue { return slot }}
	kind.Initial = func(ctx context.Context, task Task, rt *Runtime) (Step, error) {
		_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			slot := must(tx.Slot(TaskRef{Id: task.Id, Kind: kind}))
			slot.Set("progress", 1)
			slot.Set("memos", JsonObject{"secret": "never-project"})
			return nil, nil
		})
		if err != nil {
			return Step{}, err
		}
		return done(Completed(nil)), gate.Wait(ctx)
	}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
	ref := createTestTask(t, env, kind, nil)
	gate.Arrivals(t, 1)
	watch := must(env.root.Watch(bg))
	defer watch.Stop()
	equal(t, obj(obj(watch.View, "tasks"), fmt.Sprint(ref.Id))["status"], JsonObject{"progress": 1}, "public status")
	if strings.Contains(string(mustJSON(watch.View)), "never-project") {
		t.Fatal("memo leaked into view")
	}
	gate.Open()
	env.untilTerminal(ref.Id)
}

func TestRuntimeKindRegistrationSeedsLoadedDocuments(t *testing.T) {
	env := openEnv(t, openOptions{})
	must(env.root.Rewindable(bg))
	observed := make(chan JsonObject, 1)
	kind := &Kind{Name: "runtime-default", Config: &KindConfig{Rewindable: []ConfigKey{{Key: "enabled", Default: false}}}, Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		value, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, current Task) (any, error) {
			return JsonObject{"facade": must(must(tx.Config(current.ConversationId)).Get("enabled")), "document": must(tx.Snapshot(RewindableDoc(current.ConversationId)))["enabled"]}, nil
		})
		if err != nil {
			return Step{}, err
		}
		observed <- value.(JsonObject)
		return done(Completed(nil)), nil
	}}
	must(env.h.RegisterTaskKind(kind))
	ref := createTestTask(t, env, kind, nil)
	equal(t, env.untilTerminal(ref.Id).Outcome.Status, OutcomeCompleted, "task")
	equal(t, <-observed, JsonObject{"facade": false, "document": false}, "seeded defaults")
}

func TestRunningKindKeepsConfigAuthorityAfterUnregister(t *testing.T) {
	gate := &testGate{}
	kind := &Kind{Name: "active-reload", Config: &KindConfig{Sticky: []ConfigKey{{Key: "leaseValue", Default: "old-default"}}}, Initial: func(ctx context.Context, task Task, rt *Runtime) (Step, error) {
		if err := gate.Wait(ctx); err != nil {
			return Step{}, err
		}
		value, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			config, err := tx.Config(task.ConversationId)
			if err != nil {
				return nil, err
			}
			return config.Get("leaseValue")
		})
		if err != nil {
			return Step{}, err
		}
		return done(Completed(value)), nil
	}}
	env := openEnv(t, openOptions{})
	off := must(env.h.RegisterTaskKind(kind))
	ref := createTestTask(t, env, kind, nil)
	gate.Arrivals(t, 1)
	off()
	gate.Open()
	equal(t, env.untilTerminal(ref.Id).Outcome, &Outcome{Status: OutcomeCompleted, Result: "old-default"}, "captured authority")
}

func TestUnregisteredDependencyTaskWaitsForReplacement(t *testing.T) {
	gate := &testGate{}
	blocker := &Kind{Name: "blocker", Initial: func(ctx context.Context, _ Task, _ *Runtime) (Step, error) {
		return done(Completed(nil)), gate.Wait(ctx)
	}}
	old := quickKind(t, "pending-reload", func(Task) JsonValue { return "original" })
	env := openEnv(t, openOptions{taskKinds: []*Kind{blocker}})
	off := must(env.h.RegisterTaskKind(old))
	refs := must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) ([]TaskRef, error) {
		dependency := must(tx.CreateTask(blocker, nil, TaskOptions{ConversationId: new(Id(1)), Background: true}))
		target := must(tx.CreateTask(old, nil, TaskOptions{ConversationId: new(Id(1)), Background: true, After: []Id{dependency.Id}}))
		return []TaskRef{dependency, target}, nil
	}))
	gate.Arrivals(t, 1)
	off()
	gate.Open()
	env.untilTerminal(refs[0].Id)
	equal(t, must(env.h.GetTask(bg, refs[1].Id)).Status, TaskPending, "pending without kind")
	replacement := quickKind(t, "pending-reload", func(Task) JsonValue { return "replacement" })
	must(env.h.RegisterTaskKind(replacement))
	equal(t, env.untilTerminal(refs[1].Id).Outcome, &Outcome{Status: OutcomeCompleted, Result: "replacement"}, "replacement executes")
}

func TestTaskKindReplacementStalesTokensAndOldUnsubscribe(t *testing.T) {
	var calls []string
	makeKind := func(label string) *Kind {
		return &Kind{Name: "reload-kind", Initial: func(context.Context, Task, *Runtime) (Step, error) {
			calls = append(calls, label)
			return done(Completed(label)), nil
		}}
	}
	env := openEnv(t, openOptions{})
	old := makeKind("old")
	off := must(env.h.RegisterTaskKind(old))
	off()
	off()
	replacement := makeKind("replacement")
	must(env.h.RegisterTaskKind(replacement))
	off()
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		return tx.CreateTask(old, nil, TaskOptions{ConversationId: new(Id(1))})
	})
	if _, ok := errors.AsType[*Forbidden](err); !ok {
		t.Fatalf("stale kind: %v", err)
	}
	ref := createTestTask(t, env, replacement, nil)
	equal(t, env.untilTerminal(ref.Id).Outcome, &Outcome{Status: OutcomeCompleted, Result: "replacement"}, "current outcome")
	equal(t, calls, []string{"replacement"}, "only replacement")
}
