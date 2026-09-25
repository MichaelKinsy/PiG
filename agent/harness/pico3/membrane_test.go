package pico3

import (
	"strings"
	"testing"
)

func assertMembranePanic(t *testing.T, contains string, fn func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(value.(error).Error(), contains) {
			t.Errorf("panic=%v; want %s", value, contains)
		}
	}()
	fn()
}

func TestMembraneIdentityRevocationAndPlainInputs(t *testing.T) {
	target := JsonObject{"a": JsonObject{"b": []any{float64(1), float64(2)}}}
	membrane := NewMembrane("test")
	root := membrane.Wrap(target)
	nested := root.Get("a").(*Node)
	if root.Get("a") != nested {
		t.Fatal("wrapper identity changed")
	}
	array := nested.Get("b").(*Node)
	array.Push(3)
	if array.Len() != 3 {
		t.Fatal("push not forwarded")
	}
	assertMembranePanic(t, "document proxy", func() { root.Set("c", JsonObject{"nested": nested}) })
	assigned := JsonObject{"value": float64(1)}
	root.Set("copy", assigned)
	assigned["value"] = float64(2)
	if root.Get("copy").(*Node).Get("value") != float64(1) {
		t.Fatal("assigned input aliased")
	}
	membrane.Revoke()
	for _, operation := range []func(){func() { root.Get("a") }, func() { nested.Keys() }, func() { array.Len() }, func() { array.Push(4) }, func() { nested.Set("b", []any{}) }, func() { nested.Has("b") }} {
		assertMembranePanic(t, "outside its transaction", operation)
	}
}

func TestMembraneCallbacksCannotLeakMutableValues(t *testing.T) {
	membrane := NewMembrane("callback")
	root := membrane.Wrap(JsonObject{"items": []any{JsonObject{"safe": true}}})
	var escaped any
	root.Get("items").(*Node).RemoveWhere(func(value any) bool { escaped = value; return false })
	node, ok := escaped.(*Node)
	if !ok {
		t.Fatalf("callback leaked raw %T", escaped)
	}
	membrane.Revoke()
	assertMembranePanic(t, "outside its transaction", func() { node.Set("safe", false) })
}

func TestMembraneRetainedObjectDoesNotRetargetAfterReplacement(t *testing.T) {
	membrane := NewMembrane("identity")
	target := JsonObject{"object": JsonObject{"value": "old"}}
	root := membrane.Wrap(target)
	old := root.Get("object").(*Node)
	root.Set("object", JsonObject{"value": "new"})
	old.Set("value", "detached")
	if got := root.Get("object").(*Node).Get("value"); got != "new" {
		t.Fatalf("old wrapper mutated replacement: %v", got)
	}
}

func TestMembraneRejectsCyclesAndPrototypeChanges(t *testing.T) {
	membrane := NewMembrane("plain")
	root := membrane.Wrap(JsonObject{})
	cycle := JsonObject{}
	cycle["self"] = cycle
	assertMembranePanic(t, "non-JSON", func() { root.Set("cycle", cycle) })
	assertMembranePanic(t, "cannot change prototypes", func() { root.Set("__proto__", JsonObject{}) })
}

func TestMembraneRetainedArrayDoesNotRetargetAfterReplacement(t *testing.T) {
	membrane := NewMembrane("array")
	root := membrane.Wrap(JsonObject{"list": []any{float64(1)}})
	old := root.Get("list").(*Node)
	old.Push(2)
	if root.Get("list") != old {
		t.Fatal("array append changed wrapper identity")
	}
	root.Set("list", []any{float64(3)})
	old.Push(4)
	current := root.Get("list").(*Node)
	if current.Len() != 1 || current.Index(0) != float64(3) {
		t.Fatalf("old wrapper changed replacement: %v", current.Plain())
	}
}
