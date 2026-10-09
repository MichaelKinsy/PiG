package delta

import (
	"fmt"
	"iter"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/test/delta-apply-immutable.test.ts. JavaScript freezes its fixtures to prove they are not written; Go maps cannot be frozen, so each case compares the inputs with a deep snapshot taken before the call.

func TestCheckedImmutableOperationApplication(t *testing.T) {
	t.Run("copies each touched container once while preserving input and payload ownership", func(t *testing.T) {
		shared := parse(t, `{"nested":{"value":1}}`)
		untouched := parse(t, `{"value":9}`)
		rowPayload := parse(t, `{"id":4,"label":"placed"}`)
		base := parse(t, `{"text":"abcdef","stable":{"value":7},"branch":{"value":1},"copy":null,"placed":null,"untouched":null,"left":null,"right":null,"meta":{"count":0,"obsolete":true},"rows":[{"id":1,"label":"one"},{"id":2,"label":"two"},{"id":3,"label":"three"}]}`)
		baseSnapshot := clone(t, base)
		branch := obj(t, base).Value("branch")
		operations := []Op{
			{"t", []any{"text"}, 2}, {"a", []any{"text"}, "!"},
			{"s", []any{"meta", "count"}, 1}, {"s", []any{"meta", "count"}, 2},
			{"d", []any{"meta", "obsolete"}},
			{"s", []any{"copy"}, branch}, {"s", []any{"copy", "value"}, 2},
			{"s", []any{"placed"}, shared}, {"s", []any{"placed", "nested", "value"}, 2},
			{"s", []any{"untouched"}, untouched},
			{"s", []any{"left"}, shared}, {"s", []any{"right"}, shared}, {"s", []any{"left", "nested", "value"}, 3},
			{"p", []any{"rows"}, 1, 1, []any{rowPayload}},
			{"s", []any{"rows", 1, "label"}, "edited"},
			{"m", []any{"rows"}, []any{1, 0, 2}},
			{"s", []any{"rows", 0, "label"}, "moved"},
		}

		result := obj(t, mustApplyImmutable(t, base, operations))
		mutableResult := mustApply(t, clone(t, base), ops(t, mustJSON(t, operations)))

		expectEqual(t, result, mutableResult)
		if result.Value("text") != "cdef!" {
			t.Fatalf("text=%v", result.Value("text"))
		}
		expectSame(t, result.Value("stable"), obj(t, base).Value("stable"), "stable")
		expectSame(t, result.Value("untouched"), untouched, "untouched")
		expectNotSame(t, result.Value("copy"), branch, "copy")
		expectJSON(t, result.Value("copy"), `{"value":2}`)
		expectNotSame(t, result.Value("placed"), shared, "placed")
		expectJSON(t, result.Value("placed"), `{"nested":{"value":2}}`)
		expectNotSame(t, result.Value("left"), shared, "left")
		expectSame(t, result.Value("right"), shared, "right")
		resultRows := arr(t, result.Value("rows"))
		expectNotSame(t, resultRows[0], rowPayload, "rows[0]")
		expectJSON(t, resultRows[0], `{"id":4,"label":"moved"}`)
		expectEqual(t, base, baseSnapshot)
		expectJSON(t, shared, `{"nested":{"value":1}}`)
		expectJSON(t, rowPayload, `{"id":4,"label":"placed"}`)
	})

	t.Run("protects root replacement payloads before later object and array edits", func(t *testing.T) {
		replacement := parse(t, `{"nested":{"value":1},"values":[1,2,3]}`)
		result, err := ApplyImmutableBatchesSeq(nil, slices.Values([][]Op{
			{{"r", replacement}},
			ops(t, `[["s",["nested","value"],2]]`),
			ops(t, `[["p",["values"],1,1,[4,5]],["m",["values"],[3,0,1,2]]]`),
		}))
		expectNoErr(t, err)
		expectJSON(t, result, `{"nested":{"value":2},"values":[3,1,4,5]}`)
		expectJSON(t, replacement, `{"nested":{"value":1},"values":[1,2,3]}`)

		array := parse(t, `[1,2,3]`)
		arrayResult := mustApplyImmutable(t, array, ops(t, `[["p",[],1,1,[4,5]],["m",[],[3,0,1,2]],["d",[1]]]`))
		expectJSON(t, arrayResult, `[3,4,5]`)
		expectJSON(t, array, `[1,2,3]`)
	})

	t.Run("shares one private copy-on-write scope across batch partitions", func(t *testing.T) {
		base := parse(t, `{"text":"abcdef","meta":{"count":0},"values":[{"id":1,"value":1},{"id":2,"value":2},{"id":3,"value":3}]}`)
		baseSnapshot := clone(t, base)
		batches := [][]Op{
			ops(t, `[["s",["meta","count"],1],["p",["values"],1,1,[{"id":4,"value":4}]]]`),
			{},
			ops(t, `[["m",["values"],[2,0,1]],["s",["values",2,"value"],40]]`),
			ops(t, `[["t",["text"],2],["a",["text"],"!"]]`),
		}
		intermediate := mustApplyImmutable(t, base, batches[0])
		intermediateSnapshot := clone(t, intermediate)
		sequential := intermediate
		for _, batch := range batches[1:] {
			sequential = mustApplyImmutable(t, sequential, batch)
		}
		streamed, err := ApplyImmutableBatchesSeq(base, slices.Values(batches))
		expectNoErr(t, err)
		flattened := mustApplyImmutable(t, base, slices.Concat(batches...))

		expectEqual(t, streamed, sequential)
		expectEqual(t, streamed, flattened)
		expectEqual(t, intermediate, intermediateSnapshot)
		expectEqual(t, base, baseSnapshot)
	})

	t.Run("handles tracker-produced batches across arbitrary revision boundaries", func(t *testing.T) {
		initial := chordjson.ObjectOf("text", "start", "values", rows(8, func(at int) any { return chordjson.ObjectOf("id", float64(at), "score", 0.0) }), "revision", 0.0)
		tracker := trackCopy(initial)
		var batches [][]Op
		for revision := 1; revision <= 40; revision++ {
			change := tracker.BeginChange()
			state := obj(t, mustState(t, change))
			state.Set("revision", float64(revision))
			state.Set("text", state.Value("text").(string)[1:]+fmt.Sprint(revision))
			values := arr(t, state.Value("values"))
			switch revision % 5 {
			case 0:
				slices.Reverse(values)
			case 1:
				state.Set("values", append(values, chordjson.ObjectOf("id", float64(100+revision), "score", float64(revision))))
			case 2:
				state.Set("values", values[1:])
			case 3:
				obj(t, values[revision%len(values)]).Set("score", float64(revision))
			default:
				values[1] = chordjson.ObjectOf("id", float64(200+revision), "score", float64(revision))
			}
			prepared := mustPrepareCopy(t, change)
			batches = append(batches, prepared.Ops)
			expectNoErr(t, tracker.Adopt(prepared))
		}
		result, err := ApplyImmutableBatchesSeq(initial, slices.Values(batches))
		expectNoErr(t, err)
		expectEqual(t, result, tracker.Value())
	})

	t.Run("does not expose partial application when validation or iteration fails", func(t *testing.T) {
		base := parse(t, `{"nested":{"value":1}}`)
		_, err := ApplyImmutableBatchesSeq(base, slices.Values([][]Op{
			ops(t, `[["s",["nested","value"],2]]`),
			ops(t, `[["s",["constructor","prototype","polluted"],true]]`),
		}))
		expectErr(t, err, "")
		expectJSON(t, base, `{"nested":{"value":1}}`)

		throwing := func(yield func([]Op) bool) {
			if !yield(ops(t, `[["s",["nested","value"],3]]`)) {
				return
			}
			panic("revision stream failed")
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != "revision stream failed" {
					t.Fatalf("recovered=%v", recovered)
				}
			}()
			_, _ = ApplyImmutableBatchesSeq(base, iter.Seq[[]Op](throwing))
		}()
		expectJSON(t, base, `{"nested":{"value":1}}`)

		advancedPastInvalid, iteratorClosed := false, false
		invalid := func(yield func([]Op) bool) {
			defer func() { iteratorClosed = true }()
			if !yield(ops(t, `[["s",["nested","value"],4]]`)) {
				return
			}
			if !yield(ops(t, `[["s",["__proto__","polluted"],true]]`)) {
				return
			}
			advancedPastInvalid = true
		}
		_, err = ApplyImmutableBatchesSeq(base, iter.Seq[[]Op](invalid))
		expectUnsafePath(t, err)
		if advancedPastInvalid || !iteratorClosed {
			t.Fatalf("advanced=%v closed=%v", advancedPastInvalid, iteratorClosed)
		}
		expectJSON(t, base, `{"nested":{"value":1}}`)

		_, err = ApplyImmutableBatchesSeq(base, slices.Values([][]Op{{{"s", "bad-path", 1.0}}}))
		expectErr(t, err, "path")

		_, err = ApplyImmutable(parse(t, `{"values":[]}`), ops(t, `[["s",["values","missing","value"],1]]`))
		expectPathError(t, err)
		_, err = ApplyImmutable(parse(t, `{"values":[{}]}`), ops(t, `[["s",["values","0","value"],1]]`))
		expectUnsafePath(t, err)
	})

	t.Run("allows one immutable batch to fan out without mutating shared payloads", func(t *testing.T) {
		payload := parse(t, `{"nested":{"value":1}}`)
		operations := []Op{{"s", []any{"placed"}, payload}, {"s", []any{"placed", "nested", "value"}, 2}}
		base := parse(t, `{"placed":null}`)
		first, second := mustApplyImmutable(t, base, operations), mustApplyImmutable(t, base, operations)
		expectEqual(t, first, second)
		expectNotSame(t, first, second, "results")
		expectNotSame(t, obj(t, first).Value("placed"), obj(t, second).Value("placed"), "placed")
		expectJSON(t, payload, `{"nested":{"value":1}}`)
	})
}

func TestImmutableApplicationComplexity(t *testing.T) {
	t.Run("copies a wide object once rather than once per repeated write", func(t *testing.T) {
		// delta-apply-immutable.test.ts:259-271 counts Object.keys calls; Go has no such hook, so the guard is the allocation: one copy of a 20,000-field map costs about a megabyte, a copy per write would cost a thousand times that.
		const width = 20_000
		base := chordjson.NewObject(width)
		for at := range width {
			base.Set(fmt.Sprintf("field%d", at), float64(at))
		}
		operations := make([]Op, 1_000)
		for at := range operations {
			operations[at] = Op{"s", []any{fmt.Sprintf("field%d", at)}, float64(-at)}
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		result := obj(t, mustApplyImmutable(t, base, operations))
		runtime.ReadMemStats(&after)
		if result.Value("field999") != -999.0 || base.Value("field999") != 999.0 {
			t.Fatalf("result=%v base=%v", result.Value("field999"), base.Value("field999"))
		}
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
			t.Fatalf("allocated %d bytes: containers were copied more than once", allocated)
		}
	})
}

func mustJSON(t testing.TB, value any) string {
	t.Helper()
	return strings.TrimSpace(fmt.Sprint(jsonText(value)))
}
