package pico3

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestChordSlowPublicationBoundsQueueWithoutBlockingWriter(t *testing.T) {
	env := openEnv(t, openOptions{})
	entered, release := make(chan struct{}), make(chan struct{})
	reported := make(chan error, 1)
	capacity := 1
	var state *chordTestState
	bridge := must(AttachChordView(bg, env.root, func(initial PublishedConversationView) MutableReplicatedState {
		state = newChordTestState(initial)
		state.change = func() error { close(entered); <-release; return nil }
		return state
	}, ChordViewBridgeOptions{Capacity: &capacity, OnFailure: func(err error) { reported <- err; panic("ignored failure callback") }}))
	t.Cleanup(func() { closeChord(t, bridge) })
	defer close(release)
	_, err := env.root.Write(bg, NewEntry{Kind: "note"})
	check(t, err)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not start")
	}
	for range 2 {
		_, err = env.root.Write(bg, NewEntry{Kind: "note"})
		check(t, err)
	}
	select {
	case err = <-reported:
		equal(t, err.Error(), "Pico-to-Chord view queue exceeded 1 envelopes", "bounded queue")
	default:
		t.Fatal("queue overflow was not reported")
	}
	if !bridge.Closed() || !bridge.watch.Closed() {
		t.Fatal("overflow left watch open")
	}
	fresh := must(env.root.Watch(bg))
	fresh.Stop()
	equal(t, len(arr(fresh.View, "entries")), 3, "all writes persisted while publication blocked")
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if !errors.Is(bridge.Wait(ctx), context.Canceled) {
		t.Fatal("Wait ignored caller cancellation")
	}
	state.mu.Lock()
	equal(t, state.sequence, 0, "blocked publisher")
	state.mu.Unlock()
}

func TestChordCloseFromPublicationDiscardsQueueAndJoins(t *testing.T) {
	env := openEnv(t, openOptions{})
	var state *chordTestState
	bridge := must(AttachChordView(bg, env.root, func(initial PublishedConversationView) MutableReplicatedState {
		state = newChordTestState(initial)
		return state
	}, ChordViewBridgeOptions{}))
	t.Cleanup(func() { closeChord(t, bridge) })
	stop := must(bridge.View.Subscribe(func(_ PublishedConversationView, _ context.Context, delivery ReplicatedStateDelivery) {
		if delivery.Kind == "update" {
			bridge.Close()
		}
	}))
	defer stop()
	_, err := env.root.Write(bg, NewEntry{Kind: "note"})
	check(t, err)
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	check(t, bridge.Wait(ctx))
	_, err = env.root.Write(bg, NewEntry{Kind: "note"})
	check(t, err)
	equal(t, len(state.publications(t, 1)), 1, "no publications after close")
}

func TestChordServiceRegistrationAndScopedOperations(t *testing.T) {
	equal(t, PicoHarnessService.Id(), "pi.harness", "harness service id")
	equal(t, PicoHarnessService.Local(), true, "harness stays local")
	equal(t, PicoConversationServiceDefinition.Id(), "pi.conversation", "conversation service id")
	equal(t, PicoConversationServiceDefinition.Local(), false, "conversation remote facet")
	env := openEnv(t, openOptions{})
	service := CreatePicoConversationService(env.h, env.root, nil)
	id := must(service.Send(bg, SendInput{Content: "service input"}))
	env.wait(env.h.inputHandle(id))
	check(t, service.Abort(bg))
	note := must(service.Write(bg, NewEntry{Kind: "note"}))
	if note == 0 {
		t.Fatal("write did not return its input id")
	}
	child := must(service.Fork(bg, nil, ConversationSpec{}))
	entries := must(service.Entries(bg, EntryScan{ConversationId: child, Limit: 100}))
	if len(entries) == 0 {
		t.Fatal("scoped scan returned child entries")
	}
	equal(t, entries[0].Kind, "note", "service scan overrides supplied conversation")
	_, err := service.InputAbort(bg, id)
	check(t, err)
	check(t, service.Reset(bg, nil))
	_, err = service.Collapse(bg, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing to collapse") {
		t.Fatalf("collapse forwarding: %v", err)
	}
}

func TestChordRejectsInvalidCapacity(t *testing.T) {
	env := openEnv(t, openOptions{})
	for _, capacity := range []int{0, -1, 9007199254740992} {
		_, err := AttachChordView(bg, env.root, func(initial PublishedConversationView) MutableReplicatedState { return newChordTestState(initial) }, ChordViewBridgeOptions{Capacity: &capacity})
		if err == nil || err.Error() != "Chord view queue capacity must be positive" {
			t.Fatalf("capacity %d: %v", capacity, err)
		}
	}
}

func TestChordTrackedOperations(t *testing.T) {
	root := JsonObject{"items": []any{"a", "b", "a"}, "text": "hello", "drop": true}
	check(t, applyTracked(root, []Op{
		{"m", []any{"items"}, []any{1, 2, 0}},
		{"d", []any{"items", 1}},
		{"p", []any{"items"}, 1, 0, []any{"insert"}},
		{"s", []any{"items", 0}, "B"},
		{"a", []any{"text"}, "!"},
		{"t", []any{"text"}, 2},
		{"d", []any{"drop"}},
	}))
	equal(t, root, JsonObject{"items": []any{"B", "insert", "a"}, "text": "llo!"}, "all live operations")
	for _, test := range []struct {
		op      Op
		message string
	}{
		{Op{"r", JsonObject{}}, "unexpectedly replaced"},
		{Op{"p", []any{"text"}, 0, 0, []any{}}, "splice path is not an array"},
		{Op{"m", []any{"items"}, []any{0}}, "permutation path is not a matching array"},
		{Op{"s", []any{"missing", "value"}, 1}, "parent is not an object"},
	} {
		if err := applyTracked(root, []Op{test.op}); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("op %v: %v", test.op, err)
		}
	}
}
