package agentharness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// Review P3: structuredClone keeps shared identity inside the clone while
// isolating the original.
func TestCloneHarnessEventPreservesAliases(t *testing.T) {
	shared := map[string]any{"v": "old"}
	list := []any{"a", "b"}
	original := HarnessEvent{Payload: ToolStartPayload{RunID: "r", Args: map[string]any{"left": shared, "right": shared, "l1": list, "l2": list}}, Lane: "main"}
	clone := CloneHarnessEvent(original)
	args := clone.Payload.(ToolStartPayload).Args.(map[string]any)
	args["left"].(map[string]any)["v"] = "new"
	args["l1"].([]any)[0] = "changed"
	if got := args["right"].(map[string]any)["v"]; got != "new" {
		t.Fatalf("clone right = %v, want new (shared identity kept)", got)
	}
	if got := args["l2"].([]any)[0]; got != "changed" {
		t.Fatalf("clone l2 = %v, want changed (shared slice kept)", got)
	}
	if shared["v"] != "old" || list[0] != "a" {
		t.Fatalf("original mutated: %v %v", shared, list)
	}
}

// Upstream events.ts structuredClone copies Map keys and preserves aliases between keys and other references in the event. Recipient mutations must not reach the emitter or another recipient.
func TestHarnessEventBusClonesMapKeysAndTheirAliases(t *testing.T) {
	type object struct{ Value string }
	key := &object{Value: "original"}
	event := HarnessEvent{Payload: ToolStartPayload{Args: map[string]any{
		"key": key, "map": map[*object]string{key: "value"},
	}}}
	assertClone := func(event HarnessEvent) *object {
		t.Helper()
		args := event.Payload.(ToolStartPayload).Args.(map[string]any)
		clonedKey := args["key"].(*object)
		clonedMap := args["map"].(map[*object]string)
		if got := clonedMap[clonedKey]; got != "value" {
			t.Errorf("cloned map lookup by cloned key = %q, want value", got)
		}
		for mapKey := range clonedMap {
			if mapKey == key {
				t.Error("cloned map retained the original key")
			}
			if mapKey.Value != "original" {
				t.Errorf("recipient observed another recipient's mutation: %s", mapKey.Value)
			}
			mapKey.Value = "mutated by recipient"
		}
		if clonedKey.Value != "mutated by recipient" {
			t.Errorf("key alias did not observe map key mutation: %s", clonedKey.Value)
		}
		return clonedKey
	}
	assertClone(CloneHarnessEvent(event))
	bus := NewHarnessEventBus()
	calls := 0
	var previous *object
	listener := func(_ harness.Context, event HarnessEvent) error {
		clonedKey := assertClone(event)
		if previous == clonedKey {
			t.Error("recipients shared a cloned key")
		}
		previous = clonedKey
		calls++
		return nil
	}
	mustSubscribe(t)(bus.On(EventToolStart, listener))
	mustSubscribe(t)(bus.On(EventToolStart, listener))
	bus.Emit(context.Background(), event)
	if calls != 2 {
		t.Fatalf("listener calls = %d, want one per registration", calls)
	}
	if key.Value != "original" {
		t.Errorf("recipient mutated the emitter's map key: %s", key.Value)
	}
}

// structuredClone copies cyclic graphs; the clone's cycle points into the
// clone.
func TestCloneHarnessEventCopiesCycles(t *testing.T) {
	cyclic := map[string]any{"name": "root"}
	cyclic["self"] = cyclic
	loop := []any{nil}
	loop[0] = loop
	clone := CloneHarnessEvent(HarnessEvent{Payload: ToolStartPayload{Args: map[string]any{"m": cyclic, "s": loop}}})
	args := clone.Payload.(ToolStartPayload).Args.(map[string]any)
	m := args["m"].(map[string]any)
	m["name"] = "cloned"
	if m["self"].(map[string]any)["name"] != "cloned" || cyclic["name"] != "root" {
		t.Fatalf("cycle not preserved/isolated: clone=%v original=%v", m["self"].(map[string]any)["name"], cyclic["name"])
	}
	s := args["s"].([]any)
	s[0] = "x"
	if loop[0] == "x" {
		t.Fatal("original slice mutated through the clone")
	}
}
