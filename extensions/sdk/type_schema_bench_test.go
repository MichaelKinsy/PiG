package sdk

import "testing"

// BenchmarkTypeObject builds the tool parameters of a typical extension tool (five properties, two optional, a union, an array and a record) and marshals them.
func BenchmarkTypeObject(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		schema := Type.Object(NewOrderedObject(
			"query", Type.String(NewOrderedObject("description", "What to look up")),
			"limit", Type.Optional(Type.Integer(NewOrderedObject("minimum", 1, "maximum", 50))),
			"mode", Type.Union([]any{Type.Literal("fast"), Type.Literal("deep")}),
			"tags", Type.Optional(Type.Array(Type.String())),
			"filter", Type.Optional(Type.Record(Type.String(), Type.Number())),
		), NewOrderedObject("additionalProperties", false))
		if _, err := schema.MarshalJSON(); err != nil {
			b.Fatal(err)
		}
	}
}
