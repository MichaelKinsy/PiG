package pico3

import (
	"context"
	"errors"
	"testing"
)

func TestMembraneRevokesSuccessfulAndFailedTransactionHandles(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			env := openEnv(t, openOptions{})
			state := must(env.h.Namespace("membrane", NamespaceDefaults{Rewindable: JsonObject{"list": []any{}}, Sticky: JsonObject{"unrelated": false}}, nil))
			var root, nested *Node
			_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
				root = must(tx.Plugins(state))
				root.Set("list", []any{1})
				nested = root.Get("list").(*Node)
				if fail {
					return nil, errors.New("boom")
				}
				return nil, nil
			})
			if fail {
				if err == nil {
					t.Fatal("failed callback accepted")
				}
			} else {
				check(t, err)
			}
			for _, operation := range []func(){func() { root.Set("list", nil) }, func() { nested.Push(2) }, func() { root.Get("list") }} {
				assertMembranePanic(t, "outside its transaction", operation)
			}
			_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
				must(tx.Plugins(state)).Set("unrelated", true)
				return nil, nil
			})
			check(t, err)
			want := []any{}
			if !fail {
				want = []any{float64(1)}
			}
			equal(t, obj(must(env.root.Rewindable(bg)), "plugins")["membrane"], JsonObject{"list": want}, "no escaped mutations")
		})
	}
}

func TestMembraneArrayCallbackHandlesExpireAfterCommit(t *testing.T) {
	env := openEnv(t, openOptions{})
	state := must(env.h.Namespace("callback", NamespaceDefaults{Rewindable: JsonObject{"list": []any{}}, Sticky: JsonObject{"unrelated": false}}, nil))
	var escaped []*Node
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		list := must(tx.Plugins(state)).Get("list").(*Node)
		list.Push(JsonObject{"mode": "safe"})
		for i := range list.Len() {
			escaped = append(escaped, list.Index(i).(*Node))
		}
		list.RemoveWhere(func(value any) bool { escaped = append(escaped, value.(*Node)); return false })
		return nil, nil
	})
	check(t, err)
	for _, item := range escaped {
		assertMembranePanic(t, "outside its transaction", func() { item.Set("mode", "escaped") })
	}
	_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		must(tx.Plugins(state)).Set("unrelated", true)
		return nil, nil
	})
	check(t, err)
	equal(t, obj(must(env.root.Rewindable(bg)), "plugins")["callback"], JsonObject{"list": []any{JsonObject{"mode": "safe"}}}, "callback isolation")
}

func TestRuntimeDocumentHandleExpiresAtCommitEnd(t *testing.T) {
	var state *Namespace
	reported := make(chan any, 1)
	kind := &Kind{Name: "escaped-handle", Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		var escaped *Node
		_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			escaped = must(tx.Plugins(state))
			escaped.Set("v", 1)
			return nil, nil
		})
		if err != nil {
			return Step{}, err
		}
		func() { defer func() { reported <- recover() }(); escaped.Set("v", 2) }()
		return done(Completed(nil)), nil
	}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
	state = must(env.h.Namespace("runtime", NamespaceDefaults{Sticky: JsonObject{"v": 0}}, nil))
	ref := createTestTask(t, env, kind, nil)
	env.untilTerminal(ref.Id)
	if _, ok := (<-reported).(*TypeError); !ok {
		t.Fatal("runtime handle did not expire")
	}
	equal(t, obj(obj(env.sticky(), "plugins"), "runtime")["v"], 1, "original value")
}
