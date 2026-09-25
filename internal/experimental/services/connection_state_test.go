package services

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

func TestSourceStatePublishesInCommitOrderWithoutHoldingSourceLocks(t *testing.T) {
	state := &SourceState[int]{}
	var values []int
	var deliveries []ReplicatedStateDelivery
	ctx := t.Context()
	stop := state.Subscribe(func(value int, delivered context.Context, delivery ReplicatedStateDelivery) {
		values = append(values, value)
		deliveries = append(deliveries, delivery)
		if delivery.Kind == "update" && delivered != ctx {
			t.Error("publication context changed")
		}
		_ = state.Value()
	})
	first := state.replace(ctx, 1)
	second := state.replace(ctx, 2)
	second()
	first()
	state.replace(ctx, 2)()
	queued := state.replace(ctx, 3)
	stop()
	stop()
	queued()
	state.replace(ctx, 4)()
	if !reflect.DeepEqual(values, []int{0, 1, 2}) {
		t.Fatalf("state delivery = %v", values)
	}
	if !reflect.DeepEqual(deliveries, []ReplicatedStateDelivery{{Kind: "hydrate", Sequence: 0}, {Kind: "update", Sequence: 1}, {Kind: "update", Sequence: 2}}) {
		t.Fatalf("delivery metadata = %#v", deliveries)
	}
}

func TestSourceStateHydratesReentrantSubscriptionBeforeReturning(t *testing.T) {
	state := &SourceState[int]{}
	var events []string
	var stopInner func()
	stop := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		if value != 1 {
			return
		}
		events = append(events, "outer")
		stopInner = state.Subscribe(func(_ int, _ context.Context, delivery ReplicatedStateDelivery) {
			events = append(events, delivery.Kind)
		})
		events = append(events, "returned")
	})
	defer stop()
	state.replace(t.Context(), 1)()
	defer stopInner()
	if !reflect.DeepEqual(events, []string{"outer", "hydrate", "returned"}) {
		t.Fatalf("subscription hydration order = %v", events)
	}
}

func TestSourceStateContinuesDeliveryAfterListenerPanics(t *testing.T) {
	state := &SourceState[int]{}
	first, second := errors.New("first listener"), errors.New("second listener")
	stopFirst := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		if value != 0 {
			panic(first)
		}
	})
	stopSecond := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) {
		if value != 0 {
			panic(second)
		}
	})
	var values []int
	stop := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) { values = append(values, value) })
	defer stop()
	failure := capturePanic(func() { state.replace(t.Context(), 1)() })
	err, ok := failure.(error)
	if !ok || err.Error() != "Replicated state listeners failed" || !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("publication failure = %v", failure)
	}
	stopFirst()
	stopSecond()
	state.replace(t.Context(), 2)()
	if !reflect.DeepEqual(values, []int{0, 1, 2}) {
		t.Fatalf("delivery stopped after listener error: %v", values)
	}
}

func TestSourceStateRemovesListenerThatPanicsDuringHydration(t *testing.T) {
	state := &SourceState[int]{}
	failure := errors.New("hydrate")
	got := capturePanic(func() {
		state.Subscribe(func(int, context.Context, ReplicatedStateDelivery) { panic(failure) })
	})
	err, ok := got.(error)
	if !ok || !errors.Is(err, failure) {
		t.Fatalf("hydration panic = %v", got)
	}
	state.replace(t.Context(), 1)()
	if len(state.listeners) != 0 {
		t.Fatal("failed hydration retained its listener")
	}
}

// Chord snapshots membership at delivery, not at commit or before each callback.
func TestSourceStateSiblingUnsubscribeMatchesPinnedChord(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "node", "testdata/state-listeners-oracle.mjs").Output()
	if err != nil {
		t.Fatalf("pinned Chord state probe: %v", err)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("pinned Chord state JSON: %v\n%s", err, output)
	}
	state := &SourceState[int]{}
	var events []string
	var removeSecond func()
	state.Subscribe(func(value int, ctx context.Context, delivery ReplicatedStateDelivery) {
		if delivery.Kind != "update" {
			return
		}
		if value == 1 {
			events = append(events, "first:1")
			state.replace(ctx, 2)()
			removeSecond()
		} else {
			events = append(events, "first:2")
		}
	})
	removeSecond = state.Subscribe(func(value int, _ context.Context, delivery ReplicatedStateDelivery) {
		if delivery.Kind == "update" {
			if value == 1 {
				events = append(events, "second:1")
			} else {
				events = append(events, "second:2")
			}
		}
	})
	state.replace(t.Context(), 1)()
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("listener snapshot differs: Go %v; Chord %v", events, want)
	}
}

func TestSourceStateHydrationSkipsAlreadyCommittedPublications(t *testing.T) {
	state := &SourceState[int]{}
	state.replace(t.Context(), 1)
	state.replace(t.Context(), 2)
	var values []int
	stop := state.Subscribe(func(value int, _ context.Context, _ ReplicatedStateDelivery) { values = append(values, value) })
	defer stop()
	state.deliver()
	state.replace(t.Context(), 3)()
	if !reflect.DeepEqual(values, []int{2, 3}) {
		t.Fatalf("subscriber replayed state older than its hydration: %v", values)
	}
}

func capturePanic(call func()) (failure any) {
	defer func() { failure = recover() }()
	call()
	return nil
}

func TestConnectionStateJSON(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{ServerConnectionState{Status: "connecting", Attempt: 2}, `{"status":"connecting","attempt":2}`},
		{ServerConnectionState{Status: "connected", Since: "now"}, `{"status":"connected","since":"now"}`},
		{ServerConnectionState{Status: "disconnected", Since: "now", Reason: "closed"}, `{"status":"disconnected","since":"now","reason":"closed","retryAt":null}`},
		{SessionAttachmentState{Status: "detached"}, `{"status":"detached"}`},
		{SessionAttachmentState{Status: "attaching", SessionID: "s"}, `{"status":"attaching","sessionId":"s"}`},
		{SessionAttachmentState{Status: "attached", SessionID: "s"}, `{"status":"attached","sessionId":"s"}`},
		{SessionAttachmentState{Status: "attached", SessionID: ""}, `{"status":"attached","sessionId":""}`},
		{SessionAttachmentState{Status: "degraded", SessionID: "s"}, `{"status":"degraded","sessionId":"s"}`},
	}
	for _, tt := range cases {
		got, err := json.Marshal(tt.value)
		if err != nil || string(got) != tt.want {
			t.Fatalf("state = %s, %v; want %s", got, err, tt.want)
		}
	}
}
