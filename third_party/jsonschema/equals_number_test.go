package jsonschema

import (
	"encoding/json"
	"testing"
)

// JSON Schema equality never equates a number with a string, in either argument order.
func TestEqualsDoesNotEquateANumberWithAString(t *testing.T) {
	for _, c := range []struct{ a, b any }{
		{1.5, "1.5"}, {"1.5", 1.5}, {float64(16), "0x10"}, {0.5, "1/2"}, {json.Number("1"), "1"}, {"1", json.Number("1")}, {1, "1"}, {1.5, true}, {1.5, nil}, {1.5, []any{1.5}}, {1.5, map[string]any{"a": 1.5}},
		{[]any{1.5}, []any{"1.5"}}, {[]any{"1.5"}, []any{1.5}}, {map[string]any{"a": 1.5}, map[string]any{"a": "1.5"}}, {map[string]any{"a": "1.5"}, map[string]any{"a": 1.5}},
	} {
		if ok, kind := equals(c.a, c.b); ok || kind != nil {
			t.Errorf("equals(%#v, %#v) = %v, %v", c.a, c.b, ok, kind)
		}
	}
	for _, c := range []struct{ a, b any }{{1.5, 1.5}, {json.Number("1.50"), 1.5}, {1, json.Number("1")}, {float64(2), 2}, {"a", "a"}} {
		if ok, kind := equals(c.a, c.b); !ok || kind != nil {
			t.Errorf("equals(%#v, %#v) = %v, %v", c.a, c.b, ok, kind)
		}
	}
}
