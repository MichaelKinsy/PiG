package host

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

const benchSchemaText = `{"type":"object","properties":{"name":{"type":"string","default":"x"},"limit":{"type":"integer","default":10},"tags":{"type":"array","items":{"type":"string"}},"nested":{"type":"object","properties":{"on":{"type":"boolean","default":false}}}},"required":["name"],"additionalProperties":false}`

func decodedBenchSchema(b *testing.B) Schema {
	b.Helper()
	var description Description
	text := `{"name":"t","scope":"local","version":1,"input":` + benchSchemaText + `,"variants":{"":{}}}`
	if err := json.Unmarshal([]byte(text), &description); err != nil {
		b.Fatal(err)
	}
	return description.Input
}

func mapBenchSchema(b *testing.B) Schema {
	b.Helper()
	var schema Schema
	if err := json.Unmarshal([]byte(benchSchemaText), &schema); err != nil {
		b.Fatal(err)
	}
	return schema
}

// BenchmarkSchemaPaths is the success path of the schema functions: Normalize and Fits over a Go-map schema and over the
// schema of a decoded wire description (what a Node child or a hold token delivers).
func BenchmarkSchemaPaths(b *testing.B) {
	value, err := chordjson.Decode([]byte(`{"name":"a","tags":["p","q"],"nested":{}}`))
	if err != nil {
		b.Fatal(err)
	}
	for _, source := range []struct {
		name   string
		schema func(*testing.B) Schema
	}{{"map", mapBenchSchema}, {"decoded", decodedBenchSchema}} {
		schema := source.schema(b)
		b.Run("Normalize/"+source.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Normalize(schema, value, "input"); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("Check/"+source.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := assertValid("input", "input", schema, value); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("Fits/"+source.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if !Fits(schema, schema) {
					b.Fatal("does not fit")
				}
			}
		})
		b.Run("Identity/"+source.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = CanonicalIdentity(schema, value)
			}
		})
	}
}

// BenchmarkDecodeDescription is what a decoded description costs once, and what it keeps: the allocated bytes and objects of
// one UnmarshalJSON are, within a few container headers, what the decoded schemas retain for the contract's lifetime.
func BenchmarkDecodeDescription(b *testing.B) {
	text := []byte(`{"name":"t","scope":"local","version":1,"input":` + benchSchemaText + `,"variants":{"":{"m":{"kind":"method","args":` + benchSchemaText + `,"result":` + benchSchemaText + `},"s":{"kind":"state","schema":` + benchSchemaText + `}}}}`)
	b.ReportAllocs()
	for b.Loop() {
		var description Description
		if err := json.Unmarshal(text, &description); err != nil {
			b.Fatal(err)
		}
	}
}
