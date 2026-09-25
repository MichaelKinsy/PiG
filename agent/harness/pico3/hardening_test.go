package pico3

import (
	"context"
	"errors"
	"testing"
)

// Source: packages/agent/test/harness/pico3/hardening.test.ts, all seven cases.
func TestHardeningStorageReadCannotMutateEntry(t *testing.T) {
	storage := NewMemoryStorage()
	defer func() { check(t, storage.Close(bg)) }()
	must(storage.Commit(bg, []Write{{Type: WriteConversation, Conversation: &Conversation{Id: 1}}, {Type: WriteEntry, Entry: &Entry{Id: 2, ConversationId: 1, Kind: "original"}}}))
	entry := must(storage.Entries(bg, []Id{2}))[2]
	entry.Kind = "mutated"
	equal(t, must(storage.Entries(bg, []Id{2}))[2].Kind, "original", "authoritative entry")
}
func TestHardeningProviderFailureAdvancesFollowUp(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: func([]JsonObject, int) fakeResponse { return errorResponse("fatal") }, gate: gate}), root: &RootSpec{Rewindable: JsonObject{"model": testModel}, Sticky: JsonObject{"retry": JsonObject{"enabled": false, "maxRetries": 0, "baseDelayMs": 1}}}})
	first := env.send(env.root, "first")
	gate.Arrivals(t, 1)
	queued := env.send(env.root, "second")
	gate.Open()
	env.wait(first)
	env.wait(queued)
	if env.input(queued.Id).Status == InputQueued {
		t.Fatal("failure stranded follow-up")
	}
	found := false
	for _, task := range env.tasks() {
		if task.Kind == "pi.generation" {
			for _, id := range arr(asObject(task.Input), "inputs") {
				if Id(numberOr(id, 0)) == queued.Id {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("follow-up had no successor generation")
	}
}
func TestHardeningRuntimeReadEnforcesSubtree(t *testing.T) {
	observed := make(chan error, 1)
	kind := &Kind{Name: "scope-probe", Initial: func(ctx context.Context, task Task, rt *Runtime) (Step, error) {
		_, err := rt.Context(ctx, Id(numberOr(asObject(task.Input)["foreign"], 0)), nil)
		observed <- err
		return Step{Next: Checkpoint{"phase": "done"}}, nil
	}, Phases: map[string]PhaseHandler{"done": func(context.Context, Task, *Runtime) (Step, error) { return done(Completed(nil)), nil }}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
	foreign := must(env.h.CreateConversation(bg, ConversationSpec{}, nil))
	must(foreign.Write(bg, NewEntry{Kind: "foreign"}))
	ref := createTestTask(t, env, kind, JsonObject{"foreign": foreign.Id})
	env.untilTerminal(ref.Id)
	if err := <-observed; err == nil {
		t.Fatal("foreign context exposed")
	} else if _, ok := errors.AsType[*Forbidden](err); !ok {
		t.Fatalf("read error: %v", err)
	}
}
func TestHardeningUnknownKindOrphanedAtResume(t *testing.T) {
	storage := NewMemoryStorage()
	writes := historyConversationWrites(Conversation{Id: 1})
	writes = append(writes, Write{Type: WriteTask, Task: &Task{Id: 2, ConversationId: 1, Kind: "missing.kind", Status: TaskRunning, After: []Id{}, Owns: []Id{}}})
	must(storage.Commit(bg, writes))
	h := historyHarness(t, storage)
	task := must(h.WaitForTask(bg, 2))
	equal(t, task.Status, TaskTerminal, "unknown task terminal")
	equal(t, task.Outcome.Status, OutcomeOrphaned, "unknown task orphaned")
}
func TestHardeningRegisterAfterOpenBeforeResumeRecovers(t *testing.T) {
	storage := NewMemoryStorage()
	writes := historyConversationWrites(Conversation{Id: 1})
	writes = append(writes, Write{Type: WriteTask, Task: &Task{Id: 2, ConversationId: 1, Kind: "recover.after-open", Status: TaskRunning, After: []Id{}, Owns: []Id{}}})
	must(storage.Commit(bg, writes))
	h := must(OpenHarness(bg, storage, HarnessOptions{Models: newFake(fakeOptions{respond: func([]JsonObject, int) fakeResponse { return textResponse("ok") }})}))
	defer func() { check(t, h.Close(bg)) }()
	recovered := &Kind{Name: "recover.after-open", Initial: func(context.Context, Task, *Runtime) (Step, error) {
		return Step{Next: Checkpoint{"phase": "finish"}}, nil
	}, Phases: map[string]PhaseHandler{"finish": func(context.Context, Task, *Runtime) (Step, error) { return done(Completed("replacement")), nil }}}
	must(h.RegisterTaskKind(recovered))
	check(t, h.Resume())
	task := must(h.WaitForTask(bg, 2))
	equal(t, task.Outcome.Status, OutcomeCompleted, "late registration recovers")
	equal(t, task.Outcome.Result, "replacement", "replacement result")
}

func TestHardeningNestedAssignedWrapperAndDeepClone(t *testing.T) {
	target := JsonObject{"left": JsonObject{}, "right": JsonObject{"value": 1}}
	membrane := NewMembrane("nested")
	view := membrane.Wrap(target)
	left := view.Get("left").(*Node)
	escaped := view.Get("right").(*Node)
	assertMembranePanic(t, "document proxy", func() { left.Set("payload", JsonObject{"escaped": escaped}) })
	assigned := JsonObject{"nested": JsonObject{"value": 2}}
	left.Set("payload", assigned)
	asObject(assigned["nested"])["value"] = 3
	equal(t, asObject(target["left"])["payload"], JsonObject{"nested": JsonObject{"value": 2}}, "deep clone assigned input")
	membrane.Revoke()
	assertMembranePanic(t, "outside its transaction", func() { view.Get("left") })
}
func TestHardeningTailPartialAndSingleChunkTrimming(t *testing.T) {
	bytes := NewBounded(10, 100, "tail")
	bytes.Push([]byte("abcdefgh"))
	bytes.Push([]byte("ijklmnop"))
	equal(t, bytes.Text(), "ghijklmnop", "partial chunk byte trimming")
	equal(t, bytes.DroppedBytes, 6, "dropped bytes")
	lines := NewBounded(100, 2, "tail")
	lines.Push([]byte("a\nb\nc\nd\n"))
	equal(t, lines.Text(), "c\nd\n", "single chunk line trimming")
	equal(t, lines.DroppedLines, 2, "dropped lines")
}
