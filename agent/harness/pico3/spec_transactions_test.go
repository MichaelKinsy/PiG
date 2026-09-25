package pico3

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

func TestTaskPatchAndSlotReadTheirWrites(t *testing.T) {
	observed := make(chan JsonObject, 1)
	kind := &Kind{Name: "overlay", Slot: func(JsonValue) JsonObject { return JsonObject{"count": 0} }, Phases: map[string]PhaseHandler{"finish": func(context.Context, Task, *Runtime) (Step, error) { return done(Completed(nil)), nil }}}
	kind.Initial = func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		value, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, current Task) (any, error) {
			if err := tx.Checkpoint(Checkpoint{"phase": "finish"}); err != nil {
				return nil, err
			}
			slot := must(tx.Slot(TaskRef{Id: current.Id, Kind: kind}))
			slot.Set("count", 1)
			direct := must(tx.Task(current.Id))
			snapshot := must(tx.Snapshot(StickyDoc(current.ConversationId)))
			return JsonObject{"phase": Phase(direct.Checkpoint), "count": obj(obj(snapshot, "tasks"), fmt.Sprint(current.Id))["count"]}, nil
		})
		if err != nil {
			return Step{}, err
		}
		observed <- value.(JsonObject)
		return done(Completed(nil)), nil
	}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
	ref := createTestTask(t, env, kind, nil)
	equal(t, env.untilTerminal(ref.Id).Outcome.Status, OutcomeCompleted, "completed")
	equal(t, <-observed, JsonObject{"phase": "finish", "count": 1}, "overlay")
}

func TestOwnedConversationSurvivesSameCommitCheckpointAndReopen(t *testing.T) {
	gate := &testGate{}
	kind := &Kind{Name: "owns-overlay", Initial: func(context.Context, Task, *Runtime) (Step, error) {
		return Step{Build: func(_ context.Context, tx *Tx, _ Task) (Transition, error) {
			child, err := tx.CreateConversation(ConversationSpec{})
			if err != nil {
				return Transition{}, err
			}
			cp := Checkpoint{"phase": "finish", "child": float64(child)}
			if err := tx.Checkpoint(cp); err != nil {
				return Transition{}, err
			}
			return Transition{Checkpoint: cp}, nil
		}}, nil
	}, Phases: map[string]PhaseHandler{"finish": func(ctx context.Context, _ Task, _ *Runtime) (Step, error) {
		return done(Completed(nil)), gate.Wait(ctx)
	}}}
	env := openEnv(t, openOptions{backend: "jsonl", taskKinds: []*Kind{kind}})
	ref := createTestTask(t, env, kind, nil)
	gate.Arrivals(t, 1)
	running := must(env.h.GetTask(bg, ref.Id))
	equal(t, running.Owns, []Id{Id(running.Checkpoint["child"].(float64))}, "running owns")
	gate.Open()
	terminal := env.untilTerminal(ref.Id)
	equal(t, terminal.Owns, running.Owns, "terminal owns")
	next := env.crash()()
	equal(t, *must(next.h.GetTask(bg, ref.Id)), terminal, "durable owns")
}

func TestDocumentOverlayAndScanDomains(t *testing.T) {
	env := openEnv(t, openOptions{})
	other := must(env.h.CreateConversation(bg, ConversationSpec{}, nil))
	ns := must(env.h.Namespace("overlay", NamespaceDefaults{Rewindable: JsonObject{"nested": JsonObject{"value": 0}}}, nil))
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		must(tx.Plugins(ns)).Set("nested", JsonObject{"value": 1})
		config := must(tx.Config(1))
		check(t, config.Set("profile", "updated"))
		equal(t, must(config.Get("profile")), "updated", "config overlay")
		equal(t, obj(must(tx.Snapshot(RewindableDoc(1))), "plugins")["overlay"], JsonObject{"nested": JsonObject{"value": 1}}, "document overlay")
		if _, err := tx.Write(1, NewEntry{Kind: "note"}); err != nil {
			return nil, err
		}
		if _, err := tx.Context(other.Id, nil); err != nil {
			return nil, err
		}
		if _, err := tx.NewestEntry(other.Id, NewestOptions{}); err != nil {
			return nil, err
		}
		return tx.ScanEntries(EntryScan{ConversationId: other.Id, Limit: 10})
	})
	check(t, err)
	for _, taskWrite := range []bool{false, true} {
		_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
			if taskWrite {
				_, err := tx.CreateTask(Kinds.Plugin, JsonObject{"handler": "missing", "input": nil}, TaskOptions{ConversationId: new(Id(1)), Background: true})
				if err != nil {
					return nil, err
				}
				return tx.Tasks(TaskScan{ConversationId: &other.Id})
			}
			if _, err := tx.Write(1, NewEntry{Kind: "note"}); err != nil {
				return nil, err
			}
			return tx.Context(1, nil)
		})
		if _, ok := errors.AsType[*ReadAfterWrite](err); !ok {
			t.Fatalf("scan domain task=%v: %v", taskWrite, err)
		}
	}
}

