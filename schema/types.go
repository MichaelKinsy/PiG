// Package schema validates JSON values against JSON Schema drafts 2020-12 and 7 without code generation: Compile
// builds a schema once into closures, and the builders return plain JSON Schema objects.
package schema

import "github.com/MichaelKinsy/PiG/internal/chord/chordjson"

// Ports packages/schema/src/types.ts

// Object is a JSON object that keeps JavaScript's property order (own keys: array-index keys ascending, then insertion order).
type Object = chordjson.Object

// JsonSchema is a JSON Schema: a bool, an *Object or a map[string]any. Schemas come from tool definitions, MCP servers and other untrusted sources, so Compile reads every keyword defensively instead of trusting a static shape.
type JsonSchema = any

// FormatCheck reports whether value has the format.
type FormatCheck func(value string) bool

// Draft is the draft whose semantics apply.
type Draft string

const (
	// Draft202012 is draft 2020-12, the default.
	Draft202012 Draft = "2020-12"
	// Draft7 is draft 7.
	Draft7 Draft = "7"
)

// CompileOptions are the options of Compile.
type CompileOptions struct {
	// Draft is the draft whose semantics apply: Draft202012 (default, also for "") or Draft7.
	Draft Draft
	// Formats maps a format name to its check. Without it `format` is an annotation, as draft 2020-12 specifies. The standard formats are in package formats.
	Formats map[string]FormatCheck
	// Schemas holds schema documents by URI, for `$ref`s to other documents.
	Schemas map[string]JsonSchema
}

// ValidationError is one failure of a value: where it is (a JSON Pointer, "" for the value itself), the failing keyword and a message.
type ValidationError struct {
	Path    string
	Keyword string
	Message string
}

// Check is a compiled boolean check.
type Check func(value any) bool
