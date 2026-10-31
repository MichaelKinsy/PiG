package schema

// Ports packages/schema/src/builders.ts
//
// Schema builders: short functions that return plain JSON Schema objects. The objects are ordinary data: they serialize as JSON and compile with Compile like any other schema.
//
// Pi's builders are generic over the TypeScript type of the values they accept (`Schema<T>`, `Static<S>`, `StaticObject<P>`, `Annotations<T>`, `LengthBounds`, `NumberBounds`, `Optional`); those are compile-time types with no Go form. An option object that TypeScript spreads into the schema becomes Option values applied in call order, so members appear in the order the caller wrote them, as the spread produces them.

import "github.com/MichaelKinsy/PiG/internal/chord/chordjson"

// Schema is a JSON Schema object returned by a builder.
type Schema = *Object

// optionSet is what an Option writes to: the schema under construction and the builder flags.
type optionSet struct {
	object *Object
	open   bool
}

// Option sets one keyword or builder flag of a builder's schema. Annotation options (Title, Description, Examples, Deprecated, Default) describe the schema and never change what it accepts; a `Default` is an annotation for tools and forms, validation never inserts it.
type Option func(*optionSet)

func keyword(name string, value any) Option {
	return func(s *optionSet) { s.object.Set(name, value) }
}

// scalar reads a Go number as the float64 JSON holds.
func scalar(v any) any {
	if n, ok := asNumber(v); ok {
		return n
	}
	return v
}

// Title sets `title`.
func Title(text string) Option { return keyword("title", text) }

// Description sets `description`.
func Description(text string) Option { return keyword("description", text) }

// Examples sets `examples`.
func Examples(values ...any) Option {
	list := make([]any, len(values))
	for i, v := range values {
		list[i] = scalar(v)
	}
	return keyword("examples", list)
}

// Deprecated sets `deprecated`.
func Deprecated(deprecated bool) Option { return keyword("deprecated", deprecated) }

// Default sets `default`.
func Default(value any) Option { return keyword("default", scalar(value)) }

// MinLength sets `minLength` of a string or bytes, in code points (bytes: in bytes).
func MinLength(n int) Option { return keyword("minLength", float64(n)) }

// MaxLength sets `maxLength` of a string or bytes.
func MaxLength(n int) Option { return keyword("maxLength", float64(n)) }

// Pattern sets `pattern` of a string.
func Pattern(source string) Option { return keyword("pattern", source) }

// Format sets `format` of a string.
func Format(name string) Option { return keyword("format", name) }

// Minimum sets `minimum` of a number.
func Minimum(n float64) Option { return keyword("minimum", n) }

// Maximum sets `maximum` of a number.
func Maximum(n float64) Option { return keyword("maximum", n) }

// ExclusiveMinimum sets `exclusiveMinimum` of a number.
func ExclusiveMinimum(n float64) Option { return keyword("exclusiveMinimum", n) }

// ExclusiveMaximum sets `exclusiveMaximum` of a number.
func ExclusiveMaximum(n float64) Option { return keyword("exclusiveMaximum", n) }

// MultipleOf sets `multipleOf` of a number.
func MultipleOf(n float64) Option { return keyword("multipleOf", n) }

// MinItems sets `minItems` of an array.
func MinItems(n int) Option { return keyword("minItems", float64(n)) }

// MaxItems sets `maxItems` of an array.
func MaxItems(n int) Option { return keyword("maxItems", float64(n)) }

// UniqueItems sets `uniqueItems` of an array.
func UniqueItems(unique bool) Option { return keyword("uniqueItems", unique) }

// Open lets an Obj schema allow properties it does not declare, as tool schemas from other systems often do.
func Open() Option { return func(s *optionSet) { s.open = true } }

func build(object *Object, options []Option) *Object {
	set := optionSet{object: object}
	for _, option := range options {
		option(&set)
	}
	return object
}

func typed(name string, options []Option) Schema {
	object := build(chordjson.ObjectOf("type", name), options)
	return object
}

