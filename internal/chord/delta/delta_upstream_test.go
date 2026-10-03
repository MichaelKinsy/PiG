package delta

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/chord/test/delta.test.ts. The two cases that install an inherited JavaScript setter are equivalence ports: a Go map has no prototype chain, so they assert the invariant the setter guards (reserved and inherited names are stored as own data, byte-identical to Pi's output, with no side effect on other values) on the same inputs.

func TestDeltaImmutableTrackerLifecycle(t *testing.T) {
	t.Run("keeps a draft alive across await and adopts only a prepared change", func(t *testing.T) {
		// delta.test.ts:19-45. Go has no await; the draft stays open across an arbitrary gap.
		input := parse(t, `{"count":1,"nested":{"text":"a"},"values":[1]}`)
		tracker := Track(input)
		expectSame(t, tracker.Value(), input, "tracker.value")

		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state["count"] = 2.0
		nested := obj(t, state["nested"])
		nested["text"] = nested["text"].(string) + "b"
		state["values"] = append(arr(t, state["values"]), 2.0)
		expectJSON(t, tracker.Value(), `{"count":1,"nested":{"text":"a"},"values":[1]}`)

		prepared := mustPrepare(t, change)
		expectSame(t, prepared.Base, tracker.Value(), "prepared.base")
		expectJSON(t, prepared.Value, `{"count":2,"nested":{"text":"ab"},"values":[1,2]}`)
		expectJSON(t, prepared.Ops, `[["s",["count"],2],["a",["nested","text"],"b"],["p",["values"],1,0,[2]]]`)
		_, err := change.State()
		expectTypeError(t, err)
		expectJSON(t, tracker.Value(), `{"count":1,"nested":{"text":"a"},"values":[1]}`)

		expectNoErr(t, tracker.Adopt(prepared))
		expectSame(t, tracker.Value(), prepared.Value, "tracker.value")
	})

	t.Run("aborts and revokes without changing the committed value", func(t *testing.T) {
		// delta.test.ts:47-60. A held child handle cannot be revoked in Go; the change refuses new use.
		tracker := Track(parse(t, `{"child":{"value":1}}`))
		change := tracker.BeginChange()
		child := obj(t, obj(t, mustState(t, change))["child"])
		child["value"] = 2.0
		change.Abort()
		expectJSON(t, obj(t, tracker.Value())["child"], `{"value":1}`)
		_, err := change.State()
		expectTypeError(t, err)
		change.Abort()
		_, err = change.Prepare()
		expectErr(t, err, "settled")
		tracker.BeginChange().Abort()
	})

	t.Run("grows arrays with explicit nulls and revokes drafts after preparation", func(t *testing.T) {
		// delta.test.ts:62-70. Setting length has no Go form; appending two nulls is the same growth.
		tracker := Track(parse(t, `{"values":[1,2]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state["values"] = append(arr(t, state["values"]), nil, nil)
		prepared := mustPrepare(t, change)
		expectJSON(t, prepared.Value, `{"values":[1,2,null,null]}`)
		_, err := change.State()
		expectTypeError(t, err)
		expectNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("normalizes a deep no-op to exact previous identity", func(t *testing.T) {
		tracker := Track(parse(t, `{"value":{"nested":[1,2]}}`))
		change := tracker.BeginChange()
		obj(t, mustState(t, change))["value"] = parse(t, `{"nested":[1,2]}`)
		prepared := mustPrepare(t, change)
		expectSame(t, prepared.Value, prepared.Base, "prepared.value")
		if len(prepared.Ops) != 0 {
			t.Fatalf("ops=%v", prepared.Ops)
		}
		expectNoErr(t, tracker.Adopt(prepared))
		expectSame(t, tracker.Value(), prepared.Base, "tracker.value")
	})

	t.Run("takes immutable ownership of replacement input and applies no-op normalization", func(t *testing.T) {
		tracker := Track(parse(t, `{"nested":{"value":1}}`))
		replacement := parse(t, `{"nested":{"value":2}}`)
		prepared := tracker.PrepareReplace(replacement)
		expectSame(t, prepared.Value, replacement, "prepared.value")
		if len(prepared.Ops) != 1 || prepared.Ops[0].Verb() != "r" {
			t.Fatalf("ops=%v", prepared.Ops)
		}
		expectSame(t, prepared.Ops[0][1], replacement, "op payload")
		expectNoErr(t, tracker.Adopt(prepared))

		noOp := tracker.PrepareReplace(parse(t, `{"nested":{"value":2}}`))
		expectSame(t, noOp.Value, tracker.Value(), "noOp.value")
		if len(noOp.Ops) != 0 {
			t.Fatalf("ops=%v", noOp.Ops)
		}
	})

	t.Run("shares immutable operation placements with the prepared candidate", func(t *testing.T) {
		tracker := Track(parse(t, `{"rows":[]}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state["rows"] = append(arr(t, state["rows"]), parse(t, `{"id":1}`))
		prepared := mustPrepare(t, change)
		splice := prepared.Ops[0]
		if splice.Verb() != "p" {
			t.Fatalf("expected splice, got %v", splice)
		}
		expectSame(t, splice[4].([]any)[0], arr(t, obj(t, prepared.Value)["rows"])[0], "placement")
	})

	t.Run("rejects foreign, stale, and repeated preparations while allowing competing changes", func(t *testing.T) {
		first, second := Track(parse(t, `{"value":0}`)), Track(parse(t, `{"value":0}`))
		change, competing := first.BeginChange(), first.BeginChange()
		obj(t, mustState(t, change))["value"] = 1.0
		obj(t, mustState(t, competing))["value"] = 2.0
		prepared, competingPrepared := mustPrepare(t, change), mustPrepare(t, competing)
		expectErr(t, second.Adopt(prepared), "different tracker")
		expectNoErr(t, first.Adopt(prepared))
		expectErr(t, first.Adopt(prepared), "already been used")
		expectErr(t, first.Adopt(competingPrepared), "stale")

		stale := first.PrepareReplace(parse(t, `{"value":2}`))
		winner := first.PrepareReplace(parse(t, `{"value":3}`))
		expectNoErr(t, first.Adopt(winner))
		expectErr(t, first.Adopt(stale), "stale")
		expectErr(t, first.Adopt(stale), "stale")
	})

	t.Run("invalidates a prepared result when its change is aborted", func(t *testing.T) {
		tracker := Track(parse(t, `{"value":0}`))
		change := tracker.BeginChange()
		obj(t, mustState(t, change))["value"] = 1.0
		prepared := mustPrepare(t, change)
		change.Abort()
		expectErr(t, tracker.Adopt(prepared), "aborted")
		change.Abort()
	})

	t.Run("makes competing same-base preparations stale after adopting a no-op", func(t *testing.T) {
		tracker := Track(parse(t, `{"value":{"count":1}}`))
		first := tracker.PrepareReplace(parse(t, `{"value":{"count":1}}`))
		competing := tracker.PrepareReplace(parse(t, `{"value":{"count":1}}`))
		expectSame(t, first.Value, first.Base, "first")
		expectSame(t, competing.Value, competing.Base, "competing")
		expectNoErr(t, tracker.Adopt(first))
		expectErr(t, tracker.Adopt(first), "already been used")
		expectErr(t, tracker.Adopt(competing), "stale")
	})

	t.Run("emits an owned root replacement without traversing large replacement input", func(t *testing.T) {
		rows := make([]any, 10_000)
		for at := range rows {
			rows[at] = map[string]any{"value": float64(at), "stable": map[string]any{"value": float64(at)}}
		}
		tracker := Track(map[string]any{"rows": rows})
		replaced := append([]any(nil), arr(t, obj(t, tracker.Value())["rows"])...)
		replaced[5_000] = map[string]any{"value": -1.0, "stable": obj(t, replaced[5_000])["stable"]}
		replacement := map[string]any{"rows": replaced}
		prepared := tracker.PrepareReplace(replacement)
		expectSame(t, prepared.Value, replacement, "prepared.value")
		if len(prepared.Ops) != 1 || prepared.Ops[0].Verb() != "r" {
			t.Fatalf("ops=%v", prepared.Ops)
		}
		expectSame(t, prepared.Ops[0][1], replacement, "op payload")
		expectEqual(t, mustApplyImmutable(t, prepared.Base, prepared.Ops), prepared.Value)
	})
}

func TestDeltaCanonicalStrings(t *testing.T) {
	t.Run("emits append and rolling-window operations", func(t *testing.T) {
		tracker := Track(parse(t, `{"text":"abcdefgh"}`))
		change := tracker.BeginChange()
		state := obj(t, mustState(t, change))
		state["text"] = state["text"].(string) + "ij"
		prepared := mustPrepare(t, change)
		expectJSON(t, prepared.Ops, `[["a",["text"],"ij"]]`)
		expectNoErr(t, tracker.Adopt(prepared))

		change = tracker.BeginChange()
		state = obj(t, mustState(t, change))
		state["text"] = state["text"].(string)[3:] + "xyz"
		prepared = mustPrepare(t, change)
		expectJSON(t, prepared.Ops, `[["t",["text"],3],["a",["text"],"xyz"]]`)
	})

	t.Run("finds bounded overlaps", func(t *testing.T) {
		if got := Overlap("abcdefgh", "defghxyz", 65_536); got != 5 {
			t.Fatalf("overlap=%d", got)
		}
		if got := Overlap("abcdef", "defghi", 0); got != 0 {
			t.Fatalf("overlap=%d", got)
		}
	})
}

func TestDeltaOperationApplicationAndValidation(t *testing.T) {
	t.Run("applies mutable and immutable operations", func(t *testing.T) {
		operations := ops(t, `[["a",["text"],"b"],["p",["values"],1,1,[3,4]],["s",["nested","value"],2]]`)
		base := parse(t, `{"text":"a","values":[1,2],"nested":{"value":1},"stable":{"value":9}}`)
		immutable := mustApplyImmutable(t, base, operations)
		expectJSON(t, immutable, `{"text":"ab","values":[1,3,4],"nested":{"value":2},"stable":{"value":9}}`)
		expectJSON(t, base, `{"text":"a","values":[1,2],"nested":{"value":1},"stable":{"value":9}}`)
		expectSame(t, obj(t, immutable)["stable"], obj(t, base)["stable"], "stable")
		expectEqual(t, mustApply(t, clone(t, base), operations), immutable)
	})

	t.Run("supports root replacement, root splice, and permutations", func(t *testing.T) {
		base := ops(t, `[["r",[1,2,3]]]`)
		if !IsBase(base) {
			t.Fatal("IsBase")
		}
		value := mustApply(t, nil, base)
		value = mustApply(t, value, ops(t, `[["p",[],1,1,[4]]]`))
		value = mustApply(t, value, ops(t, `[["m",[],[2,0,1]]]`))
		expectJSON(t, value, `[3,1,4]`)
	})

	t.Run("rejects unsafe and malformed paths", func(t *testing.T) {
		_, err := Apply(map[string]any{}, ops(t, `[["s",["constructor","prototype","x"],true]]`))
		expectUnsafePath(t, err)
		_, err = Apply(parse(t, `{"values":[1]}`), ops(t, `[["s",["values",3],2]]`))
		expectUnsafePath(t, err)
		_, err = Apply(parse(t, `{"value":1}`), ops(t, `[["a",["value"],"x"]]`))
		expectErr(t, err, "")
		expectErr(t, AssertValidOp(Op{"s", "value", 1.0}), "")
		expectErr(t, AssertValidOp(Op{"m", []any{}, []any{0.0, 0.0}}), "bijection")
	})

	t.Run("validates immutable operations before traversing the target", func(t *testing.T) {
		// delta.test.ts:225-237. The JavaScript target is a getter that counts reads; a Go map has no getters, so
		// the observable guarantee is that a malformed path fails validation and leaves the target untouched.
		target := parse(t, `{"trap":{}}`)
		_, err := ApplyImmutable(target, []Op{{"s", "bad-path", 1.0}})
		expectErr(t, err, "path")
		expectJSON(t, target, `{"trap":{}}`)
	})

	t.Run("validates decoded and wire vocabularies separately", func(t *testing.T) {
		expectNoErr(t, AssertValidOp(Op{"s", []any{"value"}, 1.0}))
		expectErr(t, AssertValidOp(Op{"s", 1.0}), "")
		expectNoErr(t, AssertValidWireOp(WireOp{"s", 1.0}))
		expectNoErr(t, AssertValidWireOp(WireOp{"#", 0.0, []any{"value"}}))
	})
}

func TestDeltaCodec(t *testing.T) {
	path := []any{"nested", "text"}
	decodeAll := func(t *testing.T, dec *Decoder, wire []WireOp) []Op {
		t.Helper()
		out, err := dec.Decode(wire)
		expectNoErr(t, err)
		return out
	}
	encodeAll := func(t *testing.T, enc *Encoder, operations []Op) []WireOp {
		t.Helper()
		out, err := enc.Encode(operations)
		expectNoErr(t, err)
		return out
	}

	t.Run("interns paths, omits adjacent paths, and round-trips", func(t *testing.T) {
		enc, dec := NewEncoder(), NewDecoder()
		first := []Op{{"t", path, 1}, {"a", path, "x"}}
		expectEqual(t, decodeAll(t, dec, encodeAll(t, enc, first)), first)
		second := []Op{{"a", path, "y"}}
		wire := encodeAll(t, enc, second)
		expectJSON(t, wire, `[["#",0,["nested","text"]],["a",0,"y"]]`)
		expectEqual(t, decodeAll(t, dec, wire), second)
	})

	t.Run("resets path dictionaries on a base", func(t *testing.T) {
		enc := NewEncoder()
		p := []any{"value"}
		encodeAll(t, enc, []Op{{"s", p, 1}})
		encodeAll(t, enc, []Op{{"s", p, 2}})
		expectJSON(t, encodeAll(t, enc, []Op{{"r", map[string]any{"value": 3.0}}}), `[["r",{"value":3}]]`)
		expectJSON(t, encodeAll(t, enc, []Op{{"s", p, 4}}), `[["s",["value"],4]]`)
	})

	t.Run("rejects unresolved short forms and unsafe interned paths", func(t *testing.T) {
		_, err := NewDecoder().Decode([]WireOp{{"a", "x"}})
		expectErr(t, err, "")
		_, err = NewDecoder().Decode([]WireOp{{"#", 0.0, []any{"__proto__"}}, {"s", 0.0, true}})
		expectUnsafePath(t, err)
	})

	t.Run("omits an adjacent repeated path", func(t *testing.T) {
		wire := encodeAll(t, NewEncoder(), []Op{{"s", []any{"value"}, 1}, {"s", []any{"value"}, 2}})
		expectJSON(t, wire, `[["s",["value"],1],["s",2]]`)
	})

	t.Run("interns on second use rather than first", func(t *testing.T) {
		enc := NewEncoder()
		deep := []any{"a", "deep"}
		expectJSON(t, encodeAll(t, enc, []Op{{"a", deep, "1"}}), `[["a",["a","deep"],"1"]]`)
		expectJSON(t, encodeAll(t, enc, []Op{{"a", deep, "2"}}), `[["#",0,["a","deep"]],["a",0,"2"]]`)
	})

	t.Run("does not collide paths containing null characters", func(t *testing.T) {
		operations := []Op{{"s", []any{"a\x00b"}, 1}, {"s", []any{"a", "b"}, 2}}
		expectEqual(t, decodeAll(t, NewDecoder(), encodeAll(t, NewEncoder(), operations)), operations)
	})

	t.Run("clears decoder ids on a base batch", func(t *testing.T) {
		dec := NewDecoder()
		decodeAll(t, dec, []WireOp{{"#", 0.0, []any{"a"}}, {"a", 0.0, "1"}})
		decodeAll(t, dec, []WireOp{{"r", map[string]any{"a": ""}}})
		_, err := dec.Decode([]WireOp{{"a", 0.0, "2"}})
		expectErr(t, err, "")
	})

	t.Run("makes batches after a base self-contained", func(t *testing.T) {
		enc := NewEncoder()
		deep := []any{"a", "deep"}
		encodeAll(t, enc, []Op{{"a", deep, "1"}})
		encodeAll(t, enc, []Op{{"a", deep, "2"}})
		base := encodeAll(t, enc, []Op{{"r", parse(t, `{"a":{"deep":"x"}}`)}})
		after := encodeAll(t, enc, []Op{{"a", deep, "3"}})
		expectJSON(t, after, `[["a",["a","deep"],"3"]]`)
		dec := NewDecoder()
		expectJSON(t, decodeAll(t, dec, base), `[["r",{"a":{"deep":"x"}}]]`)
		expectJSON(t, decodeAll(t, dec, after), `[["a",["a","deep"],"3"]]`)
	})

	t.Run("round-trips deterministic mixed operation streams", func(t *testing.T) {
		enc, dec := NewEncoder(), NewDecoder()
		for index := range 100 {
			batch := []Op{
				{"s", []any{"rows", index, "value"}, index},
				{"a", []any{"output"}, "x"},
				{"p", []any{"tail"}, index, 0, []any{float64(index)}},
			}
			expectEqual(t, decodeAll(t, dec, encodeAll(t, enc, batch)), batch)
		}
	})
}

func TestDeltaImmutableApplicationOwnership(t *testing.T) {
	t.Run("does not mutate a replacement payload targeted by a later operation", func(t *testing.T) {
		replacement := parse(t, `{"nested":{"value":1}}`)
		next := mustApplyImmutable(t, nil, []Op{{"r", replacement}, {"s", []any{"nested", "value"}, 2.0}})
		expectJSON(t, replacement, `{"nested":{"value":1}}`)
		expectJSON(t, next, `{"nested":{"value":2}}`)
	})

	t.Run("adopts a mutable replacement payload rather than copying it", func(t *testing.T) {
		batch := []Op{{"r", map[string]any{"value": 0.0}}}
		first, second := obj(t, mustApply(t, nil, batch)), obj(t, mustApply(t, nil, batch))
		first["value"] = 1.0
		if second["value"] != 1.0 {
			t.Fatalf("second=%v", second)
		}
	})
}

func TestDeltaPathAndPrototypeSafety(t *testing.T) {
	t.Run("rejects constructor walks and forbidden interned paths", func(t *testing.T) {
		_, err := Apply(map[string]any{}, ops(t, `[["s",["constructor","prototype","gadget"],true]]`))
		expectErr(t, err, "")
		_, err = NewDecoder().Decode([]WireOp{{"#", 0.0, []any{"__proto__", "w"}}, {"s", 0.0, true}})
		expectErr(t, err, "")
	})

	t.Run("does not run inherited setters", func(t *testing.T) {
		// Equivalence port of delta.test.ts:374. Pi 1.0.0 apply({}, [["s",[name],1]]) under Node 24 gives {"trap":1} and
		// {"toString":1}, and throws UnsafePathError naming the segment for __proto__, constructor and prototype.
		for _, applier := range []func(JsonValue, []Op) (JsonValue, error){Apply, ApplyImmutable} {
			for _, name := range []string{"trap", "toString"} {
				out, err := applier(map[string]any{}, []Op{{"s", []any{name}, 1.0}})
				expectNoErr(t, err)
				if got, want := jsonText(out), `{"`+name+`":1}`; got != want {
					t.Fatalf("set %s: %s, want Pi's %s", name, got, want)
				}
			}
			for _, name := range []string{"__proto__", "constructor", "prototype"} {
				target := map[string]any{}
				_, err := applier(target, []Op{{"s", []any{name}, 1.0}})
				var unsafe *UnsafePathError
				if !errors.As(err, &unsafe) || unsafe.Segment != name || err.Error() != "unsafe path segment: "+name {
					t.Fatalf("set %s: %v, want Pi's UnsafePathError", name, err)
				}
				if len(target) != 0 {
					t.Fatalf("a rejected set changed its target: %v", target)
				}
			}
		}
	})

	t.Run("imports own properties without invoking inherited setters", func(t *testing.T) {
		// Equivalence port of delta.test.ts:396. Expected text is Pi 1.0.0 track, prepare and encoder output under Node 24
		// for the same inputs; the keys are in sorted order, so Go's sorted map encoding equals Pi's insertion order.
		const imported = `{"__proto__":{"z":1},"constructor":2,"prototype":3,"toString":4,"trap":5}`
		root := parse(t, imported)
		tracker := Track(root)
		expectSame(t, tracker.Value(), root, "tracker.value")
		if got := jsonText(tracker.Value()); got != imported {
			t.Fatalf("imported %s, want %s", got, imported)
		}

		// Pi's tracker.ts emits [["s",["trap"],6]] here. This tracker diffs its working copy with DiffRevisions, which, as
		// diff.ts diffObject, folds any object holding a reserved key into one whole-value write; batches are exact but
		// not canonical (delta/README.md "Operations"), so the batch is asserted to replay to Pi's revision through the
		// wire codec without naming a reserved segment. chord/delta, the tracker.ts port, asserts Pi's exact batch
		// (TestTrackerStoresReservedAndInheritedNamesAsOwnData).
		change := tracker.BeginChange()
		obj(t, mustState(t, change))["trap"] = 6.0
		prepared := mustPrepare(t, change)
		for _, op := range prepared.Ops {
			if op.Verb() != "r" {
				expectNoErr(t, AssertSafePath(op.Path()))
			}
		}
		wire, err := NewEncoder().Encode(prepared.Ops)
		expectNoErr(t, err)
		decoded, err := NewDecoder().Decode(wire)
		expectNoErr(t, err)
		const trapped = `{"__proto__":{"z":1},"constructor":2,"prototype":3,"toString":4,"trap":6}`
		if got := jsonText(mustApplyImmutable(t, root, decoded)); got != trapped || jsonText(prepared.Value) != trapped {
			t.Fatalf("replay %s, value %s", got, jsonText(prepared.Value))
		}

		change = Track(root).BeginChange()
		obj(t, obj(t, mustState(t, change))["__proto__"])["z"] = 2.0
		prepared = mustPrepare(t, change)
		const folded = `[["r",{"__proto__":{"z":2},"constructor":2,"prototype":3,"toString":4,"trap":5}]]`
		if got := jsonText(prepared.Ops); got != folded {
			t.Fatalf("an edit below __proto__ gave %s, want Pi's %s", got, folded)
		}
		if got := jsonText(root); got != imported {
			t.Fatalf("importing and editing changed the source: %s", got)
		}

		if got := jsonText(Track(parse(t, `{"values":[1,2]}`)).Value()); got != `{"values":[1,2]}` {
			t.Fatalf("array import %s", got)
		}
		if got := jsonText(mustApply(t, map[string]any{}, []Op{{"s", []any{"value"}, parse(t, imported)}})); got != `{"value":`+imported+`}` {
			t.Fatalf("value import %s", got)
		}
	})

	t.Run("allows reserved names inside values without prototype pollution", func(t *testing.T) {
		value := parse(t, `{"__proto__":{"z":1}}`)
		out := mustApply(t, map[string]any{}, []Op{{"s", []any{"value"}, value}})
		if _, ok := obj(t, obj(t, out)["value"])["__proto__"]; !ok {
			t.Fatal("reserved own key was dropped")
		}
	})
}

func TestDeltaArrayIndexSafety(t *testing.T) {
	t.Run("writes an existing index and appends exactly one past the end", func(t *testing.T) {
		expectJSON(t, mustApply(t, parse(t, `{"values":[1,2,3]}`), ops(t, `[["s",["values",1],9]]`)), `{"values":[1,9,3]}`)
		expectJSON(t, mustApply(t, parse(t, `{"values":[1,2,3]}`), ops(t, `[["s",["values",3],9]]`)), `{"values":[1,2,3,9]}`)
	})

	t.Run("rejects gaps, huge indices, and string-spelled indices", func(t *testing.T) {
		_, err := Apply(parse(t, `{"values":[1,2,3]}`), ops(t, `[["s",["values",5],9]]`))
		expectErr(t, err, "")
		_, err = Apply(parse(t, `{"values":[]}`), ops(t, `[["s",["values",4294967290],1]]`))
		expectErr(t, err, "")
		_, err = Apply(parse(t, `{"values":[1]}`), ops(t, `[["s",["values","0"],9]]`))
		expectErr(t, err, "")
		_, err = Apply(parse(t, `{"values":["a"]}`), ops(t, `[["a",["values","0"],"b"]]`))
		expectErr(t, err, "")
	})

	t.Run("allows explicit growth values and rejects deletion past the end", func(t *testing.T) {
		expectJSON(t, mustApply(t, parse(t, `{"values":[1]}`), ops(t, `[["p",["values"],1,0,[null,null,9]]]`)), `{"values":[1,null,null,9]}`)
		_, err := Apply(parse(t, `{"values":[1]}`), ops(t, `[["d",["values",1]]]`))
		expectErr(t, err, "")
	})

	t.Run("applies large splice payloads without spreading them at once", func(t *testing.T) {
		items := make([]any, 300_000)
		result := obj(t, mustApply(t, parse(t, `{"values":[]}`), []Op{{"p", []any{"values"}, 0, 0, items}}))
		if got := len(arr(t, result["values"])); got != len(items) {
			t.Fatalf("len=%d", got)
		}
	})
}

func TestDeltaOperationStructureSafety(t *testing.T) {
	t.Run("rejects unknown verbs, malformed tuples, paths, and splice payloads", func(t *testing.T) {
		value := func() any { return parse(t, `{"value":1}`) }
		for _, malformed := range []Op{
			{"ZZZ", []any{"value"}, 9.0},
			{"s", "value", 9.0},
			nil,
			{},
		} {
			if _, err := Apply(value(), []Op{malformed}); err == nil {
				t.Fatalf("accepted %v", malformed)
			}
		}
		_, err := Apply(parse(t, `{"values":[1]}`), []Op{{"p", []any{"values"}, 0.0, 0.0, "not-an-array"}})
		expectErr(t, err, "")
	})

	t.Run("rejects invalid append and truncation operations", func(t *testing.T) {
		_, err := Apply(parse(t, `{"value":1}`), ops(t, `[["a",["missing"],"x"]]`))
		expectErr(t, err, "")
		_, err = Apply(parse(t, `{"value":1}`), ops(t, `[["a",["value"],"x"]]`))
		expectErr(t, err, "")
		_, err = Apply(parse(t, `{"value":"abc"}`), ops(t, `[["t",["value"],-1]]`))
		expectErr(t, err, "")
		_, err = NewDecoder().Decode([]WireOp{{"t", []any{"value"}, -1.0}})
		expectErr(t, err, "")
	})

	t.Run("clamps splice removal past the end", func(t *testing.T) {
		expectJSON(t, mustApply(t, parse(t, `{"values":[1,2]}`), ops(t, `[["p",["values"],0,1e9,[]]]`)), `{"values":[]}`)
	})
}

func TestDeltaOperationAssertions(t *testing.T) {
	t.Run("accepts decoded operations and rejects wire-only forms", func(t *testing.T) {
		for _, operation := range ops(t, `[["r",{"value":1}],["s",["value"],1],["d",["value"]],["a",["value"],"x"],["t",["value"],2],["p",["value"],0,0,[]],["m",["value"],[0]]]`) {
			expectNoErr(t, AssertValidOp(operation))
		}
		for _, wireOnly := range ops(t, `[["s",1],["d"],["a","x"],["t",2],["p",0,0,[]],["#",0,["value"]],["s",0,1]]`) {
			expectErr(t, AssertValidOp(wireOnly), "")
			expectNoErr(t, AssertValidWireOp(wireOnly))
		}
	})

	t.Run("does not recursively inspect operation payloads", func(t *testing.T) {
		expectNoErr(t, AssertValidOp(Op{"s", []any{"value"}, struct{}{}}))
		expectNoErr(t, AssertValidWireOp(WireOp{"r", struct{}{}}))
	})
}

func TestOverlapIsExactAtUnitBoundaries(t *testing.T) {
	// Oracle: the returned n always satisfies a[len-n:] == b[:n] in UTF-16 code units (index.ts overlap contract).
	if got := Overlap("a😀", "😀b", 65_536); got != 2 {
		t.Fatalf("overlap over a surrogate pair=%d", got)
	}
	if got := Overlap(strings.Repeat("x", 200), strings.Repeat("x", 200), 65_536); got == 0 {
		t.Fatal("repetitive overlap gave up on an exact full match")
	}
	if !reflect.DeepEqual(Overlap("", "x", 10), 0) {
		t.Fatal("empty overlap")
	}
}
