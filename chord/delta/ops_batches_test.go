package delta

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// upstream: chord/test/delta-apply-immutable.test.ts "protects root replacement payloads before later object and array edits".
// Pi source: packages/chord/src/delta/index.ts (applyImmutableBatches)
// mutation-checked: a nil root after the replacement fails it
func TestApplyImmutableBatchesReplaysRootReplacementThenEdits(t *testing.T) {
	replacement := JsonObjectOf("nested", JsonObjectOf("value", float64(1)), "values", []any{float64(1), float64(2), float64(3)})
	result, err := ApplyImmutableBatchesSeq(nil, slices.Values([][]Op{
		{{"r", replacement}},
		{{"s", []any{"nested", "value"}, float64(2)}},
		{
			{"p", []any{"values"}, float64(1), float64(1), []any{float64(4), float64(5)}},
			{"m", []any{"values"}, []any{float64(3), float64(0), float64(1), float64(2)}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := JsonObjectOf("nested", JsonObjectOf("value", float64(2)), "values", []any{float64(3), float64(1), float64(4), float64(5)})
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	if !reflect.DeepEqual(replacement, JsonObjectOf("nested", JsonObjectOf("value", float64(1)), "values", []any{float64(1), float64(2), float64(3)})) {
		t.Fatalf("the replacement payload was edited: %#v", replacement)
	}
}

// upstream: chord/test/delta-apply-immutable.test.ts "shares one private copy-on-write scope across batch partitions": the batch partition does not change the final value, and the base is not edited.
// Pi source: packages/chord/src/delta/index.ts (applyImmutableBatches)
// mutation-checked: replaying only the first batch fails it
func TestApplyImmutableBatchesMatchesOneBatchAndLeavesTheBaseAlone(t *testing.T) {
	base := func() *JsonObject {
		return JsonObjectOf(
			"text", "abcdef",
			"meta", JsonObjectOf("count", float64(0)),
			"values", []any{
				JsonObjectOf("id", float64(1), "value", float64(1)),
				JsonObjectOf("id", float64(2), "value", float64(2)),
				JsonObjectOf("id", float64(3), "value", float64(3)),
			},
		)
	}
	batches := [][]Op{
		{{"s", []any{"meta", "count"}, float64(1)}, {"p", []any{"values"}, float64(1), float64(1), []any{JsonObjectOf("id", float64(4), "value", float64(4))}}},
		{},
		{{"m", []any{"values"}, []any{float64(2), float64(0), float64(1)}}, {"s", []any{"values", float64(2), "value"}, float64(40)}},
		{{"t", []any{"text"}, float64(2)}, {"a", []any{"text"}, "!"}},
	}
	input := base()
	got, err := ApplyImmutableBatchesSeq(input, slices.Values(batches))
	if err != nil {
		t.Fatal(err)
	}
	var flat []Op
	for _, ops := range batches {
		flat = append(flat, ops...)
	}
	want, err := ApplyImmutable(base(), flat)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batched = %#v, single batch = %#v", got, want)
	}
	if !reflect.DeepEqual(input, base()) {
		t.Fatalf("the input revision was edited: %#v", input)
	}
}

// upstream: delta-apply-immutable.test.ts: an invalid operation or unsafe path in any batch throws and no value is returned.
// Pi source: packages/chord/src/delta/index.ts (applyImmutableBatches)
// mutation-checked: skipping AssertValidOpValue fails it
func TestApplyImmutableBatchesRejectsInvalidAndUnsafeOperations(t *testing.T) {
	if _, err := ApplyImmutableBatchesSeq(NewJsonObject(0), slices.Values([][]Op{{{"s", []any{"a"}, float64(1)}}, {{"x", []any{"a"}}}})); err == nil || err.Error() != "unknown op verb: x" {
		t.Fatalf("invalid verb error = %v, want TypeError unknown op verb: x", err)
	}
	var unsafe *UnsafePathError
	_, err := ApplyImmutableBatchesSeq(NewJsonObject(0), slices.Values([][]Op{{{"s", []any{"__proto__", "x"}, float64(1)}}}))
	if !errors.As(err, &unsafe) {
		t.Fatalf("unsafe path error = %v, want UnsafePathError", err)
	}
}

// upstream: chord/test/delta.test.ts "supports root replacement, root splice, and permutations": isBase marks a batch that begins with a replacement.
// Pi source: packages/chord/src/delta/index.ts (isBase, isReplace)
// mutation-checked: IsBase on the last op, IsReplace on the wrong tag each fail it
func TestIsBaseAndIsReplace(t *testing.T) {
	replace := Op{"r", []any{float64(1), float64(2), float64(3)}}
	for _, tc := range []struct {
		name string
		ops  []Op
		base bool
	}{
		{"leading replacement", []Op{replace}, true},
		{"replacement after an edit", []Op{{"a", []any{"t"}, "x"}, replace}, false},
		{"edit first", []Op{{"d", []any{"k"}}}, false},
		{"empty batch", nil, false},
	} {
		if got := IsBase(tc.ops); got != tc.base {
			t.Errorf("%s: IsBase = %v, want %v", tc.name, got, tc.base)
		}
	}
	if !IsReplace(replace) || IsReplace(Op{"s", []any{"k"}, nil}) || IsReplace(Op{}) {
		t.Fatal("IsReplace must be true only for the \"r\" verb")
	}
}
