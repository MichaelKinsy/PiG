package delta

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Ports packages/chord/test/delta-diff.test.ts and packages/chord/test/state-diff.test.ts. The two upstream files share their first twenty cases; each differs in one tail case. Object keys visit in ascending order (Go maps have no insertion order), so the one case whose expectation depends on JavaScript insertion order lists its operations in that order.

func expectDiff(t *testing.T, before, after any, expected string) {
	t.Helper()
	operations := DiffRevisions(before, after)
	expectJSON(t, operations, expected)
	expectEqual(t, mustApplyImmutable(t, before, operations), after)
}

func rows(count int, build func(at int) any) []any {
	out := make([]any, count)
	for at := range out {
		out[at] = build(at)
	}
	return out
}

func idObject(id string) map[string]any { return map[string]any{"id": id} }

func wrap(key string, value any) map[string]any { return map[string]any{key: value} }

func encodedLen(t *testing.T, value any) int {
	t.Helper()
	encoded, err := json.Marshal(value)
	expectNoErr(t, err)
	return len(encoded)
}

func runSharedDiffCases(t *testing.T) {
	t.Run("emits sets and deletes", func(t *testing.T) {
		// Insertion order is change, add, remove; ascending key order is add, change, remove.
		expectDiff(t, parse(t, `{"keep":1,"change":1,"remove":true}`), parse(t, `{"keep":1,"change":2,"add":3}`),
			`[["s",["add"],3],["s",["change"],2],["d",["remove"]]]`)
	})

	t.Run("emits string append and front truncation", func(t *testing.T) {
		expectDiff(t, parse(t, `{"text":"hello"}`), parse(t, `{"text":"hello world"}`), `[["a",["text"]," world"]]`)
		expectDiff(t, parse(t, `{"text":"hello world"}`), parse(t, `{"text":"world"}`), `[["t",["text"],6]]`)
		expectDiff(t, parse(t, `{"text":"abcdefgh"}`), parse(t, `{"text":"defghxyz"}`), `[["t",["text"],3],["a",["text"],"xyz"]]`)
	})

	t.Run("represents array insertion, removal, and shift with splices", func(t *testing.T) {
		a, b, c := idObject("a"), idObject("b"), idObject("c")
		expectDiff(t, wrap("values", []any{a, b}), wrap("values", []any{a, c, b}), `[["p",["values"],1,0,[{"id":"c"}]]]`)
		expectDiff(t, wrap("values", []any{a, b, c}), wrap("values", []any{b, c}), `[["p",["values"],0,1,[]]]`)
	})

	t.Run("collapses a same-length queue update to two splices", func(t *testing.T) {
		a, b, c, d := idObject("a"), idObject("b"), idObject("c"), idObject("d")
		expectDiff(t, wrap("values", []any{a, b, c}), wrap("values", []any{b, c, d}),
			`[["p",["values"],0,1,[]],["p",["values"],2,0,[{"id":"d"}]]]`)
	})

	t.Run("emits a permutation for a pure reorder", func(t *testing.T) {
		a, b, c := idObject("a"), idObject("b"), idObject("c")
		expectDiff(t, wrap("values", []any{a, b, c}), wrap("values", []any{c, a, b}), `[["m",["values"],[2,0,1]]]`)
	})

	t.Run("normalizes reordered distinct deeply-equal objects to a no-op", func(t *testing.T) {
		first, second := parse(t, `{"nested":{"value":1}}`), parse(t, `{"nested":{"value":1}}`)
		expectNotSame(t, first, second, "distinct objects")
		if operations := DiffRevisions(wrap("values", []any{first, second}), wrap("values", []any{second, first})); len(operations) != 0 {
			t.Fatalf("ops=%v", operations)
		}
	})

	t.Run("validates and encodes permutations", func(t *testing.T) {
		operations := ops(t, `[["m",["values"],[2,0,1]],["m",["values"],[1,2,0]]]`)
		for _, operation := range operations {
			expectNoErr(t, AssertValidOp(operation))
		}
		wire, err := NewEncoder().Encode(operations)
		expectNoErr(t, err)
		expectJSON(t, wire, `[["m",["values"],[2,0,1]],["m",[1,2,0]]]`)
		for _, operation := range wire {
			expectNoErr(t, AssertValidWireOp(operation))
		}
		decoded, err := NewDecoder().Decode(wire)
		expectNoErr(t, err)
		expectEqual(t, decoded, operations)
		expectErr(t, AssertValidOp(ops(t, `[["m",["values"],[0,0]]]`)[0]), "bijection")
	})

	t.Run("emits nothing for deeply equal reconstructed values", func(t *testing.T) {
		for _, pair := range [][2]string{
			{`{"value":{"nested":[1,2]}}`, `{"value":{"nested":[1,2]}}`},
			{`{"values":[{"id":1},{"id":2}]}`, `{"values":[{"id":1},{"id":2}]}`},
			{`{"values":[true,true,true]}`, `{"values":[true,true,true]}`},
		} {
			if operations := DiffRevisions(parse(t, pair[0]), parse(t, pair[1])); len(operations) != 0 {
				t.Fatalf("ops=%v for %s", operations, pair[0])
			}
		}
	})

	t.Run("keeps a leaf edit inside a reconstructed array narrow", func(t *testing.T) {
		expectDiff(t,
			parse(t, `{"values":[{"id":1,"label":"one"},{"id":2,"label":"two"}]}`),
			parse(t, `{"values":[{"id":1,"label":"one"},{"id":2,"label":"changed"}]}`),
			`[["s",["values",1,"label"],"changed"]]`)
	})

	t.Run("emits payload-free splices for scattered removals", func(t *testing.T) {
		a, b, c, d, e := idObject("a"), idObject("b"), idObject("c"), idObject("d"), idObject("e")
		expectDiff(t, wrap("values", []any{a, b, c, d, e}), wrap("values", []any{a, c, e}), `[["p",["values"],1,1,[]],["p",["values"],2,1,[]]]`)
	})

	for _, tc := range []struct {
		name          string
		before, after []any
		expected      string
	}{
		{"front", []any{1.0, 2.0, 3.0, 4.0}, []any{2.0, 3.0, 4.0}, `[["p",["values"],0,1,[]]]`},
		{"tail", []any{1.0, 2.0, 3.0, 4.0}, []any{1.0, 2.0, 3.0}, `[["p",["values"],3,1,[]]]`},
		{"middle", []any{1.0, 2.0, 3.0, 4.0}, []any{1.0, 3.0, 4.0}, `[["p",["values"],1,1,[]]]`},
		{"all", []any{1.0, 2.0, 3.0, 4.0}, []any{}, `[["p",["values"],0,4,[]]]`},
		{"none", []any{1.0, 2.0, 3.0, 4.0}, []any{1.0, 2.0, 3.0, 4.0}, `[]`},
	} {
		t.Run(fmt.Sprintf("encodes %s removal canonically", tc.name), func(t *testing.T) {
			expectDiff(t, wrap("values", append([]any{}, tc.before...)), wrap("values", append([]any{}, tc.after...)), tc.expected)
		})
	}

	t.Run("does not field-diff unrelated shifted objects with common fields", func(t *testing.T) {
		row := func(value float64) any { return map[string]any{"type": "row", "value": value} }
		before := []any{row(1), row(2), row(3)}
		after := []any{row(2), row(3), row(4)}
		expectDiff(t, wrap("values", before), wrap("values", after), `[["p",["values"],0,1,[]],["p",["values"],2,0,[{"type":"row","value":4}]]]`)
	})

	t.Run("does not treat coincidental id or key fields as structural identity", func(t *testing.T) {
		left, right := map[string]any{"value": "left"}, map[string]any{"value": "right"}
		before := []any{left, parse(t, `{"id":1,"key":"a","value":"first"}`), parse(t, `{"id":2,"key":"b","value":"second"}`), right}
		replacements := []any{parse(t, `{"id":2,"key":"b","value":"edited-second"}`), parse(t, `{"id":1,"key":"a","value":"edited-first"}`)}
		after := append(append([]any{left}, replacements...), right)
		expectDiff(t, wrap("values", before), wrap("values", after),
			`[["p",["values"],1,2,[{"id":2,"key":"b","value":"edited-second"},{"id":1,"key":"a","value":"edited-first"}]]]`)
	})

	t.Run("combines removals, append, and a shared-subtree survivor edit", func(t *testing.T) {
		item := func(id string) map[string]any {
			return map[string]any{"id": id, "stable": map[string]any{}, "detail": map[string]any{"text": id}}
		}
		a, b, c, d := item("a"), item("b"), item("c"), item("d")
		changedC := map[string]any{"id": "c", "stable": c["stable"], "detail": map[string]any{"text": "changed"}}
		appended := item("e")
		expectDiff(t, wrap("values", []any{a, b, c, d}), wrap("values", []any{a, changedC, d, appended}),
			`[["p",["values"],1,1,[]],["a",["values",1,"detail","text"],"hanged"],["p",["values"],3,0,[{"id":"e","stable":{},"detail":{"text":"e"}}]]]`)
	})

	for _, size := range []int{256 * 1024, 1024 * 1024} {
		t.Run(fmt.Sprintf("does not retain a %d-byte removed-neighbor payload", size), func(t *testing.T) {
			payload := strings.Repeat("x", size)
			retained := rows(5, func(at int) any { return map[string]any{"id": string(rune('a' + at)), "payload": payload} })
			before := wrap("values", append([]any(nil), retained...))
			after := wrap("values", []any{retained[0], retained[2], retained[4]})
			operations := DiffRevisions(before, after)
			expectJSON(t, operations, `[["p",["values"],1,1,[]],["p",["values"],2,1,[]]]`)
			if got := encodedLen(t, operations); got >= 100 {
				t.Fatalf("encoded=%d", got)
			}
			expectEqual(t, mustApplyImmutable(t, before, operations), after)
			if got := len(arr(t, obj(t, before)["values"])); got != 5 {
				t.Fatalf("before mutated: %d", got)
			}
		})
	}

	for _, size := range []int{1_001, 10_000} {
		t.Run(fmt.Sprintf("keeps push, pop, and middle removal narrow at %d items", size), func(t *testing.T) {
			values := rows(size, func(at int) any { return map[string]any{"value": float64(at)} })
			appended := map[string]any{"value": float64(size)}
			expectDiff(t, wrap("values", values), wrap("values", append(append([]any(nil), values...), appended)),
				fmt.Sprintf(`[["p",["values"],%d,0,[{"value":%d}]]]`, size, size))
			expectDiff(t, wrap("values", values), wrap("values", append([]any(nil), values[:size-1]...)),
				fmt.Sprintf(`[["p",["values"],%d,1,[]]]`, size-1))
			middle := size / 2
			without := append(append([]any(nil), values[:middle]...), values[middle+1:]...)
			expectDiff(t, wrap("values", values), wrap("values", without), fmt.Sprintf(`[["p",["values"],%d,1,[]]]`, middle))
		})
	}

	t.Run("keeps forty-thousand-row sparse edits narrow", func(t *testing.T) {
		values := rows(40_000, func(at int) any {
			return map[string]any{"value": float64(at), "stable": map[string]any{"value": float64(at)}}
		})
		after := append([]any(nil), values...)
		var expected []any
		for at := 100; at < len(after); at += 400 {
			after[at] = map[string]any{"value": float64(-at), "stable": obj(t, values[at])["stable"]}
			expected = append(expected, []any{"s", []any{"values", at, "value"}, float64(-at)})
		}
		operations := DiffRevisions(wrap("values", values), wrap("values", after))
		expectEqual(t, operations, expected)
		if got := encodedLen(t, operations); got >= 7_500 {
			t.Fatalf("encoded=%d", got)
		}
		expectEqual(t, mustApplyImmutable(t, wrap("values", values), operations), wrap("values", after))
	})

	t.Run("keeps a reconstructed large-array leaf edit narrow", func(t *testing.T) {
		build := func() []any {
			return rows(1_000, func(at int) any { return map[string]any{"value": float64(at), "label": fmt.Sprintf("row-%d", at)} })
		}
		before, after := build(), build()
		obj(t, after[700])["label"] = "changed"
		operations := DiffRevisions(wrap("values", before), wrap("values", after))
		expectJSON(t, operations, `[["s",["values",700,"label"],"changed"]]`)
		if got := encodedLen(t, operations); got >= 100 {
			t.Fatalf("encoded=%d", got)
		}
		expectEqual(t, mustApplyImmutable(t, wrap("values", before), operations), wrap("values", after))
	})

	t.Run("splices an ambiguous equal-count moved-and-edited gap", func(t *testing.T) {
		left, right := map[string]any{"value": "left"}, map[string]any{"value": "right"}
		before := []any{left, parse(t, `{"id":1,"value":"a"}`), parse(t, `{"id":2,"value":"b"}`), right}
		replacements := []any{parse(t, `{"id":2,"value":"edited"}`), parse(t, `{"id":1,"value":"also-edited"}`)}
		after := append(append([]any{left}, replacements...), right)
		expectDiff(t, wrap("values", before), wrap("values", after),
			`[["p",["values"],1,2,[{"id":2,"value":"edited"},{"id":1,"value":"also-edited"}]]]`)
	})

	t.Run("encodes five hundred unshifts without snapshotting retained rows", func(t *testing.T) {
		retained := rows(10_000, func(at int) any { return map[string]any{"value": float64(at), "payload": strings.Repeat("x", 100)} })
		inserted := rows(500, func(at int) any { return map[string]any{"value": float64(-at - 1)} })
		operations := DiffRevisions(wrap("values", retained), wrap("values", append(append([]any(nil), inserted...), retained...)))
		expectEqual(t, operations, []any{[]any{"p", []any{"values"}, 0, 0, inserted}})
		if got := encodedLen(t, operations); got >= 20_000 {
			t.Fatalf("encoded=%d", got)
		}
		expectEqual(t, mustApplyImmutable(t, wrap("values", retained), operations), wrap("values", append(append([]any(nil), inserted...), retained...)))
	})

	t.Run("bounds wide-object operation emission with a root replacement", func(t *testing.T) {
		before, after := map[string]any{}, map[string]any{}
		for at := range 20_000 {
			key := fmt.Sprintf("field%d", at)
			before[key], after[key] = 0.0, 1.0
		}
		operations := DiffRevisions(before, after)
		if len(operations) != 1 || operations[0].Verb() != "r" {
			t.Fatalf("ops=%d", len(operations))
		}
		expectSame(t, operations[0][1], after, "payload")
	})
}

