package contracts

import (
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/contracts/schema.ts (Chord's short constructors over pi-schema's builders: plain JSON
// Schema, and method and keyed member declarations). The helpers keep the shapes contracts were fingerprinted with:
// literals carry their `type`, OneOf is a union of literals, Map a pattern-property record; Obj follows pi-schema's
// builder order (type, properties, required, additionalProperties).
//
// Forced Go differences. Schema is the schema as data: the node (an ordered *delta.JsonObject, or a bool) plus the
// Optional flag, which stands for pi-schema's `Optional` wrapper of `opt(...)` (a value that is no schema: only an
// object field takes it, and a snapshot refuses it anywhere else). The generic Static<>, Defaulted<> and Bounds
// types and the typed scalar schemas (StringSchema, IntegerSchema, ..., LiteralsSchema, NullSchema) are type-level
// only and designed out; bounds and defaults are Option values (Min, Max, Pattern, Default), and a constructor panics
// on an option that does not apply to it, where TypeScript rejects it at compile time. withBytesCheck is deleted
// upstream: a bytes leaf is plain data, checked natively by pi-schema's validator (a []byte is its Uint8Array).

// Entry is one name and value of an object literal whose key order matters: Go has no ordered literal.
type Entry[T any] struct {
	Name  string
	Value T
}

// Property is one property of an object schema, or one field of params.
type Property = Entry[Schema]

// Prop is `{ [name]: schema }`.
func Prop(name string, schema Schema) Property { return Property{Name: name, Value: schema} }

// Params is a flat record of required fields, in identity order (see ReadParams).
type Params []Property

// Schema is a pi-schema schema: JSON Schema data. Node is an ordered *delta.JsonObject or a bool; the zero Schema
// (a nil Node) is no schema, upstream's `undefined`.
type Schema struct {
	Node delta.JsonValue
	// Optional is pi-schema's `Optional` wrapper: an object field made optional by Opt.
	Optional bool
}

func (Schema) localMember() {}

// Option is a bound or a default for a schema constructor.
type Option struct {
	name  string
	apply func(*options)
}

type options struct {
	min, max   *float64
	pattern    *string
	fallback   any
	hasDefault bool
}

// Min is the lower bound of a length, a number or an array length (`min`).
func Min(n float64) Option { return Option{"Min", func(o *options) { o.min = &n }} }

// Max is the upper bound of a length, a number or an array length (`max`).
func Max(n float64) Option { return Option{"Max", func(o *options) { o.max = &n }} }

// Pattern is the regular expression a string matches (`pattern`).
func Pattern(pattern string) Option {
	return Option{"Pattern", func(o *options) { o.pattern = &pattern }}
}

// Default is the declared default value (`default`): a string for Str, a number for Num and Int, a bool for Bool.
func Default(value any) Option {
	return Option{"Default", func(o *options) { o.fallback, o.hasDefault = value, true }}
}

// read applies the options of constructor, which accepts the named ones; defaults are checked by kind.
func read(constructor string, allowed []string, kind func(any) (any, bool), opts []Option) options {
	var out options
	for _, option := range opts {
		ok := false
		for _, name := range allowed {
			ok = ok || name == option.name
		}
		if !ok {
			panic(fmt.Sprintf("contracts: %s does not take the option %s", constructor, option.name))
		}
		option.apply(&out)
	}
	if out.hasDefault {
		value, ok := kind(out.fallback)
		if !ok {
			panic(fmt.Sprintf("contracts: %s: the default %v has the wrong type", constructor, out.fallback))
		}
		out.fallback = value
	}
	return out
}

func stringKind(value any) (any, bool) {
	text, ok := value.(string)
	return text, ok
}

func numberKind(value any) (any, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	}
	return nil, false
}

func boolKind(value any) (any, bool) {
	flag, ok := value.(bool)
	return flag, ok
}

var (
	lengthOptions = []string{"Min", "Max"}
	stringOptions = []string{"Min", "Max", "Pattern", "Default"}
	numberOptions = []string{"Min", "Max", "Default"}
	boolOptions   = []string{"Default"}
)

// setLimits writes the bounds under their JSON Schema names; an absent bound is left out.
func setLimits(node *delta.JsonObject, o options, lower, upper string) {
	if o.min != nil {
		node.Set(lower, *o.min)
	}
	if o.max != nil {
		node.Set(upper, *o.max)
	}
}

func setDefault(node *delta.JsonObject, o options) {
	if o.hasDefault {
		node.Set("default", o.fallback)
	}
}

// Str is a string, with optional length bounds, pattern and default.
func Str(opts ...Option) Schema {
	o := read("Str", stringOptions, stringKind, opts)
	node := delta.JsonObjectOf("type", "string")
	setLimits(node, o, "minLength", "maxLength")
	if o.pattern != nil {
		node.Set("pattern", *o.pattern)
	}
	setDefault(node, o)
	return Schema{Node: node}
}

// Num is a finite number within optional bounds, with an optional default.
func Num(opts ...Option) Schema {
	o := read("Num", numberOptions, numberKind, opts)
	node := delta.JsonObjectOf("type", "number")
	setLimits(node, o, "minimum", "maximum")
	setDefault(node, o)
	return Schema{Node: node}
}

// Int is an integer within optional bounds, with an optional default.
func Int(opts ...Option) Schema {
	o := read("Int", numberOptions, numberKind, opts)
	node := delta.JsonObjectOf("type", "integer")
	setLimits(node, o, "minimum", "maximum")
	setDefault(node, o)
	return Schema{Node: node}
}

// Bool is a boolean, with an optional default.
func Bool(opts ...Option) Schema {
	o := read("Bool", boolOptions, boolKind, opts)
	node := delta.JsonObjectOf("type", "boolean")
	setDefault(node, o)
	return Schema{Node: node}
}

// Nul is `null`; also the omitted side of a method.
func Nul() Schema { return Schema{Node: delta.JsonObjectOf("type", "null")} }

// Literal is the Go form of `string | number | boolean`.
type Literal interface {
	~string | ~bool | ~int | ~int64 | ~float64
}

// literalSchema is `{ type: typeof value, const: value }`: a literal with its JSON type.
func literalSchema[V Literal](value V) Schema {
	switch typed := any(value).(type) {
	case string:
		return Schema{Node: delta.JsonObjectOf("type", "string", "const", typed)}
	case bool:
		return Schema{Node: delta.JsonObjectOf("type", "boolean", "const", typed)}
	}
	var number float64
	switch typed := any(value).(type) {
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case float64:
		number = typed
	}
	return Schema{Node: delta.JsonObjectOf("type", "number", "const", number)}
}

// Lit is exactly the given value.
func Lit[V Literal](value V) Schema { return literalSchema(value) }

// OneOf is one of the given literals: a union of one literal schema per value, in order.
func OneOf[V Literal](values ...V) Schema {
	schemas := make([]Schema, len(values))
	for i, value := range values {
		schemas[i] = literalSchema(value)
	}
	return Union(schemas)
}

// Union is any of the given schemas.
func Union(schemas []Schema) Schema {
	refuseOptional("Union", schemas...)
	nodes := make([]any, len(schemas))
	for i, schema := range schemas {
		nodes[i] = schema.Node
	}
	return Schema{Node: delta.JsonObjectOf("anyOf", nodes)}
}

// Obj is a closed object: unknown fields are refused. Properties are read in the order JavaScript enumerates an
// object's own keys; a field made optional by Opt is left out of `required`.
func Obj(properties ...Property) Schema {
	ordered := orderedEntries(properties)
	props := delta.NewJsonObject(len(ordered))
	var required []any
	for _, property := range ordered {
		props.Set(property.Name, property.Value.Node)
		if !property.Value.Optional {
			required = append(required, property.Name)
		}
	}
	node := delta.JsonObjectOf("type", "object", "properties", props)
	if len(required) > 0 {
		node.Set("required", required)
	}
	node.Set("additionalProperties", false)
	return Schema{Node: node}
}

// Arr is an array, with optional length bounds.
func Arr(items Schema, opts ...Option) Schema {
	refuseOptional("Arr", items)
	o := read("Arr", lengthOptions, nil, opts)
	node := delta.JsonObjectOf("type", "array", "items", items.Node)
	setLimits(node, o, "minItems", "maxItems")
	return Schema{Node: node}
}

// Map is a string-keyed record: one pattern property matching every key.
func Map(value Schema) Schema {
	refuseOptional("Map", value)
	return Schema{Node: delta.JsonObjectOf("type", "object", "patternProperties", delta.JsonObjectOf("^.*$", value.Node))}
}

// Opt is an optional object field.
func Opt(schema Schema) Schema {
	schema.Optional = true
	return schema
}

// Unknown is any value.
func Unknown() Schema { return Schema{Node: delta.NewJsonObject(0)} }

// Bytes is a `Uint8Array` leaf, a []byte in Go: `{ type: "bytes", minLength?, maxLength? }` (pi-schema's extension of
// JSON Schema for values that never pass through JSON).
func Bytes(opts ...Option) Schema {
	o := read("Bytes", lengthOptions, nil, opts)
	node := delta.JsonObjectOf("type", "bytes")
	setLimits(node, o, "minLength", "maxLength")
	return Schema{Node: node}
}

// Method is a method declaration: its argument and result schemas; an omitted side is `null` on the wire.
type Method struct {
	Args   Schema
	Result Schema
}

func (*Method) localMember() {}

// KeyedMember is a keyed state member's declaration (slice 6a §12): documents of Schema by string key; only in local
// contracts.
type KeyedMember struct {
	Schema Schema
}

func (*KeyedMember) localMember() {}

// Keyed declares a keyed state member of a local contract: documents of schema by string key, each watchable.
func Keyed(schema Schema) *KeyedMember {
	refuseOptional("Keyed", schema)
	return &KeyedMember{Schema: schema}
}

// refuseOptional panics when a schema is opt(...): it marks an object property and is no schema anywhere else.
// TypeScript rejects it where a Schema is expected (`arr`, `map`, `union`, `keyed`, `method`), and a snapshot refuses it
// with `<path>: expected a schema`; Go has no compile-time check, so the constructor refuses it, as it refuses an option
// that does not apply.
func refuseOptional(constructor string, schemas ...Schema) {
	for _, schema := range schemas {
		if schema.Optional {
			panic("contracts: " + constructor + ": opt(...) is no schema outside an object")
		}
	}
}

// NewMethod is `method(args?, result?)` (upstream's `method`, which Go cannot name beside the Method type): no side,
// `args`, or `args, result`. A side that is the zero Schema is omitted, as `method(undefined, result)` omits the
// arguments, and is `null`.
func NewMethod(sides ...Schema) *Method {
	if len(sides) > 2 {
		panic("contracts: NewMethod takes at most an argument and a result schema")
	}
	refuseOptional("NewMethod", sides...)
	method := &Method{Args: Nul(), Result: Nul()}
	if len(sides) > 0 && sides[0].Node != nil {
		method.Args = sides[0]
	}
	if len(sides) > 1 && sides[1].Node != nil {
		method.Result = sides[1]
	}
	return method
}

// LocalMember is a local contract's member: a state schema (Schema), a *Method or a *KeyedMember.
type LocalMember interface {
	localMember()
}

// orderedEntries returns entries as JavaScript would enumerate the same object literal: array-index names first in
// ascending order, then the others in the given order; a repeated name keeps its first place and takes its last value.
func orderedEntries[T any](entries []Entry[T]) []Entry[T] {
	out := make([]Entry[T], 0, len(entries))
	at := map[string]int{}
	for _, entry := range entries {
		if i, seen := at[entry.Name]; seen {
			out[i].Value = entry.Value
			continue
		}
		at[entry.Name] = len(out)
		out = append(out, entry)
	}
	ownKeyOrder(out, func(entry Entry[T]) string { return entry.Name })
	return out
}
