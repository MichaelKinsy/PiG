package typebox

import (
	"strings"
	"testing"
)

// BenchmarkLeafKeywords is Check and Errors of the leaf keywords (keywords.go) on a passing and on a failing value: the string lengths, the numeric bounds, const and enum, the item
// and property counts, and uniqueItems with scalar, object and long items.
func BenchmarkLeafKeywords(b *testing.B) {
	long := strings.Repeat("abcdefghij", 20)
	wide := strings.Repeat("日本語テキスト", 10)
	objects := func(count int) []any {
		items := make([]any, count)
		for index := range items {
			items[index] = map[string]any{"id": float64(index), "name": "item", "kind": "k", "tags": []any{"a", "b"}, "ok": true, "z": nil, "é": "ü"}
		}
		return items
	}
	numbers := make([]any, 64)
	words := make([]any, 64)
	for index := range numbers {
		numbers[index] = float64(index)
		words[index] = strings.Repeat("w", index+1)
	}
	cases := []struct {
		name          string
		schema, value any
	}{
		{"minLength/ascii", map[string]any{"type": "string", "minLength": 3.0}, "abcdef"},
		{"maxLength/ascii", map[string]any{"type": "string", "maxLength": 8.0}, "abcdef"},
		{"maxLength/ascii-long-fail", map[string]any{"type": "string", "maxLength": 50.0}, long},
		{"minLength/ascii-long", map[string]any{"type": "string", "minLength": 50.0}, long},
		{"maxLength/wide", map[string]any{"type": "string", "maxLength": 100.0}, wide},
		{"minLength/wide", map[string]any{"type": "string", "minLength": 10.0}, wide},
		{"pattern", map[string]any{"type": "string", "pattern": "^a+b"}, "aaab"},
		{"number-bounds", map[string]any{"type": "number", "minimum": 0.0, "maximum": 100.0, "multipleOf": 0.5}, 42.5},
		{"enum-scalar", map[string]any{"enum": []any{"a", "b", "c", "d", float64(5)}}, "d"},
		{"enum-object", map[string]any{"enum": []any{map[string]any{"a": 1.0, "b": []any{1.0, 2.0}}, map[string]any{"a": 2.0}}}, map[string]any{"a": 2.0}},
		{"const-scalar", map[string]any{"const": "x"}, "x"},
		{"counts", map[string]any{"type": "array", "minItems": 1.0, "maxItems": 100.0}, numbers},
		{"properties", map[string]any{"type": "object", "minProperties": 1.0, "maxProperties": 9.0}, map[string]any{"a": 1.0, "b": 2.0}},
		{"unique/numbers", map[string]any{"type": "array", "uniqueItems": true}, numbers},
		{"unique/strings", map[string]any{"type": "array", "uniqueItems": true}, words},
		{"unique/objects-8", map[string]any{"type": "array", "uniqueItems": true}, objects(8)},
		{"unique/objects-64", map[string]any{"type": "array", "uniqueItems": true}, objects(64)},
		{"unique/duplicate-fail", map[string]any{"type": "array", "uniqueItems": true}, append(objects(8), objects(1)[0])},
	}
	for _, tc := range cases {
		b.Run("Check/"+tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Check(tc.schema, tc.value)
			}
		})
		b.Run("Errors/"+tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Errors(tc.schema, tc.value)
			}
		})
	}
}
