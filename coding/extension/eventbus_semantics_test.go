package extension

import (
	"slices"
	"testing"
)

// event-bus.ts:12-33 createEventBus is a Node EventEmitter: emit runs a snapshot of the channel's listeners in registration order, so a handler that unsubscribes
// another (or subscribes a new one) during the emit changes the next emit only; an unsubscribe removes exactly its own handler, a second call does nothing, and
// Clear removes every listener of every channel.
// mutation-checked: iterating the live list, deleting the wrong listener, or not clearing fails.
func TestEventBusControllerEmitsToASnapshotAndUnsubscribesExactlyItsHandler(t *testing.T) {
	bus := CreateEventBus()
	if _, ok := any(bus).(EventBusController); !ok {
		t.Fatalf("%T is not an EventBusController", bus)
	}
	if _, ok := any(bus).(EventBus); !ok {
		t.Fatalf("%T is not an EventBus", bus)
	}
	var calls []string
	var unsubscribeSecond func()
	bus.On("c", func(any) {
		calls = append(calls, "first")
		unsubscribeSecond()
		bus.On("c", func(any) { calls = append(calls, "added") })
	})
	unsubscribeSecond = bus.On("c", func(any) { calls = append(calls, "second") })
	bus.On("c", func(any) { calls = append(calls, "third") })
	bus.Emit("c", nil)
	if want := []string{"first", "second", "third"}; !slices.Equal(calls, want) {
		t.Fatalf("first emit called %v, want the snapshot %v", calls, want)
	}
	calls = nil
	unsubscribeSecond()
	bus.Emit("c", nil)
	if want := []string{"first", "third", "added"}; !slices.Equal(calls, want) {
		t.Fatalf("second emit called %v, want %v (second gone, added present, no duplicate removal of a neighbour)", calls, want)
	}
	other := 0
	bus.On("other", func(any) { other++ })
	bus.Clear()
	calls = nil
	bus.Emit("c", nil)
	bus.Emit("other", nil)
	if len(calls) != 0 || other != 0 {
		t.Fatalf("after Clear: channel c called %v, channel other %d times", calls, other)
	}
}
