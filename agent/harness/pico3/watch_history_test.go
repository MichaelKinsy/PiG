package pico3

import (
	"context"
	"errors"
	"testing"
)

// Source: watch.test.ts cases at lines 38, 98 and 150; existing watch_test.go
// covers the other three source cases (revision folds, listener errors, capacity).
func foldCollected(t *testing.T, collector *watchCollector) JsonObject {
	t.Helper()
	view := collector.view
	for _, envelope := range collector.Envelopes() {
		view = must(ApplyEnvelope(view, envelope))
	}
	return view
}

func TestWatchFailedDocumentCommitKeepsTaskUpdatesHealthy(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	active := env.send(env.root, "active")
	gate.Arrivals(t, 1)
	queued := env.send(env.root, "queued")
	generation := env.untilPhase("pi.generation", "requesting")
	watch := must(env.root.Watch(bg))
	watch.Start(func(*Envelope) {})
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		config, err := tx.Config(1)
		if err != nil {
			return nil, err
		}
		if err := config.Set("profile", "rolled-back"); err != nil {
			return nil, err
		}
		return nil, errors.New("rollback")
	})
	if err == nil || err.Error() != "rollback" {
		t.Fatalf("failed callback: %v", err)
	}
	equal(t, must(queued.Abort(bg)), "aborted", "queued abort")
	equal(t, must(queued.Result(bg)).Reason, "aborted", "queued result")
	equal(t, must(env.h.MarkTask(bg, generation.Id)), "marked", "mark active")
	equal(t, watch.Closed(), false, "watch survives failed docs and mark")
	must(env.h.AbortTask(bg, generation.Id))
	env.wait(active)
	watch.Stop()
}

func TestWatchForkInheritedHeadAndIncrementalFold(t *testing.T) {
	gate := &testGate{}
	gate.Open()
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	for _, text := range []string{"one", "two"} {
		env.wait(env.send(env.root, text))
	}
	check(t, env.root.Reset(bg, new("carry on")))
	env.wait(env.send(env.root, "three"))
	parent := env.entries()
	var head Id
	for _, entry := range parent {
		if entry.Kind == "pi.handoff" {
			head = entry.Id
		}
	}
	tip := parent[len(parent)-1].Id
	child := must(env.root.Fork(bg, &tip, ConversationSpec{}))
	collector := collectWatch(t, child)
	must(child.Write(bg, NewEntry{Kind: "note"}))
	env.wait(env.send(child, "four"))
	check(t, child.Reset(bg, nil))
	fresh := must(child.Watch(bg))
	equal(t, foldCollected(t, collector)["entries"], fresh.View["entries"], "fork reset fold")
	heads := 0
	for _, entry := range arr(fresh.View, "entries") {
		if _, ok := asObject(entry)["head"]; ok {
			heads++
		}
	}
	equal(t, heads, 1, "reset has one head")
	fresh.Stop()
	collector.watch.Stop()
	child2 := must(env.root.Fork(bg, &tip, ConversationSpec{}))
	collector2 := collectWatch(t, child2)
	var expected []Id
	for _, entry := range parent {
		if entry.Id >= head {
			expected = append(expected, entry.Id)
		}
	}
	equal(t, historyViewIDs(collector2.view), expected, "inherited active head range")
	check(t, child2.Reset(bg, new("again")))
	env.wait(env.send(child2, "five"))
	fresh2 := must(child2.Watch(bg))
	defer fresh2.Stop()
	equal(t, foldCollected(t, collector2)["entries"], fresh2.View["entries"], "second inherited head fold")
}

func TestWatchEnvelopePreservesDisplayAndInnerHeadEntries(t *testing.T) {
	base := JsonObject{"conversation": JsonObject{"id": 1}, "entries": []any{JsonObject{"id": 1, "conversationId": 1, "kind": "pi.user"}, JsonObject{"id": 2, "conversationId": 1, "kind": "pi.summary", "head": 2}, JsonObject{"id": 3, "conversationId": 1, "kind": "pi.assistant"}}, "config": JsonObject{}, "inbox": []any{}, "tasks": JsonObject{}, "plugins": JsonObject{}}
	display := JsonObject{"id": 4, "conversationId": 1, "kind": "pi.assistant", "data": JsonObject{"reason": "aborted"}}
	handoff := JsonObject{"id": 5, "conversationId": 1, "kind": "pi.handoff", "head": 3}
	first := &Envelope{Revision: 9, Ops: []Op{{"p", []any{"entries"}, 0, 2, []any{}}, {"p", []any{"entries"}, 1, 0, []any{display, handoff}}}}
	view := must(ApplyEnvelope(base, first))
	equal(t, historyViewIDs(view), []Id{3, 4, 5}, "first splice")
	summary := JsonObject{"id": 6, "conversationId": 1, "kind": "pi.summary", "head": 4}
	second := &Envelope{Revision: 10, Ops: []Op{{"p", []any{"entries"}, 0, 1, []any{}}, {"p", []any{"entries"}, 2, 0, []any{summary}}}}
	next := must(ApplyEnvelope(view, second))
	equal(t, historyViewIDs(next), []Id{4, 5, 6}, "inner head and display retained")
	equal(t, arr(next, "entries")[0], display, "display verbatim")
	equal(t, arr(next, "entries")[1], handoff, "inner head verbatim")
}
