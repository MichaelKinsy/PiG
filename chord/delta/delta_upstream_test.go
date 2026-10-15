package delta

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/test/delta.test.ts. The two cases that install an inherited JavaScript setter are equivalence ports: a Go map has no prototype chain, so they assert the invariant the setter guards (reserved and inherited names are stored as own data, byte-identical to Pi's output, with no side effect on other values) on the same inputs.

func TestDeltaCanonicalStrings(t *testing.T) {
	t.Run("emits append and rolling-window operations", func(t *testing.T) {
		// delta.test.ts:163-178
		tracker := Track(jsonObject(t, `{"text":"abcdefgh"}`))
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Set("text", change.State().Get("text").(string)+"ij"))
		prepared := mustPrepare(t, change)
		if got := opsText(t, prepared.Ops()); got != `[["a",["text"],"ij"]]` {
			t.Fatalf("ops %s", got)
		}
		mustNoErr(t, tracker.Adopt(prepared))

		change = tracker.BeginChange()
		mustNoErr(t, change.State().Set("text", change.State().Get("text").(string)[3:]+"xyz"))
		prepared = mustPrepare(t, change)
		if got := opsText(t, prepared.Ops()); got != `[["t",["text"],3],["a",["text"],"xyz"]]` {
			t.Fatalf("ops %s", got)
		}
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
		expectSame(t, obj(t, immutable).Value("stable"), obj(t, base).Value("stable"), "stable")
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
		_, err := Apply(chordjson.NewObject(0), ops(t, `[["s",["constructor","prototype","x"],true]]`))
		expectUnsafePath(t, err)
		_, err = Apply(parse(t, `{"values":[1]}`), ops(t, `[["s",["values",3],2]]`))
		expectUnsafePath(t, err)
		_, err = Apply(parse(t, `{"value":1}`), ops(t, `[["a",["value"],"x"]]`))
		expectErr(t, err, "")
		expectErr(t, AssertValidOpValue(Op{"s", "value", 1.0}), "")
		expectErr(t, AssertValidOpValue(Op{"m", []any{}, []any{0.0, 0.0}}), "bijection")
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
		expectNoErr(t, AssertValidOpValue(Op{"s", []any{"value"}, 1.0}))
		expectErr(t, AssertValidOpValue(Op{"s", 1.0}), "")
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
		expectJSON(t, encodeAll(t, enc, []Op{{"r", chordjson.ObjectOf("value", 3.0)}}), `[["r",{"value":3}]]`)
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
		decodeAll(t, dec, []WireOp{{"r", chordjson.ObjectOf("a", "")}})
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
		batch := []Op{{"r", chordjson.ObjectOf("value", 0.0)}}
		first, second := obj(t, mustApply(t, nil, batch)), obj(t, mustApply(t, nil, batch))
		first.Set("value", 1.0)
		if second.Value("value") != 1.0 {
			t.Fatalf("second=%v", second)
		}
	})
}

