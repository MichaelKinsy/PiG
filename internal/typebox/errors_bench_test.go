package typebox

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// BenchmarkErrors is the failing-validation path (Errors runs only after Check failed): a wrong type, an object of
// members an additionalProperties:false schema refuses, and a nested value with several wrong members.
func BenchmarkErrors(b *testing.B) {
	object := chordjson.ObjectOf
	cases := []struct {
		name          string
		schema, value any
	}{
		{"type", map[string]any{"type": "string"}, float64(1)},
		{"additional", map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "additionalProperties": false},
			object("z", float64(1), "a", "x", "m", float64(2), "b", float64(3), "q", float64(4), "c", float64(5))},
		{"nested", map[string]any{"type": "object", "properties": map[string]any{
			"a": map[string]any{"type": "string"}, "b": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			"c": map[string]any{"type": "object", "required": []any{"x", "y"}}}},
			object("a", float64(1), "b", []any{"p", float64(2), "q"}, "c", object())},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if len(Errors(tc.schema, tc.value)) == 0 {
					b.Fatal("no errors")
				}
			}
		})
	}
}
