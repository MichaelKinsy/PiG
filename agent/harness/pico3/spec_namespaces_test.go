package pico3

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNamespaceDefaultsRouteAndPreserveNull(t *testing.T) {
	env := openEnv(t, openOptions{})
	ns := must(env.h.Namespace("spec.routing", NamespaceDefaults{Rewindable: JsonObject{"plan": JsonObject{"enabled": false}}, Sticky: JsonObject{"cache": nil}, Session: JsonObject{"global": JsonObject{"count": 0}}}, nil))
	first := must(env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		slice := must(tx.Plugins(ns))
		equal(t, slice.Get("cache"), nil, "null")
		slice.Get("plan").(*Node).Set("enabled", true)
		slice.Get("global").(*Node).Set("count", 1)
		return slice.Plain(), nil
	}))
	equal(t, first, JsonObject{"plan": JsonObject{"enabled": true}, "cache": nil, "global": JsonObject{"count": 1}}, "merged slice")
	equal(t, obj(must(env.root.Rewindable(bg)), "plugins")[ns.Id], JsonObject{"plan": JsonObject{"enabled": true}}, "rewindable route")
	equal(t, obj(must(env.root.Sticky(bg)), "plugins")[ns.Id], JsonObject{"cache": nil}, "sticky route")
	global := must(env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		return must(tx.Plugins(ns)).Get("global").(*Node).Plain(), nil
	}))
	equal(t, global, JsonObject{"count": 1}, "session route")
}

func TestNamespaceReplacementRetainsStateAndAddsDefaults(t *testing.T) {
	env := openEnv(t, openOptions{})
	old := must(env.h.Namespace("spec.reload", NamespaceDefaults{Rewindable: JsonObject{"value": 1}}, nil))
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { must(tx.Plugins(old)).Set("value", 7); return nil, nil })
	check(t, err)
	old.Unregister()
	old.Unregister()
	_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { return tx.Plugins(old) })
	if _, ok := errors.AsType[*Forbidden](err); !ok {
		t.Fatalf("stale namespace: %v", err)
	}
	current := must(env.h.Namespace("spec.reload", NamespaceDefaults{Rewindable: JsonObject{"value": 100, "added": "new-default"}}, nil))
	state := must(env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { return must(tx.Plugins(current)).Plain(), nil }))
	equal(t, state, JsonObject{"value": 7, "added": "new-default"}, "retained state")
	old.Unregister()
	_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		must(tx.Plugins(current)).Set("added", "still-current")
		return nil, nil
	})
	check(t, err)
	state = must(env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { return must(tx.Plugins(current)).Get("added"), nil }))
	equal(t, state, "still-current", "current registration")
}

func TestNamespaceRejectsAmbiguityReservedAndStaleEmit(t *testing.T) {
	env := openEnv(t, openOptions{})
	for _, name := range []string{"pi.private", "bad space"} {
		if _, err := env.h.Namespace(name, NamespaceDefaults{}, nil); err == nil || !strings.Contains(err.Error(), "invalid namespace") {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	_, err := env.h.Namespace("spec.ambiguous", NamespaceDefaults{Rewindable: JsonObject{"duplicate": 1}, Sticky: JsonObject{"duplicate": 2}}, nil)
	if err == nil || !strings.Contains(err.Error(), "more than one document") {
		t.Fatalf("ambiguous: %v", err)
	}
	token := must(env.h.Namespace("spec.unique", NamespaceDefaults{Sticky: JsonObject{"value": 0}}, nil))
	_, err = env.h.Namespace("spec.unique", NamespaceDefaults{}, nil)
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate: %v", err)
	}
	token.Unregister()
	_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		return nil, tx.Emit(token, "late", JsonObject{"value": true})
	})
	if _, ok := errors.AsType[*Forbidden](err); !ok {
		t.Fatalf("stale emit: %v", err)
	}
}

func TestNamespaceProjectionAndEventAreOneEnvelope(t *testing.T) {
	env := openEnv(t, openOptions{})
	ns := must(env.h.Namespace("spec.presentation", NamespaceDefaults{Rewindable: JsonObject{"visible": 0}, Sticky: JsonObject{"secret": "hidden"}}, func(slice JsonObject) (JsonValue, error) { return JsonObject{"visible": slice["visible"]}, nil }))
	collector := collectWatch(t, env.root)
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		slice := must(tx.Plugins(ns))
		slice.Set("visible", 2)
		slice.Set("secret", "do-not-project")
		return nil, tx.Emit(ns, "changed", JsonObject{"visible": 2})
	})
	check(t, err)
	envelopes := collector.Envelopes()
	equal(t, len(envelopes), 1, "atomic envelope")
	folded := must(ApplyEnvelope(collector.view, envelopes[0]))
	equal(t, obj(folded, "plugins")[ns.Id], JsonObject{"visible": 2}, "projection")
	if strings.Contains(string(mustJSON(folded["plugins"])), "do-not-project") {
		t.Fatal("private state leaked")
	}
	equal(t, envelopes[0].Events, []ViewEvent{{"type": "plugin.spec.presentation.changed", "data": JsonObject{"visible": float64(2)}}}, "namespaced event")
}

func TestMemoOncePreservesNullAndIsolatesSlots(t *testing.T) {
	first, second := JsonObject{}, JsonObject{}
	equal(t, MemoOnce(first, "decision", nil), nil, "first null")
	equal(t, MemoOnce(first, "decision", "later"), nil, "null wins")
	equal(t, MemoOnce(second, "decision", "other"), "other", "other slot")
	equal(t, first, JsonObject{"memos": JsonObject{"decision": nil}}, "first state")
	equal(t, second, JsonObject{"memos": JsonObject{"decision": "other"}}, "second state")
}
