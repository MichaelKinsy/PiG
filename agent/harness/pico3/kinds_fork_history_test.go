package pico3

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestForkUsesHistoricalRewindableAndFreshSticky(t *testing.T) {
	gate := &testGate{}
	gate.Open()
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate}), root: &RootSpec{Rewindable: JsonObject{"model": testModel}, Sticky: JsonObject{"followUpMode": "all"}}})
	state := must(env.h.Namespace("fork-state", NamespaceDefaults{Rewindable: JsonObject{"k": ""}, Sticky: JsonObject{"s": ""}}, nil))
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { must(tx.Plugins(state)).Set("k", "v1"); return nil, nil })
	check(t, err)
	first := env.wait(env.send(env.root, "one"))
	_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		node := must(tx.Plugins(state))
		node.Set("k", "v2")
		node.Set("s", "sticky")
		return nil, nil
	})
	check(t, err)
	env.wait(env.send(env.root, "two"))
	gate.Close()
	busy := env.send(env.root, "three")
	gate.Arrivals(t, 3)
	env.send(env.root, "queued")
	fork := must(env.root.Fork(bg, first.Answer, ConversationSpec{}))
	rw := must(fork.Rewindable(bg))
	sticky := must(fork.Sticky(bg))
	equal(t, obj(obj(rw, "plugins"), "fork-state")["k"], "v1", "historical state")
	equal(t, obj(rw, "model")["modelId"], "fake-1", "model inherited")
	equal(t, sticky["followUpMode"], "one-at-a-time", "fresh sticky")
	equal(t, obj(obj(sticky, "plugins"), "fork-state")["s"], nil, "private sticky not inherited")
	equal(t, arr(sticky, "inbox"), []any{}, "fresh inbox")
	equal(t, entryKinds(env.context(fork.Id).Entries), "user system assistant", "fork cutoff")
	gate.Open()
	equal(t, env.wait(env.send(fork, "in fork")).Status, InputDone, "independent fork")
	env.wait(busy)
	equal(t, entryKinds(env.entries(fork.Id)), "user system assistant user assistant", "inherited and own entries")
	equal(t, entryKinds(env.context(fork.Id).Entries), "user system assistant user assistant", "fork context")
	equal(t, obj(obj(must(fork.Rewindable(bg)), "plugins"), "fork-state")["k"], "v1", "parent updates invisible")
}

func TestForkAtStartHasNoInheritedContext(t *testing.T) {
	env := openEnv(t, openOptions{})
	env.wait(env.send(env.root, "one"))
	fork := must(env.root.Fork(bg, nil, ConversationSpec{Rewindable: JsonObject{"model": testModel}}))
	equal(t, len(env.context(fork.Id).Entries), 0, "empty context")
	equal(t, must(env.storage.Conversation(bg, fork.Id)).Parent, (*ConversationParent)(nil), "no parent range")
}

func TestForkSynthesizesMissingToolResult(t *testing.T) {
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{newTool("a", toolOptions{}).ToolDeclaration}})
	env.wait(env.send(env.root, "tool:a"))
	var assistant Id
	for _, entry := range env.entries() {
		if entry.Kind == "pi.assistant" {
			assistant = entry.Id
			break
		}
	}
	fork := must(env.root.Fork(bg, &assistant, ConversationSpec{}))
	found := false
	for _, message := range env.context(fork.Id).Messages {
		if message["role"] == "toolResult" {
			found = true
			equal(t, message["isError"], true, "synthetic error")
			equal(t, obj(message, "details")["reason"], "missing_after_fork", "reason")
		}
	}
	if !found {
		t.Fatal("missing synthetic tool result")
	}
}

func TestWatchFoldMatchesFreshAfterSummaryHead(t *testing.T) {
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{newTool("a", toolOptions{}).ToolDeclaration}, root: &RootSpec{Rewindable: JsonObject{"model": testModel, "selectedTools": []any{"a"}, "keepRecent": 10}}, models: newFake(fakeOptions{respond: summaryScript})})
	state := must(env.h.Namespace("watch-private", NamespaceDefaults{Rewindable: JsonObject{"w": 0}}, nil))
	collector := collectWatch(t, env.root)
	env.wait(env.send(env.root, "tool:a"))
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { must(tx.Plugins(state)).Set("w", 1); return nil, nil })
	check(t, err)
	env.untilTerminal(must(env.root.Collapse(bg, nil)))
	env.idle()
	collector.watch.Stop()
	folded := collector.view
	for _, envelope := range collector.Envelopes() {
		folded = must(ApplyEnvelope(folded, envelope))
	}
	fresh := must(env.root.Watch(bg))
	defer fresh.Stop()
	equal(t, folded, fresh.View, "fold equals capture")
	equal(t, obj(folded, "tasks"), JsonObject{}, "terminal tasks omitted")
	var head float64
	for _, entry := range arr(folded, "entries") {
		if str(asObject(entry), "kind") == "pi.summary" {
			head = numberOr(asObject(entry)["head"], 0)
		}
	}
	if head == 0 {
		t.Fatal("missing summary head")
	}
	for _, entry := range arr(folded, "entries") {
		if numberOr(asObject(entry)["id"], 0) < head {
			t.Fatal("view retains pre-head entry")
		}
	}
}

