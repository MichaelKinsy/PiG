package pico3

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReadAfterWriteRejectsEveryAffectedScan(t *testing.T) {
	env := openEnv(t, openOptions{})
	for _, read := range []func(*Tx) error{
		func(tx *Tx) error { _, err := tx.NewestEntry(1, NewestOptions{}); return err },
		func(tx *Tx) error { _, err := tx.Context(1, nil); return err },
		func(tx *Tx) error { _, err := tx.ScanEntries(EntryScan{ConversationId: 1, Limit: 5}); return err },
	} {
		_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
			if _, err := tx.Write(1, NewEntry{Kind: "note"}); err != nil {
				return nil, err
			}
			caught := read(tx)
			if _, ok := errors.AsType[*ReadAfterWrite](caught); !ok {
				t.Fatalf("caught failure: %v", caught)
			}
			return "callback completed", nil
		})
		if _, ok := errors.AsType[*ReadAfterWrite](err); !ok {
			t.Fatalf("unpoisoned transaction: %v", err)
		}
		equal(t, len(env.entries()), 0, "append rolled back")
	}
	observed := make(chan error, 1)
	probe := &Kind{Name: "scan-after", Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, current Task) (any, error) {
			if _, err := tx.Write(current.ConversationId, NewEntry{Kind: "note"}); err != nil {
				return nil, err
			}
			return tx.NewestEntry(current.ConversationId, NewestOptions{WithHead: true})
		})
		observed <- err
		return done(Completed(nil)), nil
	}}
	must(env.h.RegisterTaskKind(probe))
	env.untilTerminal(createTestTask(t, env, probe, nil).Id)
	if _, ok := errors.AsType[*ReadAfterWrite](<-observed); !ok {
		t.Fatal("runtime scan not poisoned")
	}
}

func TestConfigDefaultsNullAndResetVisibleToRuntimeAndFork(t *testing.T) {
	observed := make(chan []any, 1)
	kind := &Kind{Name: "plan-null", Config: &KindConfig{Rewindable: []ConfigKey{{Key: "planMode", Default: false}, {Key: "note", Default: "n"}}}, Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		value, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, current Task) (any, error) {
			return []any{must(must(tx.Config(current.ConversationId)).Get("planMode")), must(tx.Snapshot(RewindableDoc(current.ConversationId)))["planMode"]}, nil
		})
		if err != nil {
			return Step{}, err
		}
		observed <- value.([]any)
		return done(Completed(nil)), nil
	}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
	cfg := must(env.root.Config().Get(bg))
	equal(t, cfg["planMode"], false, "default")
	equal(t, cfg["note"], "n", "note default")
	equal(t, cfg["model"], testModel, "model")
	equal(t, cfg["threshold"], 0, "threshold")
	env.untilTerminal(createTestTask(t, env, kind, nil).Id)
	equal(t, <-observed, []any{false, false}, "runtime defaults")
	check(t, env.root.Config().Set(bg, JsonObject{"note": nil}))
	equal(t, must(env.root.Config().Get(bg))["note"], nil, "stored null")
	check(t, env.root.Config().Reset(bg, []string{"note"}))
	equal(t, must(env.root.Config().Get(bg))["note"], "n", "reset")
	if err := env.root.Config().Set(bg, JsonObject{"bogus": 1}); err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("unknown key: %v", err)
	}
	fork := must(env.root.Fork(bg, nil, ConversationSpec{}))
	equal(t, must(fork.Config().Get(bg))["planMode"], false, "fork defaults")
}

func TestConfigCollisionRegistrationDoesNotLeakPartialKeys(t *testing.T) {
	clash := quickKind(t, "clash", func(Task) JsonValue { return nil })
	clash.Config = &KindConfig{Sticky: []ConfigKey{{Key: "threshold", Default: 1}}}
	if harness, err := OpenHarness(bg, NewMemoryStorage(), HarnessOptions{Models: newFake(fakeOptions{respond: echoScript}), TaskKinds: []*Kind{clash}}); err == nil {
		check(t, harness.Close(bg))
		t.Fatal("open accepted collision")
	}
	env := openEnv(t, openOptions{})
	partial := *clash
	partial.Name = "partial-clash"
	partial.Config = &KindConfig{Rewindable: []ConfigKey{{Key: "partialKey", Default: 1}}, Sticky: clash.Config.Sticky}
	if _, err := env.h.RegisterTaskKind(&partial); err == nil {
		t.Fatal("partial collision accepted")
	}
	if _, present := must(env.root.Config().Get(bg))["partialKey"]; present {
		t.Fatal("failed registration leaked key")
	}
	owner := quickKind(t, "partial-owner", func(Task) JsonValue { return nil })
	owner.Config = &KindConfig{Rewindable: []ConfigKey{{Key: "partialKey", Default: 2}}}
	off := must(env.h.RegisterTaskKind(owner))
	equal(t, must(env.root.Config().Get(bg))["partialKey"], 2, "new owner default")
	off()
}

func TestNamespaceSlicesCannotClobberOtherNamespaces(t *testing.T) {
	env := openEnv(t, openOptions{})
	a := must(env.h.Namespace("a", NamespaceDefaults{Rewindable: JsonObject{"count": 0}}, nil))
	b := must(env.h.Namespace("b", NamespaceDefaults{Rewindable: JsonObject{"count": 10}}, nil))
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		must(tx.Plugins(a)).Set("count", 1)
		must(tx.Plugins(b)).Set("count", 11)
		return nil, nil
	})
	check(t, err)
	_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { must(tx.Plugins(a)).Set("typo", true); return nil, nil })
	if _, ok := errors.AsType[*TypeError](err); !ok {
		t.Fatalf("undeclared field: %v", err)
	}
	equal(t, obj(must(env.root.Rewindable(bg)), "plugins"), JsonObject{"a": JsonObject{"count": 1}, "b": JsonObject{"count": 11}}, "isolated slices")
}