func TestDeltaDiff(t *testing.T) {
	runSharedDiffCases(t)

	t.Run("keeps a large rotation payload-free", func(t *testing.T) {
		values := rows(10_000, func(at int) any { return map[string]any{"value": float64(at)} })
		rotated := append(append([]any(nil), values[1_000:]...), values[:1_000]...)
		operations := DiffRevisions(wrap("values", values), wrap("values", rotated))
		if len(operations) != 1 || operations[0].Verb() != "m" {
			t.Fatalf("ops=%d first=%v", len(operations), operations)
		}
		if got := encodedLen(t, operations); got >= 60_000 {
			t.Fatalf("encoded=%d", got)
		}
		expectEqual(t, mustApplyImmutable(t, wrap("values", values), operations), wrap("values", rotated))
	})

	t.Run("bounds a wide normalized array fallback with a root replacement", func(t *testing.T) {
		before := wrap("values", rows(40_000, func(int) any { return 0.0 }))
		after := wrap("values", rows(40_000, func(int) any { return 1.0 }))
		operations := DiffRevisions(before, after)
		if len(operations) != 1 || operations[0].Verb() != "r" {
			t.Fatalf("ops=%d", len(operations))
		}
		expectSame(t, operations[0][1], after, "payload")
		expectEqual(t, mustApplyImmutable(t, before, operations), after)
	})
}

func TestStateDiff(t *testing.T) {
	runSharedDiffCases(t)

	t.Run("falls back to a base operation when leaf operations are larger", func(t *testing.T) {
		before := wrap("values", rows(40_000, func(int) any { return 0.0 }))
		after := wrap("values", rows(40_000, func(int) any { return 1.0 }))
		operations := DiffRevisions(before, after)
		if len(operations) != 1 || operations[0].Verb() != "r" {
			t.Fatalf("ops=%d", len(operations))
		}
		expectSame(t, operations[0][1], after, "payload")
	})
}