func TestOrdinaryKindRecoversAtDurableCheckpoint(t *testing.T) {
	gate := &testGate{}
	var ran []string
	kind := &Kind{Name: "counter", Initial: func(_ context.Context, task Task, _ *Runtime) (Step, error) {
		ran = append(ran, "initial")
		return Step{Next: Checkpoint{"phase": "counting", "n": asObject(task.Input)["start"]}}, nil
	}, Phases: map[string]PhaseHandler{"counting": func(ctx context.Context, task Task, _ *Runtime) (Step, error) {
		n := numberOr(task.Checkpoint["n"], 0)
		ran = append(ran, fmt.Sprintf("counting@%g", n))
		if n < 3 {
			if err := gate.Wait(ctx); err != nil {
				return Step{}, err
			}
			return Step{Next: Checkpoint{"phase": "counting", "n": n + 1}}, nil
		}
		return done(Completed(JsonObject{"total": n})), nil
	}}}
	env := openEnv(t, openOptions{backend: "jsonl", taskKinds: []*Kind{kind}})
	ref := createTestTask(t, env, kind, JsonObject{"start": 1})
	gate.Arrivals(t, 1)
	env = env.crash()()
	gate.Open()
	equal(t, env.untilTerminal(ref.Id).Outcome, &Outcome{Status: OutcomeCompleted, Result: JsonObject{"total": float64(3)}}, "counter outcome")
	equal(t, ran, []string{"initial", "counting@1", "counting@1", "counting@2", "counting@3"}, "phase recovery")
}

func TestStickySidecarStaysBoundedWhileHistoryGrows(t *testing.T) {
	env := openEnv(t, openOptions{backend: "jsonl"})
	storage := env.storage.(*JsonlStorage)
	run := func() {
		for range 5 {
			env.wait(env.send(env.root, strings.Repeat("x ", 200)))
		}
		env.idle()
		env.h.scheduler.running.Wait()
	}
	run()
	first := must(storage.Sizes())["sticky-1.jsonl"]
	run()
	sizes := must(storage.Sizes())
	delta := sizes["sticky-1.jsonl"] - first
	if delta < -200 || delta > 200 {
		t.Fatalf("sticky grew: %d -> %d", first, sizes["sticky-1.jsonl"])
	}
	if sizes["main.jsonl"] <= 10000 {
		t.Fatal("rewindable history lost")
	}
}

func TestStickyBusyBudgetThenIdleBase(t *testing.T) {
	gate := &testGate{}
	noisy := &ToolDeclaration{Name: "noisy", Parameters: toolSchema, Output: &ToolOutput{MaxBytes: new(100000), Retain: "tail"}, Execute: func(ctx context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
		for i := range 40 {
			if err := api.Stream([]byte(strings.Repeat(fmt.Sprintf("%d:", i), 2000))); err != nil {
				return ToolResult{}, err
			}
			timer := time.NewTimer(12 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ToolResult{}, context.Cause(ctx)
			}
		}
		return ToolResult{}, gate.Wait(ctx)
	}}
	models := newFake(fakeOptions{respond: func(messages []JsonObject, _ int) fakeResponse {
		if lastMessage(messages)["role"] == "toolResult" {
			return textResponse("ok")
		}
		return fakeResponse{toolCalls: []fakeToolCall{{name: "noisy", arguments: JsonObject{"v": "a"}}, {name: "noisy", arguments: JsonObject{"v": "b"}}}}
	}})
	env := openEnv(t, openOptions{backend: "jsonl", tools: []*ToolDeclaration{noisy}, models: models})
	input := env.send(env.root, "go")
	gate.Arrivals(t, 2)
	storage := env.storage.(*JsonlStorage)
	size := must(storage.Sizes())["sticky-1.jsonl"]
	if size <= StickyBaseBudget {
		t.Fatalf("streamed only %d bytes", size)
	}
	gate.Open()
	env.wait(input)
	env.idle()
	env.h.scheduler.running.Wait()
	size = must(storage.Sizes())["sticky-1.jsonl"]
	if size >= 5000 {
		t.Fatalf("idle sticky not truncated: %d", size)
	}
}
