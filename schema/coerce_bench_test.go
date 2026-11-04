package schema_test

// Benchmarks of Coerce and Parse for tool-like schemas (the arguments of every tool call pass through them): the read tool, and the edit tool with one and with a thousand edits, each for arguments that need no coercion (the common case: it must not allocate) and for arguments with numeric strings and a single value where an array belongs.
// Run: go test ./schema -run '^$' -bench 'Coerce|Parse' -benchmem

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/schema"
)

const readToolSchema = `{
	"type": "object",
	"properties": {
		"path": { "type": "string" },
		"offset": { "type": "integer", "minimum": 1 },
		"limit": { "type": "integer", "minimum": 1 }
	},
	"required": ["path"],
	"additionalProperties": false
}`

const editToolSchema = `{
	"type": "object",
	"properties": {
		"path": { "type": "string" },
		"edits": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": { "oldText": { "type": "string" }, "newText": { "type": "string" } },
				"required": ["oldText", "newText"],
				"additionalProperties": false
			}
		}
	},
	"required": ["path", "edits"],
	"additionalProperties": false
}`

// editArguments are the arguments of the edit tool with the given number of edits, or, for loose, one edit object where an array belongs and a number where a string belongs.
func editArguments(b *testing.B, edits int, loose bool) any {
	b.Helper()
	if loose {
		return mustDecode(b, `{"path":"src/a.go","edits":{"oldText":"old","newText":7}}`)
	}
	var text strings.Builder
	text.WriteString(`{"path":"src/a.go","edits":[`)
	for i := range edits {
		if i > 0 {
			text.WriteByte(',')
		}
		fmt.Fprintf(&text, `{"oldText":"old %d","newText":"new %d"}`, i, i)
	}
	text.WriteString(`]}`)
	return mustDecode(b, text.String())
}

func benchmarkCoercing(b *testing.B, schemaText string, input any, parse bool) {
	b.Helper()
	validator, err := schema.CompileCoercing(mustDecode(b, schemaText), schema.CompileOptions{})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := validator.Coerce(input); err != nil { // builds the coercer
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if parse {
			if _, err := validator.Parse(input); err != nil {
				b.Fatal(err)
			}
		} else if _, err := validator.Coerce(input); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCoerceRead(b *testing.B) {
	b.Run("valid arguments", func(b *testing.B) {
		benchmarkCoercing(b, readToolSchema, mustDecode(b, `{"path":"a.txt","offset":10,"limit":20}`), false)
	})
	b.Run("numeric strings", func(b *testing.B) {
		benchmarkCoercing(b, readToolSchema, mustDecode(b, `{"path":"a.txt","offset":"10","limit":"20"}`), false)
	})
	b.Run("optional nulls", func(b *testing.B) {
		benchmarkCoercing(b, readToolSchema, mustDecode(b, `{"path":"a.txt","offset":null,"limit":null}`), false)
	})
}

func BenchmarkCoerceEdit(b *testing.B) {
	for _, edits := range []int{1, 1000} {
		b.Run(fmt.Sprintf("%d edits", edits), func(b *testing.B) {
			benchmarkCoercing(b, editToolSchema, editArguments(b, edits, false), false)
		})
	}
	b.Run("single edit object", func(b *testing.B) {
		benchmarkCoercing(b, editToolSchema, editArguments(b, 1, true), false)
	})
}

func BenchmarkParseRead(b *testing.B) {
	b.Run("valid arguments", func(b *testing.B) {
		benchmarkCoercing(b, readToolSchema, mustDecode(b, `{"path":"a.txt","offset":10,"limit":20}`), true)
	})
	b.Run("numeric strings", func(b *testing.B) {
		benchmarkCoercing(b, readToolSchema, mustDecode(b, `{"path":"a.txt","offset":"10","limit":"20"}`), true)
	})
}

func BenchmarkParseEdit(b *testing.B) {
	for _, edits := range []int{1, 1000} {
		b.Run(fmt.Sprintf("%d edits", edits), func(b *testing.B) {
			benchmarkCoercing(b, editToolSchema, editArguments(b, edits, false), true)
		})
	}
}