func TestOrdinaryTaskReadsInheritedForkEntry(t *testing.T) {
	observed := make(chan []Id, 1)
	reader := &Kind{Name: "fork-reader", Initial: func(ctx context.Context, task Task, rt *Runtime) (Step, error) {
		id := Id(numberOr(asObject(task.Input)["entry"], 0))
		value, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			entry := must(tx.Entry(id))
			many := must(tx.Entries([]Id{id}))
			return []Id{entry.Id, many[id].Id}, nil
		})
		if err != nil {
			return Step{}, err
		}
		observed <- value.([]Id)
		return done(Completed(nil)), nil
	}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{reader}})
	result := env.wait(env.send(env.root, "parent"))
	var inherited Id
	for _, entry := range env.entries() {
		if entry.Kind == "pi.user" {
			inherited = entry.Id
			break
		}
	}
	fork := must(env.root.Fork(bg, result.Answer, ConversationSpec{}))
	ref := must(HostCommit(bg, fork, func(_ context.Context, tx *Tx) (TaskRef, error) {
		return tx.CreateTask(reader, JsonObject{"entry": float64(inherited)}, TaskOptions{ConversationId: &fork.Id, Background: true})
	}))
	equal(t, env.untilTerminal(ref.Id).Outcome.Status, OutcomeCompleted, "reader")
	equal(t, <-observed, []Id{inherited, inherited}, "inherited reads")
}

type failingReadStorage struct {
	*MemoryStorage
	fail atomic.Bool
}

func (s *failingReadStorage) Commit(ctx context.Context, writes []Write) (Seq, error) {
	if s.fail.Load() {
		return 0, errors.New("disk full")
	}
	return s.MemoryStorage.Commit(ctx, writes)
}

func TestSessionPersistenceFailureFaultsButCallbackFailureDoesNot(t *testing.T) {
	storage := &failingReadStorage{MemoryStorage: NewMemoryStorage()}
	h := must(OpenHarness(bg, storage, HarnessOptions{Models: newFake(fakeOptions{respond: echoScript})}))
	defer func() { check(t, h.Close(bg)) }()
	root := must(h.Root(bg))
	_, err := root.Commit(bg, func(context.Context, *Tx) (any, error) { return nil, errors.New("mine") })
	if err == nil || !strings.Contains(err.Error(), "mine") {
		t.Fatalf("callback: %v", err)
	}
	must(root.Write(bg, NewEntry{Kind: "note"}))
	storage.fail.Store(true)
	for _, op := range []func() error{func() error { _, err := root.Write(bg, NewEntry{Kind: "note"}); return err }, func() error { _, err := root.Write(bg, NewEntry{Kind: "note"}); return err }, func() error { _, err := root.Rewindable(bg); return err }} {
		if _, ok := errors.AsType[*Faulted](op()); !ok {
			t.Fatal("storage failure did not fault session")
		}
	}
}

func TestSessionLineRejectsNestedAndQueuedCancellation(t *testing.T) {
	var reports []error
	env := openEnv(t, openOptions{onReport: func(err error) { reports = append(reports, err) }})
	watch := must(env.root.Watch(bg))
	watch.Start(func(*Envelope) { panic("listener boom") })
	must(env.root.Write(bg, NewEntry{Kind: "note"}))
	if !watch.Closed() || len(reports) != 1 || !strings.Contains(reports[0].Error(), "listener boom") {
		t.Fatalf("listener isolation: %v", reports)
	}
	_, err := env.root.Commit(bg, func(ctx context.Context, tx *Tx) (any, error) {
		return env.root.Commit(ctx, func(context.Context, *Tx) (any, error) { return tx.Input(1) })
	})
	if _, ok := errors.AsType[*NestedLineOperation](err); !ok {
		t.Fatalf("nested operation: %v", err)
	}
	ctx, cancel := context.WithCancelCause(bg)
	cancel(errors.New("cancelled"))
	if _, err := env.root.Write(ctx, NewEntry{Kind: "note"}); err == nil || err.Error() != "cancelled" {
		t.Fatalf("pre-cancel: %v", err)
	}
	gate := &testGate{}
	first := make(chan error, 1)
	go func() {
		_, err := env.root.Commit(bg, func(context.Context, *Tx) (any, error) { return nil, gate.Wait(bg) })
		first <- err
	}()
	gate.Arrivals(t, 1)
	queuedCtx, queuedCancel := context.WithCancelCause(bg)
	defer queuedCancel(nil)
	barrier := &readLineBarrierContext{Context: queuedCtx, entered: make(chan struct{}), release: make(chan struct{})}
	queued := make(chan error, 1)
	go func() { _, err := env.root.Write(barrier, NewEntry{Kind: "note"}); queued <- err }()
	<-barrier.entered
	queuedCancel(errors.New("late cancel"))
	close(barrier.release)
	gate.Open()
	check(t, <-first)
	if err := <-queued; err == nil || err.Error() != "late cancel" {
		t.Fatalf("queued cancel: %v", err)
	}
}

// The barrier stops line entry after commit's initial cancellation check.
type readLineBarrierContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
}

func (ctx *readLineBarrierContext) Value(key any) any {
	if key == lineKey {
		close(ctx.entered)
		<-ctx.release
	}
	return ctx.Context.Value(key)
}