func TestQueuedPassiveWriteDoesNotPoisonEntryScans(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	active := env.send(env.root, "active")
	gate.Arrivals(t, 1)
	queued := must(HostCommit(bg, env.root, func(_ context.Context, tx *Tx) (Id, error) {
		id, err := tx.Write(1, NewEntry{Kind: "passive"})
		if err != nil {
			return 0, err
		}
		equal(t, must(tx.Input(id)).Status, InputQueued, "queued")
		view, err := tx.Context(1, nil)
		if err != nil {
			return 0, err
		}
		newest := must(tx.NewestEntry(1, NewestOptions{}))
		found := false
		for _, entry := range view.Entries {
			if entry.Id == newest.Id {
				found = true
			}
		}
		if !found {
			t.Error("context lacks newest entry")
		}
		return id, nil
	}))
	gate.Open()
	env.wait(active)
	equal(t, env.input(queued).Status, InputDone, "settled")
}

func TestAbandonedIDsRemainLiveHighWaterButReopenMayReuse(t *testing.T) {
	env := openEnv(t, openOptions{backend: "jsonl"})
	abandon := func() Id {
		var id Id
		_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
			id = must(tx.Write(1, NewEntry{Kind: "abandoned"}))
			return nil, errors.New("discard")
		})
		if err == nil {
			t.Fatal("callback succeeded")
		}
		return id
	}
	first := abandon()
	same := must(env.root.Write(bg, NewEntry{Kind: "same-process"}))
	if same <= first {
		t.Fatal("recycled live id")
	}
	env = env.crash()()
	later := must(env.root.Write(bg, NewEntry{Kind: "reopened"}))
	if later <= same {
		t.Fatal("lost committed high water")
	}
	second := abandon()
	env = env.crash()()
	equal(t, must(env.root.Write(bg, NewEntry{Kind: "reuse"})), second, "uncommitted id reused")
}

type blockedCommitStorage struct {
	*MemoryStorage
	block    atomic.Bool
	attempts atomic.Int32
	started  chan context.Context
	release  chan struct{}
}

func (s *blockedCommitStorage) Commit(ctx context.Context, writes []Write) (Seq, error) {
	if s.block.Load() {
		s.attempts.Add(1)
		s.started <- ctx
		<-s.release
	}
	return s.MemoryStorage.Commit(ctx, writes)
}
func TestPersistenceIgnoresLateCancellationAndRunsOnce(t *testing.T) {
	storage := &blockedCommitStorage{MemoryStorage: NewMemoryStorage(), started: make(chan context.Context, 1), release: make(chan struct{})}
	h := must(OpenHarness(bg, storage, HarnessOptions{Models: newFake(fakeOptions{respond: echoScript})}))
	t.Cleanup(func() { check(t, h.Close(bg)) })
	root := must(h.Root(bg))
	storage.block.Store(true)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := root.Write(ctx, NewEntry{Kind: "persist"}); done <- err }()
	commitCtx := <-storage.started
	cancel()
	equal(t, commitCtx.Done(), (<-chan struct{})(nil), "non cancellable")
	storage.block.Store(false)
	close(storage.release)
	check(t, <-done)
	equal(t, storage.attempts.Load(), int32(1), "attempts")
	entries := must(storage.ScanEntries(bg, EntryScan{ConversationId: root.Id, Limit: 100}))
	equal(t, len(entries), 1, "persisted")
}
