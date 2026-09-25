package pico3

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// chordTestState is the caller-supplied state boundary. It deliberately knows
// nothing about Pico ops; only Change's atomic draft reaches publication.
type chordTestState struct {
	mu           sync.Mutex
	value        PublishedConversationView
	sequence     int
	completed    int
	listeners    map[int]func(PublishedConversationView, context.Context, ReplicatedStateDelivery)
	nextListener int
	deliveries   []PublishedConversationView
	notify       chan struct{}
	change       func() error
}

func newChordTestState(initial PublishedConversationView) *chordTestState {
	return &chordTestState{value: cloneObject(initial), listeners: map[int]func(PublishedConversationView, context.Context, ReplicatedStateDelivery){}, notify: make(chan struct{}, 1)}
}

func (state *chordTestState) Value() PublishedConversationView {
	state.mu.Lock()
	defer state.mu.Unlock()
	return cloneObject(state.value)
}

func (state *chordTestState) Subscribe(listener func(PublishedConversationView, context.Context, ReplicatedStateDelivery)) (func(), error) {
	state.mu.Lock()
	id := state.nextListener
	state.nextListener++
	state.listeners[id] = listener
	value, sequence := cloneObject(state.value), state.sequence
	state.mu.Unlock()
	listener(value, bg, ReplicatedStateDelivery{Kind: "hydrate", Sequence: sequence})
	return func() { state.mu.Lock(); delete(state.listeners, id); state.mu.Unlock() }, nil
}

func (state *chordTestState) Change(ctx context.Context, mutate func(PublishedConversationView) error) error {
	if state.change != nil {
		if err := state.change(); err != nil {
			return err
		}
	}
	draft := state.Value()
	if err := mutate(draft); err != nil {
		return err
	}
	return state.Replace(ctx, draft)
}

func (state *chordTestState) Replace(ctx context.Context, value PublishedConversationView) error {
	state.mu.Lock()
	if reflect.DeepEqual(state.value, value) {
		state.mu.Unlock()
		return nil
	}
	state.value = cloneObject(value)
	state.sequence++
	delivery := ReplicatedStateDelivery{Kind: "update", Sequence: state.sequence}
	state.deliveries = append(state.deliveries, cloneObject(value))
	listeners := make([]func(PublishedConversationView, context.Context, ReplicatedStateDelivery), 0, len(state.listeners))
	for _, listener := range state.listeners {
		listeners = append(listeners, listener)
	}
	state.mu.Unlock()
	for _, listener := range listeners {
		listener(cloneObject(value), ctx, delivery)
	}
	state.mu.Lock()
	state.completed = delivery.Sequence
	state.mu.Unlock()
	select {
	case state.notify <- struct{}{}:
	default:
	}
	return nil
}

func (state *chordTestState) publications(t *testing.T, count int) []PublishedConversationView {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		state.mu.Lock()
		if state.completed >= count {
			values := make([]PublishedConversationView, len(state.deliveries))
			for index, value := range state.deliveries {
				values[index] = cloneObject(value)
			}
			state.mu.Unlock()
			return values
		}
		state.mu.Unlock()
		select {
		case <-state.notify:
		case <-timer.C:
			t.Fatal("Chord publication did not drain")
		}
	}
}

func closeChord(t *testing.T, bridge *ChordViewBridge) {
	t.Helper()
	bridge.Close()
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	check(t, bridge.Wait(ctx))
}

func TestChordBridgePublishesEveryEnvelopeAndConverges(t *testing.T) {
	env := openEnv(t, openOptions{})
	raw := collectWatch(t, env.root)
	var state *chordTestState
	bridge := must(AttachChordView(bg, env.root, func(initial PublishedConversationView) MutableReplicatedState {
		state = newChordTestState(initial)
		return state
	}, ChordViewBridgeOptions{}))
	t.Cleanup(func() { closeChord(t, bridge) })
	service := CreatePicoConversationService(env.h, env.root, bridge.View)
	var mu sync.Mutex
	var sequences []int
	stop := must(bridge.View.Subscribe(func(_ PublishedConversationView, _ context.Context, delivery ReplicatedStateDelivery) {
		if delivery.Kind == "update" {
			mu.Lock()
			sequences = append(sequences, delivery.Sequence)
			mu.Unlock()
		}
	}))
	t.Cleanup(stop)
	env.wait(env.send(env.root, "hello"))
	env.idle()
	envelopes := raw.Envelopes()
	publications := state.publications(t, len(envelopes))
	equal(t, len(publications), len(envelopes), "one publication per envelope")
	mu.Lock()
	equal(t, len(sequences), len(envelopes), "subscriber delivery count")
	for index, sequence := range sequences {
		equal(t, sequence, index+1, "contiguous sequence")
	}
	mu.Unlock()
	for index, publication := range publications {
		equal(t, mustJSON(obj(publication, "commit")["events"]), mustJSON(envelopes[index].Events), "commit events")
	}
	for _, patch := range []JsonObject{{"threshold": "not-a-number"}, {"selectedTools": "not-an-array"}, {"retry": 5}, {"model": nil}, {"steeringMode": "sometimes"}} {
		err := service.ConfigSet(bg, patch)
		if err == nil || !strings.Contains(err.Error(), "invalid config value") {
			t.Fatalf("invalid config accepted: %v: %v", patch, err)
		}
	}
	_, err := service.Fork(bg, nil, ConversationSpec{Rewindable: JsonObject{"threshold": "not-a-number"}})
	if err == nil || !strings.Contains(err.Error(), "invalid rewindable config value") {
		t.Fatalf("invalid fork config: %v", err)
	}
	check(t, service.ConfigSet(bg, JsonObject{"threshold": 123, "selectedTools": []any{"bash"}, "steeringMode": "all"}))
	equal(t, must(env.root.Config().Get(bg))["threshold"], float64(123), "routed config")
	foreign := must(env.h.CreateConversation(bg, ConversationSpec{}, nil))
	foreignInput := env.send(foreign, "foreign")
	_, err = service.InputAbort(bg, foreignInput.Id)
	if err == nil || !strings.Contains(err.Error(), "outside conversation") {
		t.Fatalf("foreign abort: %v", err)
	}
	env.wait(foreignInput)
	state.publications(t, len(raw.Envelopes()))
	fresh := must(env.root.Watch(bg))
	fresh.Stop()
	published := state.Value()
	delete(published, "commit")
	equal(t, published, fresh.View, "fresh snapshot")
}

func TestChordPublicationFailureDoesNotRejectPersistedWriter(t *testing.T) {
	env := openEnv(t, openOptions{})
	reported := make(chan error, 1)
	bridge := must(AttachChordView(bg, env.root, func(initial PublishedConversationView) MutableReplicatedState {
		state := newChordTestState(initial)
		state.change = func() error { return errors.New("publish failed") }
		return state
	}, ChordViewBridgeOptions{OnFailure: func(err error) { reported <- err }}))
	t.Cleanup(func() { closeChord(t, bridge) })
	input := env.send(env.root, "persist anyway")
	equal(t, env.wait(input).Status, InputDone, "durable writer")
	select {
	case err := <-reported:
		if !strings.Contains(err.Error(), "publish failed") {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing publication failure")
	}
	if !bridge.Closed() || !bridge.watch.Closed() {
		t.Fatal("failed bridge remained subscribed")
	}
	env.wait(env.send(env.root, "writer still works"))
}