func TestDeltaPathAndPrototypeSafety(t *testing.T) {
	t.Run("rejects constructor walks and forbidden interned paths", func(t *testing.T) {
		_, err := Apply(chordjson.NewObject(0), ops(t, `[["s",["constructor","prototype","gadget"],true]]`))
		expectErr(t, err, "")
		_, err = NewDecoder().Decode([]WireOp{{"#", 0.0, []any{"__proto__", "w"}}, {"s", 0.0, true}})
		expectErr(t, err, "")
	})

	t.Run("does not run inherited setters", func(t *testing.T) {
		// Equivalence port of delta.test.ts:374. Pi 1.0.0 apply({}, [["s",[name],1]]) under Node 24 gives {"trap":1} and
		// {"toString":1}, and throws UnsafePathError naming the segment for __proto__, constructor and prototype.
		for _, applier := range []func(JsonValue, []Op) (JsonValue, error){Apply, ApplyImmutable} {
			for _, name := range []string{"trap", "toString"} {
				out, err := applier(chordjson.NewObject(0), []Op{{"s", []any{name}, 1.0}})
				expectNoErr(t, err)
				if got, want := jsonText(out), `{"`+name+`":1}`; got != want {
					t.Fatalf("set %s: %s, want Pi's %s", name, got, want)
				}
			}
			for _, name := range []string{"__proto__", "constructor", "prototype"} {
				target := chordjson.NewObject(0)
				_, err := applier(target, []Op{{"s", []any{name}, 1.0}})
				var unsafe *UnsafePathError
				if !errors.As(err, &unsafe) || unsafe.Segment != name || err.Error() != "unsafe path segment: "+name {
					t.Fatalf("set %s: %v, want Pi's UnsafePathError", name, err)
				}
				if target.Len() != 0 {
					t.Fatalf("a rejected set changed its target: %v", target)
				}
			}
		}
	})

	t.Run("imports own properties without invoking inherited setters", func(t *testing.T) {
		// Equivalence port of delta.test.ts:396. Expected text is Pi 1.0.0 track output under Node 24 for the same input. The tracker edits of that case, Pi's exact batches [["s",["trap"],6]] and the root replacement for an edit below __proto__, are asserted on the same input by TestTrackerStoresReservedAndInheritedNamesAsOwnData.
		const imported = `{"__proto__":{"z":1},"constructor":2,"prototype":3,"toString":4,"trap":5}`
		root := jsonObject(t, imported)
		tracker := Track(root)
		expectSameContainer(t, tracker.Value(), root, "tracker.value")
		if got := jsonText(tracker.Value()); got != imported {
			t.Fatalf("imported %s, want %s", got, imported)
		}
		if got := jsonText(Track(jsonObject(t, `{"values":[1,2]}`)).Value()); got != `{"values":[1,2]}` {
			t.Fatalf("array import %s", got)
		}
		if got := jsonText(mustApply(t, chordjson.NewObject(0), []Op{{"s", []any{"value"}, parse(t, imported)}})); got != `{"value":`+imported+`}` {
			t.Fatalf("value import %s", got)
		}
	})

	t.Run("allows reserved names inside values without prototype pollution", func(t *testing.T) {
		value := parse(t, `{"__proto__":{"z":1}}`)
		out := mustApply(t, chordjson.NewObject(0), []Op{{"s", []any{"value"}, value}})
		if _, ok := obj(t, obj(t, out).Value("value")).Get("__proto__"); !ok {
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
		if got := len(arr(t, result.Value("values"))); got != len(items) {
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
			expectNoErr(t, AssertValidOpValue(operation))
		}
		for _, wireOnly := range ops(t, `[["s",1],["d"],["a","x"],["t",2],["p",0,0,[]],["#",0,["value"]],["s",0,1]]`) {
			expectErr(t, AssertValidOpValue(wireOnly), "")
			expectNoErr(t, AssertValidWireOp(wireOnly))
		}
	})

	t.Run("does not recursively inspect operation payloads", func(t *testing.T) {
		expectNoErr(t, AssertValidOpValue(Op{"s", []any{"value"}, struct{}{}}))
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

// upstream: delta/index.ts:87 (overlap(a, b, scan, probe = 64, maxCandidates = 8)): the defaults are the documented limits and a caller's limits replace them.
func TestOverlapProbeAndCandidateLimits(t *testing.T) {
	a, b := "xxabcabcab", "abcabcabyz"
	if got := Overlap(a, b, 65_536); got != 8 {
		t.Fatalf("default limits: %d, want 8", got)
	}
	if got := Overlap(a, b, 65_536, 64, 8); got != 8 {
		t.Fatalf("explicit defaults: %d, want 8", got)
	}
	// With no candidate budget the first head match is already over budget, so the scan gives up and reports no overlap.
	if got := Overlap(a, b, 65_536, 64, 0); got != 0 {
		t.Fatalf("zero candidates: %d, want 0 (gives up; a set is emitted, never a wrong overlap)", got)
	}
	// A one-unit head finds any overlap but meets more false starts: with 20 leading "a" the true overlap "ab" is the 21st candidate.
	run, next := strings.Repeat("a", 20)+"b", "ab"
	if got := Overlap(run, next, 65_536, 1, 8); got != 0 {
		t.Fatalf("a budget of 8 gives up before the 21st candidate: got %d", got)
	}
	if got := Overlap(run, next, 65_536, 1, 100); got != 2 {
		t.Fatalf("with a larger budget the overlap is found: got %d, want 2", got)
	}
	// An empty head (probe 0) is String.indexOf(""): it matches at every position, then the 1-unit head follows.
	if got := Overlap(a, b, 65_536, 0, 100); got != 8 {
		t.Fatalf("probe 0: %d, want 8", got)
	}
}

// upstream: delta/index.ts:87-110 overlap: only the last `scan` units of a are searched (index.ts:89), and the candidate budget `++tried > maxCandidates`
// (index.ts:102) lets a head that matches exactly maxCandidates times still succeed on its last candidate.
// mutation-checked: an Overlap that searches all of a, or gives up at the budget instead of after it, fails it.
func TestOverlapScansOnlyTheTailAndSpendsItsWholeCandidateBudget(t *testing.T) {
	if got := Overlap("abcdef", "cdefxx", 65_536); got != 4 {
		t.Fatalf("unbounded scan = %d, want 4", got)
	}
	if got := Overlap("abcdef", "cdefxx", 3); got != 0 {
		t.Fatalf("scan 3 reaches only %q, which holds no overlap with cdefxx: got %d, want 0", "def", got)
	}
	if got := Overlap("aab", "ab", 65_536, 1, 2); got != 2 {
		t.Fatalf("the overlap sits on the second of two allowed candidates: got %d, want 2", got)
	}
	if got := Overlap("aab", "ab", 65_536, 1, 1); got != 0 {
		t.Fatalf("a budget of one gives up before the second candidate: got %d, want 0", got)
	}
}
