package schema

// Ports packages/schema/src/builders.ts
//
// Schema builders: short functions that return plain JSON Schema objects. The objects are ordinary data: they serialize as JSON and compile with Compile like any other schema.
//
// Options are applied in call order, so the members of a schema appear in the order the caller wrote them, as the members of the options object Pi's builders spread do. Each builder takes the options that apply to its type: the annotations (Title, Description, Examples, Deprecated, Default) fit every builder, MinLength and MaxLength fit Str and Bytes, Pattern and Format fit Str, the number bounds fit Num and Int, the bigint bounds fit BigInt, the item bounds fit Arr and Open fits Obj. A misplaced option is a compile error, as an excess property of Pi's option types is.

import (
	"math/big"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Schema is a JSON Schema object returned by a builder.
type Schema = *Object

// keywordOption is an option that writes one member of a builder's schema.
type keywordOption interface {
	keyword() (name string, value any)
}

// AnnotationOption is an option every builder accepts: it describes the schema and never changes what it accepts. It satisfies every builder's option type.
type AnnotationOption interface {
	StrOption
	NumberOption
	BigIntOption
	ArrayOption
	BytesOption
	ObjectOption
	annotation()
}

// LengthOption is an option of Str and Bytes.
type LengthOption interface {
	StrOption
	BytesOption
}

// StrOption is an option of Str.
type StrOption interface {
	keywordOption
	forStr()
}

// NumberOption is an option of Num and Int.
type NumberOption interface {
	keywordOption
	forNumber()
}

// BigIntOption is an option of BigInt.
type BigIntOption interface {
	keywordOption
	forBigInt()
}

// ArrayOption is an option of Arr.
type ArrayOption interface {
	keywordOption
	forArray()
}

// BytesOption is an option of Bytes.
type BytesOption interface {
	keywordOption
	forBytes()
}

// ObjectOption is an option of Obj.
type ObjectOption interface {
	keywordOption
	forObject()
}

// annotation is a member that fits every builder.
type annotation struct {
	name  string
	value any
}

func (a annotation) keyword() (string, any) { return a.name, a.value }
func (annotation) annotation()              {}
func (annotation) forStr()                  {}
func (annotation) forNumber()               {}
func (annotation) forBigInt()               {}
func (annotation) forArray()                {}
func (annotation) forBytes()                {}
func (annotation) forObject()               {}

// lengthBound is a length bound of a string or of bytes.
type lengthBound struct {
	name  string
	value float64
}

func (b lengthBound) keyword() (string, any) { return b.name, b.value }
func (lengthBound) forStr()                  {}
func (lengthBound) forBytes()                {}

// stringKeyword is a member only a string has.
type stringKeyword struct{ name, value string }

func (k stringKeyword) keyword() (string, any) { return k.name, k.value }
func (stringKeyword) forStr()                  {}

// numberBound is a bound of a number or an integer.
type numberBound struct {
	name  string
	value float64
}

func (b numberBound) keyword() (string, any) { return b.name, b.value }
func (numberBound) forNumber()               {}

// bigIntBound is a bound of a bigint: TypeBox's `Type.BigInt` writes its bounds as bigints.
type bigIntBound struct {
	name  string
	value *big.Int
}

func (b bigIntBound) keyword() (string, any) { return b.name, b.value }
func (bigIntBound) forBigInt()               {}

// itemKeyword is a bound of an array.
type itemKeyword struct {
	name  string
	value any
}

func (k itemKeyword) keyword() (string, any) { return k.name, k.value }
func (itemKeyword) forArray()                {}

// openOption lets an Obj schema allow other properties; it writes no member.
type openOption struct{}

func (openOption) keyword() (string, any) { return "", nil }
func (openOption) forObject()             {}

// scalar reads a Go number as the float64 JSON holds.
func scalar(v any) any {
	if n, ok := asNumber(v); ok {
		return n
	}
	return v
}

// Title sets `title`.
func Title(text string) AnnotationOption { return annotation{"title", text} }

// Description sets `description`.
func Description(text string) AnnotationOption { return annotation{"description", text} }

// Examples sets `examples`.
func Examples(values ...any) AnnotationOption {
	list := make([]any, len(values))
	for i, v := range values {
		list[i] = scalar(v)
	}
	return annotation{"examples", list}
}

// Deprecated sets `deprecated`.
func Deprecated(deprecated bool) AnnotationOption { return annotation{"deprecated", deprecated} }

// Default sets `default`, an annotation for tools and forms: validation never inserts it.
func Default(value any) AnnotationOption { return annotation{"default", scalar(value)} }

// MinLength sets `minLength` of a string, in code points, or of bytes, in bytes.
func MinLength(n int) LengthOption {
	return lengthBound{"minLength", float64(n)}
}

// MaxLength sets `maxLength` of a string, in code points, or of bytes, in bytes.
func MaxLength(n int) LengthOption {
	return lengthBound{"maxLength", float64(n)}
}

// Pattern sets `pattern` of a string.
func Pattern(source string) StrOption { return stringKeyword{"pattern", source} }

// Format sets `format` of a string.
func Format(name string) StrOption { return stringKeyword{"format", name} }

// Minimum sets `minimum` of a number.
func Minimum(n float64) NumberOption { return numberBound{"minimum", n} }

// Maximum sets `maximum` of a number.
func Maximum(n float64) NumberOption { return numberBound{"maximum", n} }

// ExclusiveMinimum sets `exclusiveMinimum` of a number.
func ExclusiveMinimum(n float64) NumberOption { return numberBound{"exclusiveMinimum", n} }

// ExclusiveMaximum sets `exclusiveMaximum` of a number.
func ExclusiveMaximum(n float64) NumberOption { return numberBound{"exclusiveMaximum", n} }

// MultipleOf sets `multipleOf` of a number.
func MultipleOf(n float64) NumberOption { return numberBound{"multipleOf", n} }

// BigMinimum sets `minimum` of a BigInt.
func BigMinimum(n *big.Int) BigIntOption { return bigIntBound{"minimum", n} }

// BigMaximum sets `maximum` of a BigInt.
func BigMaximum(n *big.Int) BigIntOption { return bigIntBound{"maximum", n} }

// BigExclusiveMinimum sets `exclusiveMinimum` of a BigInt.
func BigExclusiveMinimum(n *big.Int) BigIntOption { return bigIntBound{"exclusiveMinimum", n} }

// BigExclusiveMaximum sets `exclusiveMaximum` of a BigInt.
func BigExclusiveMaximum(n *big.Int) BigIntOption { return bigIntBound{"exclusiveMaximum", n} }

// BigMultipleOf sets `multipleOf` of a BigInt.
func BigMultipleOf(n *big.Int) BigIntOption { return bigIntBound{"multipleOf", n} }

// MinItems sets `minItems` of an array.
func MinItems(n int) ArrayOption { return itemKeyword{"minItems", float64(n)} }

// MaxItems sets `maxItems` of an array.
func MaxItems(n int) ArrayOption { return itemKeyword{"maxItems", float64(n)} }

// UniqueItems sets `uniqueItems` of an array.
func UniqueItems(unique bool) ArrayOption { return itemKeyword{"uniqueItems", unique} }

// Open lets an Obj schema allow properties it does not declare, as tool schemas from other systems often do.
func Open() ObjectOption { return openOption{} }

// with appends the members of options to object in call order.
func with[O keywordOption](object *Object, options []O) *Object {
	for _, option := range options {
		name, value := option.keyword()
		object.Set(name, value)
	}
	return object
}

// Str is a string, with optional length bounds (in code points), pattern and format.
func Str(options ...StrOption) Schema { return with(chordjson.ObjectOf("type", "string"), options) }

// Num is a number within optional bounds.
func Num(options ...NumberOption) Schema { return with(chordjson.ObjectOf("type", "number"), options) }

// Int is an integer within optional bounds.
func Int(options ...NumberOption) Schema { return with(chordjson.ObjectOf("type", "integer"), options) }

// BigInt is a bigint (`{"type":"bigint"}`, as TypeBox's `Type.BigInt`) within optional bounds: an extension of JSON Schema for values that never pass through JSON, such as the 64-bit integers a Parquet reader decodes. Its values are *big.Int.
func BigInt(options ...BigIntOption) Schema {
	return with(chordjson.ObjectOf("type", "bigint"), options)
}

// Bool is a boolean.
func Bool(options ...AnnotationOption) Schema {
	return with(chordjson.ObjectOf("type", "boolean"), options)
}

// Nul is `null`.
func Nul(options ...AnnotationOption) Schema {
	return with(chordjson.ObjectOf("type", "null"), options)
}

// Unknown is any value.
func Unknown(options ...AnnotationOption) Schema { return with(&Object{}, options) }

// Lit is exactly value (a string, number, boolean or nil).
func Lit(value any, options ...AnnotationOption) Schema {
	return with(chordjson.ObjectOf("const", scalar(value)), options)
}

// OneOf is one of the given values.
func OneOf(values []any, options ...AnnotationOption) Schema {
	list := make([]any, len(values))
	for i, v := range values {
		list[i] = scalar(v)
	}
	return with(chordjson.ObjectOf("enum", list), options)
}

func schemaList(schemas []Schema) []any {
	list := make([]any, len(schemas))
	for i, s := range schemas {
		list[i] = s
	}
	return list
}

// Union is any of the given schemas. A union of objects tagged by a Lit property (`{ kind: Lit("text"), ... }`) is checked by the tag: only the branch it selects runs, and its errors are the ones reported.
func Union(schemas []Schema, options ...AnnotationOption) Schema {
	return with(chordjson.ObjectOf("anyOf", schemaList(schemas)), options)
}

// Nullable is schema or null.
func Nullable(schema Schema, options ...AnnotationOption) Schema {
	return with(chordjson.ObjectOf("anyOf", []any{schema, chordjson.ObjectOf("type", "null")}), options)
}

// Arr is an array of items, with optional length bounds and unique items.
func Arr(items Schema, options ...ArrayOption) Schema {
	return with(chordjson.ObjectOf("type", "array", "items", items), options)
}

// Tuple is a fixed-length array whose items match items in order.
func Tuple(items []Schema, options ...AnnotationOption) Schema {
	return with(chordjson.ObjectOf("type", "array", "prefixItems", schemaList(items), "items", false, "minItems", float64(len(items))), options)
}

// Recursive is a schema that contains itself, as a tree's children are trees: build receives a reference to the schema it returns (Pi's `recursive(id, build)`, Pi main af79b0c3d). id becomes the schema's `$id` and the target of the reference, so it must be unique among the recursive schemas within one schema. The result is a copy of what build returns with `$id` set, appended unless the schema has one.
func Recursive(id string, build func(self Schema) Schema) Schema {
	schema := build(chordjson.ObjectOf("$ref", id)).Clone()
	schema.Set("$id", id)
	return schema
}

// Map is an object with string keys and values of value (a record).
func Map(value Schema, options ...AnnotationOption) Schema {
	return with(chordjson.ObjectOf("type", "object", "additionalProperties", value), options)
}

// Bytes is a []byte with optional length bounds (an extension of JSON Schema for values that never pass through JSON).
func Bytes(options ...BytesOption) Schema { return with(chordjson.ObjectOf("type", "bytes"), options) }

// Property is a property of Obj.
type Property struct {
	Name     string
	Schema   Schema
	Optional bool
}

// Properties are the properties of Obj in declaration order. The `properties` and `required` of the schema, and so the order of its errors, follow JavaScript's own-key order: array-index names first in ascending order, then the others in declaration order. A name declared twice keeps its first position and its last declaration.
type Properties []Property

// Prop is a required property of Obj.
func Prop(name string, schema Schema) Property { return Property{Name: name, Schema: schema} }

// Opt is an optional property of Obj: `Opt("name", Str())` may be omitted.
func Opt(name string, schema Schema) Property {
	return Property{Name: name, Schema: schema, Optional: true}
}

// Obj is a closed object: the given properties (required unless Opt) and no others. The Open option allows other properties, as tool schemas from other systems often do.
func Obj(properties Properties, options ...ObjectOption) Schema {
	props := chordjson.NewObject(len(properties))
	optional := make(map[string]bool, len(properties))
	for _, p := range properties {
		props.Set(p.Name, p.Schema)
		optional[p.Name] = p.Optional
	}
	var required []any
	for _, name := range props.Keys() {
		if !optional[name] {
			required = append(required, name)
		}
	}
	open := false
	annotations := &Object{}
	for _, option := range options {
		if _, ok := option.(openOption); ok {
			open = true
			continue
		}
		name, value := option.keyword()
		annotations.Set(name, value)
	}
	object := chordjson.ObjectOf("type", "object", "properties", props)
	if len(required) > 0 {
		object.Set("required", required)
	}
	if !open {
		object.Set("additionalProperties", false)
	}
	for name, value := range annotations.All() {
		object.Set(name, value)
	}
	return object
}