// Str is a string, with optional length bounds (in code points), pattern and format.
func Str(options ...Option) Schema { return typed("string", options) }

// Num is a number within optional bounds.
func Num(options ...Option) Schema { return typed("number", options) }

// Int is an integer within optional bounds.
func Int(options ...Option) Schema { return typed("integer", options) }

// Bool is a boolean.
func Bool(options ...Option) Schema { return typed("boolean", options) }

// Nul is `null`.
func Nul(options ...Option) Schema { return typed("null", options) }

// Unknown is any value.
func Unknown(options ...Option) Schema {
	object := build(&Object{}, options)
	return object
}

// Lit is exactly value (a string, number, boolean or nil).
func Lit(value any, options ...Option) Schema {
	object := build(chordjson.ObjectOf("const", scalar(value)), options)
	return object
}

// OneOf is one of the given values.
func OneOf(values []any, options ...Option) Schema {
	list := make([]any, len(values))
	for i, v := range values {
		list[i] = scalar(v)
	}
	object := build(chordjson.ObjectOf("enum", list), options)
	return object
}

func schemaList(schemas []Schema) []any {
	list := make([]any, len(schemas))
	for i, s := range schemas {
		list[i] = s
	}
	return list
}

// Union is any of the given schemas. A union of objects tagged by a Lit property (`{ kind: Lit("text"), ... }`) is checked by the tag: only the branch it selects runs, and its errors are the ones reported.
func Union(schemas []Schema, options ...Option) Schema {
	object := build(chordjson.ObjectOf("anyOf", schemaList(schemas)), options)
	return object
}

// Nullable is schema or null.
func Nullable(schema Schema, options ...Option) Schema {
	object := build(chordjson.ObjectOf("anyOf", []any{schema, chordjson.ObjectOf("type", "null")}), options)
	return object
}

// Arr is an array of items, with optional length bounds and unique items.
func Arr(items Schema, options ...Option) Schema {
	object := build(chordjson.ObjectOf("type", "array", "items", items), options)
	return object
}

// Tuple is a fixed-length array whose items match items in order.
func Tuple(items []Schema, options ...Option) Schema {
	object := build(chordjson.ObjectOf("type", "array", "prefixItems", schemaList(items), "items", false, "minItems", float64(len(items))), options)
	return object
}

// Map is an object with string keys and values of value (a record).
func Map(value Schema, options ...Option) Schema {
	object := build(chordjson.ObjectOf("type", "object", "additionalProperties", value), options)
	return object
}

// Bytes is a []byte with optional length bounds (an extension of JSON Schema for values that never pass through JSON).
func Bytes(options ...Option) Schema { return typed("bytes", options) }

// Property is a property of Obj, in declaration order.
type Property struct {
	Name     string
	Schema   Schema
	Optional bool
}

// Properties are the properties of Obj in declaration order, which is the order of its `properties` and `required` and of the errors it reports.
type Properties []Property

// Prop is a required property of Obj.
func Prop(name string, schema Schema) Property { return Property{Name: name, Schema: schema} }

// Opt is an optional property of Obj: `Opt("name", Str())` may be omitted.
func Opt(name string, schema Schema) Property {
	return Property{Name: name, Schema: schema, Optional: true}
}

// Obj is a closed object: the given properties (required unless Opt) and no others. The Open option allows other properties, as tool schemas from other systems often do.
func Obj(properties Properties, options ...Option) Schema {
	props := &Object{}
	var required []any
	for _, p := range properties {
		props.Set(p.Name, p.Schema)
		if !p.Optional {
			required = append(required, p.Name)
		}
	}
	annotations := &Object{}
	set := optionSet{object: annotations}
	for _, option := range options {
		option(&set)
	}
	object := chordjson.ObjectOf("type", "object", "properties", props)
	if len(required) > 0 {
		object.Set("required", required)
	}
	if !set.open {
		object.Set("additionalProperties", false)
	}
	for k, v := range annotations.All() {
		object.Set(k, v)
	}
	return object
}
